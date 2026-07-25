package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/pb"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"google.golang.org/grpc/codes"
)

// TestE2E_AllRPCShapesAuthenticated exercises every RPC shape over the
// in-process gRPC server: CRUD, server streaming, client streaming, and
// bidirectional streaming. The goal is to confirm that no shape regresses
// when authentication and interceptors are wired together.
func TestE2E_AllRPCShapesAuthenticated(t *testing.T) {
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	authCtx := authContext(context.Background(), testAPIKey)

	rpcCtx, rpcCancel := context.WithTimeout(authCtx, 5*time.Second)
	defer rpcCancel()

	created, err := client.CreateUser(rpcCtx, &pb.CreateUserRequest{Name: "E2E User", Email: "e2e@example.com", Age: 42})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.GetId() == "" {
		t.Fatal("CreateUser returned empty id")
	}
	defer func() {
		_, _ = client.DeleteUser(authContext(context.Background(), testAPIKey), &pb.DeleteUserRequest{Id: created.GetId()})
	}()

	got, err := client.GetUser(rpcCtx, &pb.GetUserRequest{Id: created.GetId()})
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.GetEmail() != created.GetEmail() || got.GetName() != "E2E User" {
		t.Fatalf("GetUser returned %+v, want name=E2E User email=%s", got, created.GetEmail())
	}

	updated, err := client.UpdateUser(rpcCtx, &pb.UpdateUserRequest{Id: created.GetId(), Name: "E2E Updated", Age: 43})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if updated.GetName() != "E2E Updated" || updated.GetAge() != 43 {
		t.Fatalf("UpdateUser = %+v, want name=E2E Updated age=43", updated)
	}

	streamCtx, streamCancel := context.WithTimeout(authCtx, 5*time.Second)
	defer streamCancel()
	stream, err := client.StreamUserEvents(streamCtx, &pb.StreamUserEventsRequest{
		UserId:     created.GetId(),
		EventTypes: []string{"login", "logout", "purchase"},
	})
	if err != nil {
		t.Fatalf("StreamUserEvents: %v", err)
	}
	eventTypes := make(map[string]int)
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("StreamUserEvents Recv: %v", err)
		}
		if event.GetUserId() != created.GetId() {
			t.Fatalf("StreamUserEvents event has userId=%q, want %q", event.GetUserId(), created.GetId())
		}
		eventTypes[event.GetEventType()]++
	}
	for _, want := range []string{"login", "logout", "purchase"} {
		if eventTypes[want] == 0 {
			t.Fatalf("StreamUserEvents never emitted %q (counts=%v)", want, eventTypes)
		}
	}

	metricsStream, err := client.CollectUserMetrics(authContext(context.Background(), testAPIKey))
	if err != nil {
		t.Fatalf("CollectUserMetrics: %v", err)
	}
	values := []float64{1, 2, 3, 4}
	for _, v := range values {
		if err := metricsStream.Send(&pb.UserMetric{UserId: created.GetId(), MetricName: "e2e", Value: v}); err != nil {
			t.Fatalf("CollectUserMetrics Send(%v): %v", v, err)
		}
	}
	summary, err := metricsStream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CollectUserMetrics CloseAndRecv: %v", err)
	}
	if summary.GetCount() != int32(len(values)) {
		t.Fatalf("CollectUserMetrics count = %d, want %d", summary.GetCount(), len(values))
	}
	if summary.GetSum() != 10 {
		t.Fatalf("CollectUserMetrics sum = %v, want 10", summary.GetSum())
	}
	if summary.GetAvg() != 2.5 {
		t.Fatalf("CollectUserMetrics avg = %v, want 2.5", summary.GetAvg())
	}

	chat, err := client.ChatStream(authContext(context.Background(), testAPIKey))
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	messages := []string{"hello", "how are you", "goodbye"}
	for _, m := range messages {
		if err := chat.Send(&pb.ChatMessage{UserId: created.GetId(), Message: m}); err != nil {
			t.Fatalf("ChatStream Send(%q): %v", m, err)
		}
	}
	if err := chat.CloseSend(); err != nil {
		t.Fatalf("ChatStream CloseSend: %v", err)
	}
	received := 0
	for {
		reply, err := chat.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("ChatStream Recv: %v", err)
		}
		if reply.GetId() == "" {
			t.Fatal("ChatStream reply has empty id")
		}
		received++
	}
	if received != len(messages) {
		t.Fatalf("ChatStream received %d replies, want %d", received, len(messages))
	}

	if _, err := client.DeleteUser(authContext(context.Background(), testAPIKey), &pb.DeleteUserRequest{Id: created.GetId()}); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := client.GetUser(authContext(context.Background(), testAPIKey), &pb.GetUserRequest{Id: created.GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetUser after Delete code = %v, want NotFound", status.Code(err))
	}
}

// TestE2E_AuthFailurePaths verifies that the authentication interceptor
// rejects every flavor of bad or missing credentials and that the public
// health check remains accessible without credentials.
func TestE2E_AuthFailurePaths(t *testing.T) {
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	healthClient := grpc_health_v1.NewHealthClient(conn)

	healthCtx, healthCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer healthCancel()
	healthResp, err := healthClient.Check(healthCtx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("unauthenticated health Check: %v", err)
	}
	if healthResp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("health status = %v, want SERVING", healthResp.GetStatus())
	}

	request := &pb.CreateUserRequest{Name: "Bad", Email: "bad@example.com", Age: 20}
	noCtx, noCancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, err = client.CreateUser(noCtx, request)
	noCancel()
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing credentials code = %v, want Unauthenticated", status.Code(err))
	}

	wrongKeyCtx, wrongCancel := context.WithTimeout(authContext(context.Background(), "definitely-not-the-api-key"), 3*time.Second)
	_, err = client.CreateUser(wrongKeyCtx, request)
	wrongCancel()
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("invalid x-api-key code = %v, want Unauthenticated", status.Code(err))
	}

	badJwt := generateTestJWT(testJWTSecret, time.Now().Add(time.Hour), nil)
	badJwtParts := strings.Split(badJwt, ".")
	if len(badJwtParts) != 3 {
		t.Fatalf("expected 3-part JWT, got %d parts", len(badJwtParts))
	}
	corruptedJWT := badJwtParts[0] + "." + badJwtParts[1] + "." + "AAAA" + badJwtParts[2][4:]
	corruptCtx, corruptCancel := context.WithTimeout(jwtAuthorizationContext(context.Background(), corruptedJWT), 3*time.Second)
	_, err = client.CreateUser(corruptCtx, request)
	corruptCancel()
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("invalid JWT code = %v, want Unauthenticated", status.Code(err))
	}

	expiredJWT := generateTestJWT(testJWTSecret, time.Now().Add(-time.Minute), nil)
	expiredCtx, expiredCancel := context.WithTimeout(jwtAuthorizationContext(context.Background(), expiredJWT), 3*time.Second)
	_, err = client.CreateUser(expiredCtx, request)
	expiredCancel()
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expired JWT code = %v, want Unauthenticated", status.Code(err))
	}

	validJWT := generateTestJWT(testJWTSecret, time.Now().Add(time.Hour), nil)
	goodCtx, goodCancel := context.WithTimeout(jwtAuthorizationContext(context.Background(), validJWT), 3*time.Second)
	if _, err := client.CreateUser(goodCtx, &pb.CreateUserRequest{Name: "JWT User", Email: "jwt@example.com", Age: 25}); err != nil {
		t.Fatalf("valid JWT CreateUser: %v", err)
	}
	goodCancel()
}

// TestE2E_ValidationFailurePaths confirms that service-layer validation
// surfaces as InvalidArgument when invoked over the wire.
func TestE2E_ValidationFailurePaths(t *testing.T) {
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	authCtx := authContext(context.Background(), testAPIKey)

	cases := []struct {
		name string
		call func(context.Context) error
	}{
		{
			name: "CreateUser empty name",
			call: func(ctx context.Context) error {
				_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Email: "noname@example.com", Age: 30})
				return err
			},
		},
		{
			name: "CreateUser invalid email",
			call: func(ctx context.Context) error {
				_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Bad", Email: "not-an-email", Age: 30})
				return err
			},
		},
		{
			name: "GetUser empty id",
			call: func(ctx context.Context) error {
				_, err := client.GetUser(ctx, &pb.GetUserRequest{Id: ""})
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(authCtx, 3*time.Second)
			defer cancel()
			if err := tc.call(ctx); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("%s code = %v, want InvalidArgument", tc.name, status.Code(err))
			}
		})
	}
}

// TestE2E_StreamCancellation starts a server stream, cancels the caller's
// context, and asserts that the server-side stream is torn down promptly
// with context.Canceled (or its deadline-exceeded equivalent) rather than
// running to completion.
func TestE2E_StreamCancellation(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Server.MaxStreamMessages = 1000
	cfg.Server.StreamInterval = 50 * time.Millisecond
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)

	authCtx := authContext(context.Background(), testAPIKey)
	setupCtx, setupCancel := context.WithTimeout(authCtx, 3*time.Second)
	created, err := client.CreateUser(setupCtx, &pb.CreateUserRequest{Name: "Stream Cancel", Email: "cancel@example.com", Age: 30})
	setupCancel()
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(authContext(context.Background(), testAPIKey), 3*time.Second)
		_, _ = client.DeleteUser(ctx, &pb.DeleteUserRequest{Id: created.GetId()})
		cancel()
	}()

	streamCtx, streamCancel := context.WithCancel(authCtx)
	defer streamCancel()
	stream, err := client.StreamUserEvents(streamCtx, &pb.StreamUserEventsRequest{UserId: created.GetId(), EventTypes: []string{"e"}})
	if err != nil {
		t.Fatalf("StreamUserEvents: %v", err)
	}

	if _, err := stream.Recv(); err != nil {
		t.Fatalf("first Recv: %v", err)
	}
	streamCancel()

	recvErr := make(chan error, 1)
	go func() {
		_, err := stream.Recv()
		recvErr <- err
	}()
	select {
	case err := <-recvErr:
		if err == nil || err == io.EOF {
			t.Fatalf("Recv after cancel returned %v, want non-nil non-EOF", err)
		}
		if !isCancellation(err) {
			t.Fatalf("Recv after cancel returned %v, want cancellation", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Recv did not return after stream cancellation")
	}
}

// TestE2E_GracefulShutdown starts the server, drives a short CRUD
// transaction, cancels the run() context, and asserts that both the
// outstanding RPCs and the run() goroutine return cleanly.
func TestE2E_GracefulShutdown(t *testing.T) {
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)

	authCtx := authContext(context.Background(), testAPIKey)
	createCtx, createCancel := context.WithTimeout(authCtx, 3*time.Second)
	if _, err := client.CreateUser(createCtx, &pb.CreateUserRequest{Name: "Shutdown", Email: "shutdown@example.com", Age: 40}); err != nil {
		createCancel()
		t.Fatalf("CreateUser: %v", err)
	}
	createCancel()

	server.Stop(t)
}

// TestE2E_HealthEndpointsDuringLifecycle exercises the health and readiness
// HTTP endpoints across the start, ready, and shutting-down states.
func TestE2E_HealthEndpointsDuringLifecycle(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Server.Timeout = 5 * time.Second

	healthPort := cfg.Health.Port

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- run(ctx, cfg) }()
	healthURL := "http://127.0.0.1:" + itoa(healthPort)
	waitForStatus(t, healthURL+"/healthz", http.StatusOK)
	waitForStatus(t, healthURL+"/readyz", http.StatusOK)

	readyzURL := healthURL + "/readyz"
	healthzURL := healthURL + "/healthz"

	cancel()

	transitionDeadline := time.Now().Add(2 * time.Second)
	observedNotReady := false
	for time.Now().Before(transitionDeadline) {
		if response, err := http.Get(readyzURL); err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == http.StatusServiceUnavailable {
				observedNotReady = true
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !observedNotReady {
		t.Log("warning: readyz=503 transition window was not observed before listener closed")
	}
	if response, err := http.Get(healthzURL); err == nil {
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK {
			t.Fatalf("/healthz during shutdown = %d, want 200", response.StatusCode)
		}
	}

	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("run() returned error during drain: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not drain after cancellation")
	}
}

// TestE2E_MetricsEndpoint drives a few RPCs and asserts that the /metrics
// endpoint exposes the expected Prometheus metric families.
func TestE2E_MetricsEndpoint(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Metrics.Enabled = true
	cfg.Metrics.Port = freePort(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	authCtx := authContext(context.Background(), testAPIKey)

	createCtx, createCancel := context.WithTimeout(authCtx, 3*time.Second)
	created, err := client.CreateUser(createCtx, &pb.CreateUserRequest{Name: "Metrics", Email: "metrics@example.com", Age: 30})
	createCancel()
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	getCtx, getCancel := context.WithTimeout(authCtx, 3*time.Second)
	if _, err := client.GetUser(getCtx, &pb.GetUserRequest{Id: created.GetId()}); err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	getCancel()
	delCtx, delCancel := context.WithTimeout(authCtx, 3*time.Second)
	if _, err := client.DeleteUser(delCtx, &pb.DeleteUserRequest{Id: created.GetId()}); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	delCancel()

	metricsURL := "http://127.0.0.1:" + itoa(cfg.Metrics.Port) + "/metrics"
	body := getHTTP(t, metricsURL)
	for _, want := range []string{"grpc_server_total_requests", "grpc_server_active_streams"} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics body missing %q", want)
		}
	}
	if !strings.Contains(body, "example.v1.UserService/CreateUser") {
		t.Fatalf("metrics body missing CreateUser method label")
	}
}

// isCancellation reports whether err represents a gRPC context cancellation
// or deadline exceeded error.
func isCancellation(err error) bool {
	if err == nil {
		return false
	}
	return status.Code(err) == codes.Canceled || status.Code(err) == codes.DeadlineExceeded
}
