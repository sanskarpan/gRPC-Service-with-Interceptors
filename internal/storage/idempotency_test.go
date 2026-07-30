package storage

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func mkUser(id string) *pb.User {
	now := timestamppb.Now()
	return &pb.User{Id: id, Name: "n", Email: id + "@x.com", Age: 20, CreatedAt: now, UpdatedAt: now}
}

func TestMemoryIdempotentCreate(t *testing.T) {
	t.Parallel()
	r := NewMemoryRepository()
	ctx := context.Background()

	u1, replayed, err := r.CreateWithIdempotency(ctx, mkUser("user-1"), 100, "key-abc")
	if err != nil || replayed {
		t.Fatalf("first create: replayed=%v err=%v", replayed, err)
	}
	// Replay with the SAME key but a DIFFERENT user must return the first user.
	u2, replayed2, err := r.CreateWithIdempotency(ctx, mkUser("user-2"), 100, "key-abc")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed2 {
		t.Fatal("expected replayed=true on same key")
	}
	if u2.GetId() != u1.GetId() {
		t.Fatalf("replay returned %s, want original %s", u2.GetId(), u1.GetId())
	}
	// Only one user should exist.
	all, _, _ := r.List(ctx, 100, "")
	if len(all) != 1 {
		t.Fatalf("expected exactly 1 user after idempotent replay, got %d", len(all))
	}
	// A different key creates a new user.
	if _, replayed3, err := r.CreateWithIdempotency(ctx, mkUser("user-3"), 100, "key-xyz"); err != nil || replayed3 {
		t.Fatalf("new key: replayed=%v err=%v", replayed3, err)
	}
}

func TestMemoryIdempotentCreateConcurrent(t *testing.T) {
	t.Parallel()
	r := NewMemoryRepository()
	ctx := context.Background()
	const key = "race-key"
	var wg sync.WaitGroup
	ids := make([]string, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u, _, err := r.CreateWithIdempotency(ctx, mkUser(fmt.Sprintf("u-%d", i)), 1000, key)
			if err == nil {
				ids[i] = u.GetId()
			}
		}(i)
	}
	wg.Wait()
	all, _, _ := r.List(ctx, 1000, "")
	if len(all) != 1 {
		t.Fatalf("concurrent same-key creates must yield exactly 1 user, got %d", len(all))
	}
}

func TestPostgresIdempotentCreate(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("no TEST_DATABASE_URL")
	}
	r, err := NewPostgresRepository(context.Background(), PostgresConfig{URL: url, MaxOpenConns: 4, MaxIdleConns: 2, PingTimeout: 2 * time.Second, RetryAttempts: 1})
	if err != nil {
		t.Skipf("db: %v", err)
	}
	defer r.Close()
	ctx := context.Background()
	_, _ = r.db.ExecContext(ctx, "TRUNCATE users CASCADE")

	u1, replayed, err := r.CreateWithIdempotency(ctx, mkUser("pg-1"), 100, "pgkey-1")
	if err != nil || replayed {
		t.Fatalf("first: replayed=%v err=%v", replayed, err)
	}
	u2, replayed2, err := r.CreateWithIdempotency(ctx, mkUser("pg-2"), 100, "pgkey-1")
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replayed2 || u2.GetId() != u1.GetId() {
		t.Fatalf("replay returned id=%s replayed=%v, want %s/true", u2.GetId(), replayed2, u1.GetId())
	}
	var n int
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected 1 user after idempotent replay, got %d", n)
	}
}
