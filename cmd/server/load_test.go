//go:build loadtest

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/config"
	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	loadStreamMessages = 5
	loadGoroutines     = 20
)

func loadTestConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg := newBaseTestConfig(t)
	cfg.Server.MaxConns = 1000
	cfg.Server.MaxUsers = 100000
	cfg.Server.MaxStreamMessages = loadStreamMessages
	cfg.Server.StreamInterval = time.Millisecond
	cfg.Server.RateLimitPerSecond = 10000
	cfg.Server.RateLimitBurst = 20000
	cfg.Server.Timeout = 5 * time.Second
	return cfg
}

func startLoadTestServer(t *testing.T, configure func(*config.Config)) (*runningServer, pb.UserServiceClient) {
	t.Helper()
	cfg := loadTestConfig(t)
	if configure != nil {
		configure(cfg)
	}
	server := startTestServer(t, cfg)
	conn := dialTestClient(t, server.grpcAddr)
	t.Cleanup(func() { _ = conn.Close() })
	t.Cleanup(func() { server.Stop(t) })
	return server, pb.NewUserServiceClient(conn)
}

func loadContext() context.Context {
	return authContext(context.Background(), testAPIKey)
}

func loadCreateUser(t *testing.T, client pb.UserServiceClient, index int) *pb.User {
	t.Helper()
	user, err := client.CreateUser(loadContext(), &pb.CreateUserRequest{Name: fmt.Sprintf("load-user-%d", index), Email: fmt.Sprintf("load-%d@example.com", index), Age: 30})
	if err != nil {
		t.Fatalf("CreateUser setup: %v", err)
	}
	return user
}

func runGetRequests(client pb.UserServiceClient, userID string, count int, interval time.Duration) (int64, int64) {
	var succeeded atomic.Int64
	var failed atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(loadContext(), 2*time.Second)
			defer cancel()
			if _, err := client.GetUser(ctx, &pb.GetUserRequest{Id: userID}); err != nil {
				failed.Add(1)
				return
			}
			succeeded.Add(1)
		}()
		if interval > 0 {
			time.Sleep(interval)
		}
	}
	wg.Wait()
	return succeeded.Load(), failed.Load()
}

func TestLoad_RPSUnramped(t *testing.T) {
	_, client := startLoadTestServer(t, nil)
	user := loadCreateUser(t, client, 0)
	succeeded, failed := runGetRequests(client, user.GetId(), 500, 10*time.Millisecond)
	if failed != 0 || succeeded != 500 {
		t.Fatalf("requests succeeded=%d failed=%d, want 500/0", succeeded, failed)
	}
}

func TestLoad_RPSRampUp(t *testing.T) {
	_, client := startLoadTestServer(t, nil)
	user := loadCreateUser(t, client, 0)
	const duration = 10 * time.Second
	const tick = 20 * time.Millisecond
	start := time.Now()
	var sent atomic.Int64
	var succeeded atomic.Int64
	var failed atomic.Int64
	var wg sync.WaitGroup
	var carry float64
	for elapsed := time.Duration(0); elapsed < duration; elapsed = time.Since(start) {
		rate := 500 * float64(elapsed) / float64(duration)
		carry += rate * tick.Seconds()
		requests := int(carry)
		carry -= float64(requests)
		for i := 0; i < requests; i++ {
			sent.Add(1)
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx, cancel := context.WithTimeout(loadContext(), 2*time.Second)
				defer cancel()
				if _, err := client.GetUser(ctx, &pb.GetUserRequest{Id: user.GetId()}); err != nil {
					failed.Add(1)
				} else {
					succeeded.Add(1)
				}
			}()
		}
		time.Sleep(tick)
	}
	wg.Wait()
	total := sent.Load()
	if total == 0 || float64(succeeded.Load())/float64(total) < 0.99 {
		t.Fatalf("ramp success rate %.2f%% (%d succeeded, %d failed)", 100*float64(succeeded.Load())/float64(total), succeeded.Load(), failed.Load())
	}
}

func TestLoad_ConcurrentCRUD(t *testing.T) {
	_, client := startLoadTestServer(t, nil)
	var succeeded atomic.Int64
	var failed atomic.Int64
	var wg sync.WaitGroup
	for worker := 0; worker < 50; worker++ {
		worker := worker
		wg.Add(1)
		go func() {
			defer wg.Done()
			for cycle := 0; cycle < 5; cycle++ {
				sequence := worker*5 + cycle
				user, err := client.CreateUser(loadContext(), &pb.CreateUserRequest{Name: "concurrent", Email: fmt.Sprintf("concurrent-%d@example.com", sequence), Age: 30})
				recordLoadResult(err, &succeeded, &failed)
				if err != nil {
					failed.Add(3)
					continue
				}
				_, err = client.GetUser(loadContext(), &pb.GetUserRequest{Id: user.GetId()})
				recordLoadResult(err, &succeeded, &failed)
				_, err = client.UpdateUser(loadContext(), &pb.UpdateUserRequest{Id: user.GetId(), Age: 31})
				recordLoadResult(err, &succeeded, &failed)
				_, err = client.DeleteUser(loadContext(), &pb.DeleteUserRequest{Id: user.GetId()})
				recordLoadResult(err, &succeeded, &failed)
				time.Sleep(time.Millisecond)
			}
		}()
	}
	wg.Wait()
	if failed.Load() != 0 || succeeded.Load() != 1000 {
		t.Fatalf("CRUD operations succeeded=%d failed=%d, want 1000/0", succeeded.Load(), failed.Load())
	}
}

func recordLoadResult(err error, succeeded, failed *atomic.Int64) {
	if err != nil {
		failed.Add(1)
		return
	}
	succeeded.Add(1)
}

func TestLoad_StreamingFanOut(t *testing.T) {
	_, client := startLoadTestServer(t, nil)
	user := loadCreateUser(t, client, 0)
	runConcurrentClients(t, loadGoroutines, func(_ int) error {
		stream, err := client.StreamUserEvents(loadContext(), &pb.StreamUserEventsRequest{UserId: user.GetId(), EventTypes: []string{"load"}})
		if err != nil {
			return err
		}
		count := 0
		for {
			_, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			count++
		}
		if count != loadStreamMessages {
			return fmt.Errorf("received %d events", count)
		}
		return nil
	})
}

func TestLoad_StreamingFanIn(t *testing.T) {
	_, client := startLoadTestServer(t, nil)
	runConcurrentClients(t, loadGoroutines, func(worker int) error {
		stream, err := client.CollectUserMetrics(loadContext())
		if err != nil {
			return err
		}
		for i := 0; i < loadStreamMessages; i++ {
			if err := stream.Send(&pb.UserMetric{UserId: fmt.Sprintf("user-%d", worker), MetricName: "load", Value: float64(i)}); err != nil {
				return err
			}
			time.Sleep(time.Millisecond)
		}
		response, err := stream.CloseAndRecv()
		if err != nil {
			return err
		}
		if response.GetCount() != loadStreamMessages {
			return fmt.Errorf("metric count = %d", response.GetCount())
		}
		return nil
	})
}

func TestLoad_BidiChat(t *testing.T) {
	_, client := startLoadTestServer(t, nil)
	runConcurrentClients(t, loadGoroutines, func(worker int) error {
		stream, err := client.ChatStream(loadContext())
		if err != nil {
			return err
		}
		for i := 0; i < loadStreamMessages; i++ {
			if err := stream.Send(&pb.ChatMessage{UserId: fmt.Sprintf("user-%d", worker), Message: "load"}); err != nil {
				return err
			}
			if _, err := stream.Recv(); err != nil {
				return err
			}
			time.Sleep(time.Millisecond)
		}
		return stream.CloseSend()
	})
}

func runConcurrentClients(t *testing.T, count int, operation func(int) error) {
	t.Helper()
	errors := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := operation(i); err != nil {
				errors <- err
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Errorf("concurrent client: %v", err)
	}
}

func TestLoad_RateLimitTriggers(t *testing.T) {
	_, client := startLoadTestServer(t, func(cfg *config.Config) {
		cfg.Server.RateLimitPerSecond = 1
		cfg.Server.RateLimitBurst = 1
	})
	var exhausted int
	for i := 0; i < 10; i++ {
		_, err := client.GetUser(loadContext(), &pb.GetUserRequest{Id: "missing"})
		if status.Code(err) == codes.ResourceExhausted {
			exhausted++
		}
	}
	if exhausted == 0 {
		t.Fatal("rate limit did not return ResourceExhausted")
	}
}

func TestLoad_MemoryBaseline(t *testing.T) {
	_, client := startLoadTestServer(t, nil)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 2000; i++ {
		user, err := client.CreateUser(loadContext(), &pb.CreateUserRequest{Name: "memory", Email: fmt.Sprintf("memory-%d@example.com", i), Age: 30})
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		if _, err := client.DeleteUser(loadContext(), &pb.DeleteUserRequest{Id: user.GetId()}); err != nil {
			t.Fatalf("DeleteUser: %v", err)
		}
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	t.Logf("heap baseline=%d after=%d delta=%d", before.HeapAlloc, after.HeapAlloc, int64(after.HeapAlloc)-int64(before.HeapAlloc))
}

func TestFail_SlowClientDisconnect(t *testing.T) {
	_, client := startLoadTestServer(t, func(cfg *config.Config) { cfg.Server.StreamInterval = 10 * time.Millisecond })
	user := loadCreateUser(t, client, 0)
	ctx, cancel := context.WithCancel(loadContext())
	stream, err := client.StreamUserEvents(ctx, &pb.StreamUserEventsRequest{UserId: user.GetId()})
	if err != nil {
		t.Fatalf("StreamUserEvents: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("first Recv: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	cancel()
	for i := 0; i < loadStreamMessages+1; i++ {
		if _, err := stream.Recv(); err != nil {
			return
		}
	}
	t.Fatal("stream continued after disconnect")
}

func TestFail_ClientCancelsMidStream(t *testing.T) {
	_, client := startLoadTestServer(t, nil)
	user := loadCreateUser(t, client, 0)
	ctx, cancel := context.WithCancel(loadContext())
	stream, err := client.StreamUserEvents(ctx, &pb.StreamUserEventsRequest{UserId: user.GetId()})
	if err != nil {
		t.Fatalf("StreamUserEvents: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("first Recv: %v", err)
	}
	cancel()
	if _, err := stream.Recv(); status.Code(err) != codes.Canceled {
		t.Fatalf("Recv code = %v, want Canceled", status.Code(err))
	}
}

func TestFail_MalformedMessages(t *testing.T) {
	server, _ := startLoadTestServer(t, nil)
	conn := dialTestClient(t, server.grpcAddr)
	defer conn.Close()
	var response pb.User
	err := conn.Invoke(loadContext(), "/example.v1.UserService/CreateUser", []byte{0xff, 0xff}, &response)
	if err == nil {
		t.Fatal("malformed non-protobuf request was accepted")
	}
}

func TestFail_GracefulShutdownWithActiveClients(t *testing.T) {
	cfg := loadTestConfig(t)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, cfg) }()
	waitForStatus(t, "http://127.0.0.1:"+itoa(cfg.Health.Port)+"/readyz", http.StatusOK)
	conn := dialTestClient(t, cfg.Client.Address)
	client := pb.NewUserServiceClient(conn)
	user := loadCreateUser(t, client, 0)
	workersCtx, stopWorkers := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for workersCtx.Err() == nil {
				callCtx, callCancel := context.WithTimeout(authContext(workersCtx, testAPIKey), 100*time.Millisecond)
				_, _ = client.GetUser(callCtx, &pb.GetUserRequest{Id: user.GetId()})
				callCancel()
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run() returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("server did not shut down")
	}
	stopWorkers()
	wg.Wait()
	_ = conn.Close()
}

func TestFail_ListenerFailure(t *testing.T) {
	cfg := loadTestConfig(t)
	listener, err := net.Listen("tcp", cfg.Client.Address)
	if err != nil {
		t.Fatalf("occupy gRPC port: %v", err)
	}
	defer listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := run(ctx, cfg); err == nil || !strings.Contains(err.Error(), "listen for gRPC") {
		t.Fatalf("run() error = %v, want gRPC listener failure", err)
	}
}

func TestFail_StreamDeadline(t *testing.T) {
	_, client := startLoadTestServer(t, func(cfg *config.Config) { cfg.Server.StreamInterval = 100 * time.Millisecond })
	user := loadCreateUser(t, client, 0)
	ctx, cancel := context.WithTimeout(loadContext(), 20*time.Millisecond)
	defer cancel()
	stream, err := client.StreamUserEvents(ctx, &pb.StreamUserEventsRequest{UserId: user.GetId()})
	if err != nil {
		t.Fatalf("StreamUserEvents: %v", err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("Recv code = %v, want DeadlineExceeded", status.Code(err))
	}
}

func TestResource_MemoryBounded(t *testing.T) {
	_, client := startLoadTestServer(t, nil)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	for i := 0; i < 5000; i++ {
		user, err := client.CreateUser(loadContext(), &pb.CreateUserRequest{Name: "bounded", Email: fmt.Sprintf("bounded-%d@example.com", i), Age: 30})
		if err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		if _, err := client.DeleteUser(loadContext(), &pb.DeleteUserRequest{Id: user.GetId()}); err != nil {
			t.Fatalf("DeleteUser: %v", err)
		}
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if growth := int64(after.HeapAlloc) - int64(before.HeapAlloc); growth > 16<<20 {
		t.Fatalf("heap grew by %d bytes, limit %d", growth, 16<<20)
	}
}

func TestResource_GoroutineBounded(t *testing.T) {
	_, client := startLoadTestServer(t, nil)
	user := loadCreateUser(t, client, 0)
	before := runtime.NumGoroutine()
	runConcurrentClients(t, 100, func(_ int) error {
		ctx, cancel := context.WithCancel(loadContext())
		stream, err := client.StreamUserEvents(ctx, &pb.StreamUserEventsRequest{UserId: user.GetId()})
		if err != nil {
			cancel()
			return err
		}
		_, err = stream.Recv()
		cancel()
		return err
	})
	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before+20 && time.Now().Before(deadline) {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+20 {
		t.Fatalf("goroutines before=%d after=%d", before, after)
	}
}

func TestLive_HealthEndpointsUnderLoad(t *testing.T) {
	server, client := startLoadTestServer(t, nil)
	user := loadCreateUser(t, client, 0)
	loadDone := make(chan struct{})
	go func() {
		defer close(loadDone)
		_, _ = runGetRequests(client, user.GetId(), 200, 10*time.Millisecond)
	}()
	for i := 0; i < 20; i++ {
		for _, endpoint := range []string{"/healthz", "/readyz"} {
			response, err := http.Get(server.healthURL + endpoint)
			if err != nil {
				t.Fatalf("GET %s: %v", endpoint, err)
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				t.Fatalf("GET %s status = %d", endpoint, response.StatusCode)
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	<-loadDone
}

func TestLive_MetricsEndpointUnderLoad(t *testing.T) {
	metricsPort := freePort(t)
	_, client := startLoadTestServer(t, func(cfg *config.Config) {
		cfg.Metrics.Enabled = true
		cfg.Metrics.Port = metricsPort
	})
	user := loadCreateUser(t, client, 0)
	succeeded, failed := runGetRequests(client, user.GetId(), 100, time.Millisecond)
	if failed != 0 || succeeded != 100 {
		t.Fatalf("load succeeded=%d failed=%d", succeeded, failed)
	}
	response, err := http.Get("http://127.0.0.1:" + itoa(metricsPort) + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read /metrics: %v", err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "grpc_server_total_requests") {
		t.Fatalf("metrics status=%d missing expected counter", response.StatusCode)
	}
}
