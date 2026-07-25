package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/config"
	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const benchmarkStreamMessages = 100

type benchmarkServer struct {
	cancel context.CancelFunc
	errCh  <-chan error
	conn   *grpc.ClientConn
	client pb.UserServiceClient
}

func startBenchmarkServer(b *testing.B, configure func(*config.Config)) *benchmarkServer {
	b.Helper()
	grpcPort := benchmarkFreePort(b)
	healthPort := benchmarkFreePort(b)
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:               "127.0.0.1",
			Port:               grpcPort,
			MaxConns:           10000,
			Timeout:            30 * time.Second,
			MaxRecvMessageMB:   4,
			MaxSendMessageMB:   4,
			MaxUsers:           10000000,
			MaxStreamMessages:  benchmarkStreamMessages,
			MaxStringBytes:     256,
			StreamInterval:     time.Nanosecond,
			RateLimitPerSecond: 10000000,
			RateLimitBurst:     10000000,
		},
		Auth:    config.AuthConfig{APIKeys: []string{testAPIKey}},
		Logging: config.LoggingConfig{Level: "error", Format: "json"},
		Health:  config.HealthConfig{Port: healthPort},
		Storage: config.StorageConfig{
			Backend:         "memory",
			MaxOpenConns:    1,
			MaxIdleConns:    1,
			ConnMaxLifetime: time.Minute,
			PingTimeout:     time.Second,
			RetryAttempts:   1,
			RetryBackoff:    time.Millisecond,
			RetryMaxBackoff: 5 * time.Second,
		},
		Client: config.ClientConfig{Address: "127.0.0.1:" + itoa(grpcPort)},
	}
	if configure != nil {
		configure(cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, cfg) }()
	benchmarkWaitForStatus(b, "http://127.0.0.1:"+itoa(healthPort)+"/readyz", http.StatusOK)
	conn, err := grpc.NewClient(cfg.Client.Address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		cancel()
		b.Fatalf("create gRPC client: %v", err)
	}
	server := &benchmarkServer{cancel: cancel, errCh: errCh, conn: conn, client: pb.NewUserServiceClient(conn)}
	b.Cleanup(func() {
		_ = conn.Close()
		cancel()
		select {
		case err := <-errCh:
			if err != nil {
				b.Errorf("run() returned error: %v", err)
			}
		case <-time.After(10 * time.Second):
			b.Error("run() did not stop")
		}
	})
	return server
}

func benchmarkFreePort(b *testing.B) int {
	b.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		b.Fatalf("reserve port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		b.Fatalf("release port: %v", err)
	}
	return port
}

func benchmarkWaitForStatus(b *testing.B, url string, expected int) {
	b.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(url)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == expected {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	b.Fatalf("did not observe HTTP %d at %s", expected, url)
}

func benchmarkContext() context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "x-api-key", testAPIKey)
}

func benchmarkCreateUser(b *testing.B, client pb.UserServiceClient, index int) *pb.User {
	b.Helper()
	user, err := client.CreateUser(benchmarkContext(), &pb.CreateUserRequest{
		Name:  fmt.Sprintf("benchmark-user-%d", index),
		Email: fmt.Sprintf("benchmark-%d@example.com", index),
		Age:   30,
	})
	if err != nil {
		b.Fatalf("CreateUser setup: %v", err)
	}
	return user
}

func BenchmarkCreateUser(b *testing.B) {
	server := startBenchmarkServer(b, func(cfg *config.Config) { cfg.Server.MaxUsers = b.N + 1 })
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := server.client.CreateUser(benchmarkContext(), &pb.CreateUserRequest{Name: "benchmark", Email: fmt.Sprintf("create-%d@example.com", i), Age: 30}); err != nil {
			b.Fatalf("CreateUser: %v", err)
		}
	}
}

func BenchmarkGetUser(b *testing.B) {
	server := startBenchmarkServer(b, nil)
	user := benchmarkCreateUser(b, server.client, 0)
	request := &pb.GetUserRequest{Id: user.GetId()}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pbRun *testing.PB) {
		for pbRun.Next() {
			if _, err := server.client.GetUser(benchmarkContext(), request); err != nil {
				b.Errorf("GetUser: %v", err)
			}
		}
	})
}

func BenchmarkUpdateUser(b *testing.B) {
	server := startBenchmarkServer(b, nil)
	user := benchmarkCreateUser(b, server.client, 0)
	request := &pb.UpdateUserRequest{Id: user.GetId(), Name: "updated benchmark", Age: 31}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := server.client.UpdateUser(benchmarkContext(), request); err != nil {
			b.Fatalf("UpdateUser: %v", err)
		}
	}
}

func BenchmarkDeleteUser(b *testing.B) {
	server := startBenchmarkServer(b, func(cfg *config.Config) { cfg.Server.MaxUsers = b.N + 1 })
	users := make([]*pb.User, b.N)
	for i := range users {
		users[i] = benchmarkCreateUser(b, server.client, i)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for _, user := range users {
		if _, err := server.client.DeleteUser(benchmarkContext(), &pb.DeleteUserRequest{Id: user.GetId()}); err != nil {
			b.Fatalf("DeleteUser: %v", err)
		}
	}
}

func BenchmarkFullCRUDCycle(b *testing.B) {
	server := startBenchmarkServer(b, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		user, err := server.client.CreateUser(benchmarkContext(), &pb.CreateUserRequest{Name: "crud", Email: fmt.Sprintf("crud-%d@example.com", i), Age: 30})
		if err != nil {
			b.Fatalf("CreateUser: %v", err)
		}
		if _, err = server.client.GetUser(benchmarkContext(), &pb.GetUserRequest{Id: user.GetId()}); err != nil {
			b.Fatalf("GetUser: %v", err)
		}
		if _, err = server.client.UpdateUser(benchmarkContext(), &pb.UpdateUserRequest{Id: user.GetId(), Age: 31}); err != nil {
			b.Fatalf("UpdateUser: %v", err)
		}
		if _, err = server.client.DeleteUser(benchmarkContext(), &pb.DeleteUserRequest{Id: user.GetId()}); err != nil {
			b.Fatalf("DeleteUser: %v", err)
		}
	}
}

func BenchmarkStreamUserEvents(b *testing.B) {
	server := startBenchmarkServer(b, nil)
	user := benchmarkCreateUser(b, server.client, 0)
	request := &pb.StreamUserEventsRequest{UserId: user.GetId(), EventTypes: []string{"benchmark"}}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stream, err := server.client.StreamUserEvents(benchmarkContext(), request)
		if err != nil {
			b.Fatalf("StreamUserEvents: %v", err)
		}
		count := 0
		for {
			_, err = stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				b.Fatalf("StreamUserEvents Recv: %v", err)
			}
			count++
		}
		if count != benchmarkStreamMessages {
			b.Fatalf("received %d events, want %d", count, benchmarkStreamMessages)
		}
	}
}

func BenchmarkCollectUserMetrics(b *testing.B) {
	server := startBenchmarkServer(b, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stream, err := server.client.CollectUserMetrics(benchmarkContext())
		if err != nil {
			b.Fatalf("CollectUserMetrics: %v", err)
		}
		for message := 0; message < benchmarkStreamMessages; message++ {
			if err := stream.Send(&pb.UserMetric{UserId: "benchmark", MetricName: "latency", Value: float64(message)}); err != nil {
				b.Fatalf("CollectUserMetrics Send: %v", err)
			}
		}
		response, err := stream.CloseAndRecv()
		if err != nil {
			b.Fatalf("CollectUserMetrics CloseAndRecv: %v", err)
		}
		if response.GetCount() != benchmarkStreamMessages {
			b.Fatalf("metric count = %d, want %d", response.GetCount(), benchmarkStreamMessages)
		}
	}
}

func BenchmarkChatStream(b *testing.B) {
	server := startBenchmarkServer(b, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		stream, err := server.client.ChatStream(benchmarkContext())
		if err != nil {
			b.Fatalf("ChatStream: %v", err)
		}
		for message := 0; message < benchmarkStreamMessages; message++ {
			if err := stream.Send(&pb.ChatMessage{UserId: "benchmark", Message: "round-trip"}); err != nil {
				b.Fatalf("ChatStream Send: %v", err)
			}
			if _, err := stream.Recv(); err != nil {
				b.Fatalf("ChatStream Recv: %v", err)
			}
		}
		if err := stream.CloseSend(); err != nil {
			b.Fatalf("ChatStream CloseSend: %v", err)
		}
		if _, err := stream.Recv(); err != io.EOF {
			b.Fatalf("ChatStream final Recv = %v, want EOF", err)
		}
	}
}

func BenchmarkConcurrentGetUsers(b *testing.B) {
	server := startBenchmarkServer(b, nil)
	users := make([]*pb.User, 128)
	for i := range users {
		users[i] = benchmarkCreateUser(b, server.client, i)
	}
	var index atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pbRun *testing.PB) {
		for pbRun.Next() {
			user := users[index.Add(1)%uint64(len(users))]
			if _, err := server.client.GetUser(benchmarkContext(), &pb.GetUserRequest{Id: user.GetId()}); err != nil {
				b.Errorf("GetUser: %v", err)
			}
		}
	})
}

func BenchmarkMixedConcurrentLoad(b *testing.B) {
	server := startBenchmarkServer(b, nil)
	users := make([]*pb.User, 128)
	for i := range users {
		users[i] = benchmarkCreateUser(b, server.client, i)
	}
	var operation atomic.Uint64
	var sequence atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pbRun *testing.PB) {
		for pbRun.Next() {
			index := operation.Add(1)
			user := users[index%uint64(len(users))]
			var err error
			switch index % 4 {
			case 0:
				id := sequence.Add(1)
				_, err = server.client.CreateUser(benchmarkContext(), &pb.CreateUserRequest{Name: "mixed", Email: fmt.Sprintf("mixed-%d@example.com", id), Age: 30})
			case 1:
				_, err = server.client.GetUser(benchmarkContext(), &pb.GetUserRequest{Id: user.GetId()})
			case 2:
				_, err = server.client.UpdateUser(benchmarkContext(), &pb.UpdateUserRequest{Id: user.GetId(), Age: 31})
			case 3:
				id := sequence.Add(1)
				temporary, createErr := server.client.CreateUser(benchmarkContext(), &pb.CreateUserRequest{Name: "delete", Email: fmt.Sprintf("delete-%d@example.com", id), Age: 30})
				if createErr != nil {
					err = createErr
				} else {
					_, err = server.client.DeleteUser(benchmarkContext(), &pb.DeleteUserRequest{Id: temporary.GetId()})
				}
			}
			if err != nil {
				b.Errorf("mixed operation: %v", err)
			}
		}
	})
}
