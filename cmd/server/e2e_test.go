package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// TestNetworkE2E exercises the actual listener, TLS handshake, auth
// interceptors, every UserService RPC shape, health, and metrics. It is
// opt-in because it requires a running deployment; scripts/e2e.sh provisions
// the PostgreSQL-backed Compose deployment and sets the required variables.
func TestNetworkE2E(t *testing.T) {
	if os.Getenv("E2E") != "1" {
		t.Skip("set E2E=1 to run against a live deployment")
	}
	address := envOr("E2E_ADDRESS", "127.0.0.1:50051")
	serverName := envOr("E2E_SERVER_NAME", "localhost")
	apiKey := os.Getenv("E2E_API_KEY")
	if apiKey == "" {
		t.Fatal("E2E_API_KEY is required")
	}
	caFile := os.Getenv("E2E_CA_FILE")
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatalf("read E2E_CA_FILE: %v", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		t.Fatal("E2E_CA_FILE contains no certificate")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: serverName,
	})))
	if err != nil {
		t.Fatalf("dial TLS endpoint: %v", err)
	}
	defer func() { _ = conn.Close() }()

	health, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil || health.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("health Check = %#v, %v", health, err)
	}

	client := pb.NewUserServiceClient(conn)
	unauthenticated, unauthCancel := context.WithTimeout(ctx, 2*time.Second)
	_, err = client.CreateUser(unauthenticated, &pb.CreateUserRequest{Name: "unauth", Email: "unauth@example.com", Age: 20})
	unauthCancel()
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated CreateUser code = %v, want Unauthenticated", status.Code(err))
	}

	callCtx := metadata.AppendToOutgoingContext(ctx, "x-api-key", apiKey)
	name := "network-e2e-" + time.Now().UTC().Format("20060102150405.000000000")
	created, err := client.CreateUser(callCtx, &pb.CreateUserRequest{Name: name, Email: name + "@example.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.GetId() == "" || created.GetName() != name || created.GetAge() != 30 || created.GetCreatedAt() == nil {
		t.Fatalf("CreateUser returned incomplete data: %#v", created)
	}
	got, err := client.GetUser(callCtx, &pb.GetUserRequest{Id: created.GetId()})
	if err != nil || got.GetEmail() != created.GetEmail() {
		t.Fatalf("GetUser = %#v, %v", got, err)
	}
	updated, err := client.UpdateUser(callCtx, &pb.UpdateUserRequest{Id: created.GetId(), Name: name + " updated", Age: 31})
	if err != nil || updated.GetName() != name+" updated" || updated.GetAge() != 31 {
		t.Fatalf("UpdateUser = %#v, %v", updated, err)
	}

	events, err := client.StreamUserEvents(callCtx, &pb.StreamUserEventsRequest{UserId: created.GetId(), EventTypes: []string{"e2e"}})
	if err != nil {
		t.Fatalf("StreamUserEvents: %v", err)
	}
	eventCount := 0
	for {
		event, recvErr := events.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatalf("StreamUserEvents Recv: %v", recvErr)
		}
		if event.GetUserId() != created.GetId() || event.GetEventType() != "e2e" {
			t.Fatalf("unexpected event: %#v", event)
		}
		eventCount++
	}
	if eventCount == 0 {
		t.Fatal("StreamUserEvents returned no events")
	}

	metricsStream, err := client.CollectUserMetrics(callCtx)
	if err != nil {
		t.Fatalf("CollectUserMetrics: %v", err)
	}
	for _, value := range []float64{2, 4} {
		if err := metricsStream.Send(&pb.UserMetric{UserId: created.GetId(), MetricName: "e2e", Value: value}); err != nil {
			t.Fatalf("CollectUserMetrics Send: %v", err)
		}
	}
	metricsResponse, err := metricsStream.CloseAndRecv()
	if err != nil || metricsResponse.GetCount() != 2 || metricsResponse.GetSum() != 6 || metricsResponse.GetAvg() != 3 {
		t.Fatalf("CollectUserMetrics response = %#v, %v", metricsResponse, err)
	}

	chat, err := client.ChatStream(callCtx)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	for _, message := range []string{"one", "two"} {
		if err := chat.Send(&pb.ChatMessage{UserId: created.GetId(), Message: message}); err != nil {
			t.Fatalf("ChatStream Send: %v", err)
		}
	}
	for i := 0; i < 2; i++ {
		reply, recvErr := chat.Recv()
		if recvErr != nil || reply.GetMessage() == "" || reply.GetId() == "" {
			t.Fatalf("ChatStream Recv = %#v, %v", reply, recvErr)
		}
	}
	if err := chat.CloseSend(); err != nil {
		t.Fatalf("ChatStream CloseSend: %v", err)
	}
	if _, err := chat.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("ChatStream final Recv error = %v, want EOF", err)
	}

	if _, err := client.DeleteUser(callCtx, &pb.DeleteUserRequest{Id: created.GetId()}); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := client.GetUser(callCtx, &pb.GetUserRequest{Id: created.GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetUser after Delete code = %v, want NotFound", status.Code(err))
	}

	checkHTTP(t, envOr("E2E_HEALTH_URL", "http://127.0.0.1:8080/readyz"), "ready\n")
	checkHTTP(t, envOr("E2E_LIVENESS_URL", "http://127.0.0.1:8080/healthz"), "ok\n")
	metricsBody := getHTTP(t, envOr("E2E_METRICS_URL", "http://127.0.0.1:9090/metrics"))
	if !strings.Contains(metricsBody, "grpc_server_total_requests") {
		t.Fatalf("metrics endpoint did not expose gRPC metric family")
	}
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func checkHTTP(t *testing.T, url, expected string) {
	t.Helper()
	if body := getHTTP(t, url); body != expected {
		t.Fatalf("GET %s = %q, want %q", url, body, expected)
	}
}

func getHTTP(t *testing.T, url string) string {
	t.Helper()
	requestCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("create request %s: %v", url, err)
	}
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s: %v", url, err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, body = %q", url, response.StatusCode, body)
	}
	return string(body)
}
