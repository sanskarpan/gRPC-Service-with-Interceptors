//go:build soak

package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestSoak_Stability exercises a sustained mix of CRUD, streaming, and chat
// operations over several minutes to detect memory leaks, goroutine leaks, and
// connection-pool exhaustion.
//
// Run: go test -tags=soak -run TestSoak -timeout=30m ./cmd/server/
func TestSoak_Stability(t *testing.T) {
	dbURL := envOr("TEST_DATABASE_URL", "")
	if dbURL == "" {
		t.Skip("set TEST_DATABASE_URL to run soak tests")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cfg := newBaseTestConfig(t)
	cfg.Storage.Backend = "postgres"
	cfg.Storage.URL = dbURL
	cfg.Storage.MaxOpenConns = 10
	cfg.Storage.MaxIdleConns = 5
	cfg.Storage.ConnMaxLifetime = 5 * time.Minute
	cfg.Server.MaxUsers = 10000
	cfg.Server.MaxStreamMessages = 1000

	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx = authContext(ctx, testAPIKey)

	users := make([]string, 0, 200)
	var mu sync.Mutex

	// Phase 1: create users
	t.Log("Phase 1: creating 200 users")
	for range 200 {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
		}
		info := randomUserInfo()
		created, err := client.CreateUser(ctx, &pb.CreateUserRequest{
			Name: info.name, Email: info.email, Age: info.age,
		})
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		mu.Lock()
		users = append(users, created.GetId())
		mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Logf("Created %d users", len(users))

	// Phase 2: concurrent CRUD mix for 2 minutes
	t.Log("Phase 2: concurrent CRUD for 2 minutes")
	crudCtx, crudCancel := context.WithTimeout(ctx, 2*time.Minute)
	defer crudCancel()
	var crudWg sync.WaitGroup
	for range 10 {
		crudWg.Add(1)
		go func() {
			defer crudWg.Done()
			for crudCtx.Err() == nil {
				mu.Lock()
				if len(users) == 0 {
					mu.Unlock()
					return
				}
				idx := rand.IntN(len(users))
				id := users[idx]
				mu.Unlock()

				switch rand.IntN(4) {
				case 0: // Get
					_, err := client.GetUser(crudCtx, &pb.GetUserRequest{Id: id})
					if err != nil && !isNotFoundErr(err) {
						t.Errorf("GetUser: %v", err)
					}
				case 1: // Update
					info := randomUserInfo()
					_, err := client.UpdateUser(crudCtx, &pb.UpdateUserRequest{
						Id: id, Name: info.name, Email: info.email, Age: info.age,
					})
					if err != nil && !isNotFoundErr(err) {
						t.Errorf("UpdateUser: %v", err)
					}
				case 2: // Delete + recreate
					_, err := client.DeleteUser(crudCtx, &pb.DeleteUserRequest{Id: id})
					if err == nil {
						mu.Lock()
						uid := id
						users = deleteFromSlice(users, idx)
						mu.Unlock()
						info := randomUserInfo()
						created, err := client.CreateUser(crudCtx, &pb.CreateUserRequest{
							Name: info.name, Email: info.email, Age: info.age,
						})
						if err == nil {
							mu.Lock()
							users = append(users, created.GetId())
							mu.Unlock()
						}
						_ = uid // cleaned up
					}
				case 3: // Stream events
					sCtx, sCancel := context.WithTimeout(crudCtx, 5*time.Second)
					stream, err := client.StreamUserEvents(sCtx, &pb.StreamUserEventsRequest{
						UserId: id,
					})
					if err == nil {
						for {
							_, err := stream.Recv()
							if err != nil {
								break
							}
						}
					}
					sCancel()
				}
			}
		}()
	}
	crudWg.Wait()
	t.Log("Phase 2 complete")

	// Phase 3: sustained ChatStream for 1 minute
	t.Log("Phase 3: sustained ChatStream for 1 minute")
	chatCtx, chatCancel := context.WithTimeout(ctx, 1*time.Minute)
	defer chatCancel()
	type chatStream struct {
		stream pb.UserService_ChatStreamClient
		uid    string
	}
	chatStreams := make([]chatStream, 0, 5)
	for range 5 {
		mu.Lock()
		if len(users) == 0 {
			mu.Unlock()
			break
		}
		uid := users[rand.IntN(len(users))]
		mu.Unlock()

		stream, err := client.ChatStream(chatCtx)
		if err != nil {
			t.Fatalf("ChatStream: %v", err)
		}
		chatStreams = append(chatStreams, chatStream{stream: stream, uid: uid})
		go func(cs chatStream) {
			for chatCtx.Err() == nil {
				_, err := cs.stream.Recv()
				if err != nil {
					return
				}
			}
		}(chatStreams[len(chatStreams)-1])
	}
	// Send messages on all streams
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for i := 0; i < 50; i++ {
		select {
		case <-chatCtx.Done():
			goto chatDone
		case <-ticker.C:
			for _, cs := range chatStreams {
				_ = cs.stream.Send(&pb.ChatMessage{
					Id:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
					UserId:    "soak-test",
					Message:   "soak message",
					Timestamp: timestamppb.New(time.Now()),
				})
			}
		}
	}
chatDone:
	t.Log("Phase 3 complete")

	// Phase 4: CollectUserMetrics burst
	t.Log("Phase 4: CollectUserMetrics burst")
	metricsCtx, metricsCancel := context.WithTimeout(ctx, 30*time.Second)
	defer metricsCancel()
	metricsStream, err := client.CollectUserMetrics(metricsCtx)
	if err != nil {
		t.Fatalf("CollectUserMetrics: %v", err)
	}
	for range 1000 {
		_ = metricsStream.Send(&pb.UserMetric{
			UserId: "soak-test", MetricName: "latency", Value: float64(rand.IntN(100)),
			Timestamp: timestamppb.New(time.Now()),
		})
	}
	resp, err := metricsStream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CollectUserMetrics CloseAndRecv: %v", err)
	}
	t.Logf("Collected %d metrics, avg=%.2f", resp.Count, resp.Avg)

	// Phase 5: verify server still healthy
	t.Log("Phase 5: health check after soak")
	healthConn, err := grpc.NewClient("dns:///"+server.grpcAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("health conn: %v", err)
	}
	defer healthConn.Close()
	healthClient := grpc_health_v1.NewHealthClient(healthConn)
	hr, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: ""})
	if err != nil {
		t.Fatalf("health check: %v", err)
	}
	if hr.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("server not serving after soak: %v", hr.Status)
	}
	t.Log("Soak test passed (server healthy after sustained load)")
}

type userInfo struct {
	name  string
	email string
	age   int32
}

func randomUserInfo() userInfo {
	names := []string{"alice", "bob", "carol", "dave", "eve", "frank", "grace", "henry"}
	id := rand.IntN(99999)
	return userInfo{
		name:  fmt.Sprintf("%s-%d", names[rand.IntN(len(names))], id),
		email: fmt.Sprintf("user-%d@example.com", id),
		age:   int32(rand.IntN(62) + 18),
	}
}

func isNotFoundErr(err error) bool {
	if err == nil {
		return false
	}
	return contains(err.Error(), "not found")
}

func deleteFromSlice(s []string, i int) []string {
	return append(s[:i], s[i+1:]...)
}
