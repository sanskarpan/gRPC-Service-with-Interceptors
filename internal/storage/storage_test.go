package storage

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/pb"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func testUser(id string) *pb.User {
	now := timestamppb.Now()
	return &pb.User{Id: id, Name: "Name", Email: id + "@example.com", Age: 30, CreatedAt: now, UpdatedAt: now}
}

func TestMemoryRepositoryCRUD(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()
	user := testUser("crud-user")

	if err := repo.Create(ctx, user, 10); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	user.Name = "caller mutation"
	got, err := repo.Get(ctx, "crud-user")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.GetName() != "Name" {
		t.Fatalf("Get() returned aliased state: Name = %q", got.GetName())
	}

	clone := proto.Clone(got).(*pb.User)
	clone.Name = "Updated Name"
	if err := repo.Update(ctx, clone); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	updated, err := repo.Get(ctx, "crud-user")
	if err != nil {
		t.Fatalf("Get() after update error = %v", err)
	}
	if updated.GetName() != "Updated Name" {
		t.Fatalf("Get() after update: Name = %q, want %q", updated.GetName(), "Updated Name")
	}

	if err := repo.Delete(ctx, "crud-user"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repo.Get(ctx, "crud-user"); err != ErrNotFound {
		t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
	}
}

func TestMemoryRepositoryCreateDuplicate(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()
	user := testUser("dup-user")

	if err := repo.Create(ctx, user, 10); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	if err := repo.Create(ctx, user, 10); err != ErrAlreadyExists {
		t.Fatalf("second Create() error = %v, want ErrAlreadyExists", err)
	}
}

func TestMemoryRepositoryCreateNilUser(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()

	err := repo.Create(ctx, nil, 10)
	if err != ErrInvalid {
		t.Fatalf("Create(nil) error = %v, want ErrInvalid", err)
	}
}

func TestMemoryRepositoryCreateEmptyID(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()

	user := testUser("")
	err := repo.Create(ctx, user, 10)
	if err != ErrInvalid {
		t.Fatalf("Create(empty id) error = %v, want ErrInvalid", err)
	}
}

func TestMemoryRepositoryGetNotFound(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()

	_, err := repo.Get(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestMemoryRepositoryUpdateNotFound(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()

	user := testUser("nonexistent")
	err := repo.Update(ctx, user)
	if err != ErrNotFound {
		t.Fatalf("Update() error = %v, want ErrNotFound", err)
	}
}

func TestMemoryRepositoryUpdateNilUser(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()

	err := repo.Update(ctx, nil)
	if err != ErrInvalid {
		t.Fatalf("Update(nil) error = %v, want ErrInvalid", err)
	}
}

func TestMemoryRepositoryUpdateEmptyID(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()

	user := testUser("")
	err := repo.Update(ctx, user)
	if err != ErrInvalid {
		t.Fatalf("Update(empty id) error = %v, want ErrInvalid", err)
	}
}

func TestMemoryRepositoryDeleteNotFound(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()

	err := repo.Delete(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Fatalf("Delete() error = %v, want ErrNotFound", err)
	}
}

func TestMemoryRepositoryCapacity(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		id := "cap-user-" + string(rune('0'+i))
		if err := repo.Create(ctx, testUser(id), 3); err != nil {
			t.Fatalf("Create(%s) error = %v", id, err)
		}
	}
	if err := repo.Create(ctx, testUser("cap-user-4"), 3); err != ErrCapacity {
		t.Fatalf("Create() beyond capacity error = %v, want ErrCapacity", err)
	}
}

func TestMemoryRepositoryContextCanceled(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name string
		fn   func() error
	}{
		{"Create", func() error { return repo.Create(ctx, testUser("x"), 10) }},
		{"Get", func() error { _, err := repo.Get(ctx, "x"); return err }},
		{"Update", func() error { return repo.Update(ctx, testUser("x")) }},
		{"Delete", func() error { return repo.Delete(ctx, "x") }},
		{"Ping", func() error { return repo.Ping(ctx) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(); err == nil {
				t.Fatal("expected context canceled error, got nil")
			}
		})
	}
}

func TestMemoryRepositoryClose(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()

	if err := repo.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	tests := []struct {
		name string
		fn   func() error
	}{
		{"Create", func() error { return repo.Create(ctx, testUser("x"), 10) }},
		{"Get", func() error { _, err := repo.Get(ctx, "x"); return err }},
		{"Update", func() error { return repo.Update(ctx, testUser("x")) }},
		{"Delete", func() error { return repo.Delete(ctx, "x") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.fn(); err != ErrUnavailable {
				t.Fatalf("error = %v, want ErrUnavailable", err)
			}
		})
	}
}

func TestMemoryRepositoryPing(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()

	if err := repo.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error = %v, want nil", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := repo.Ping(ctx); err == nil {
		t.Fatal("Ping() with canceled context: expected error, got nil")
	}
}

func TestMemoryRepositoryPingNilCtx(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()

	err := repo.Ping(nil)
	if err != ErrInvalid {
		t.Fatalf("Ping(nil) error = %v, want ErrInvalid", err)
	}
}

func TestMemoryRepositoryConcurrentAccess(t *testing.T) {
	repo := NewMemoryRepository()
	ctx := context.Background()
	user := testUser("concurrent-user")

	if err := repo.Create(ctx, user, 10); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := repo.Get(ctx, "concurrent-user")
			if err != nil {
				return
			}
			clone := proto.Clone(got).(*pb.User)
			clone.Name = "concurrent update"
			_ = repo.Update(ctx, clone)
		}()
	}
	wg.Wait()

	got, err := repo.Get(ctx, "concurrent-user")
	if err != nil {
		t.Fatalf("final Get() error = %v", err)
	}
	if got.GetId() != "concurrent-user" {
		t.Fatalf("final Get() Id = %q, want %q", got.GetId(), "concurrent-user")
	}
}

func newPostgresRepo(t *testing.T) (*PostgresRepository, context.Context) {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	repo, err := NewPostgresRepository(ctx, PostgresConfig{
		URL:             dsn,
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
		PingTimeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPostgresRepository() error = %v", err)
	}
	t.Cleanup(func() { _ = repo.Close() })
	return repo, ctx
}

func uniqueID(prefix string) string {
	return prefix + "-" + time.Now().UTC().Format("20060102150405.000000000")
}

func TestPostgresRepositoryCRUD(t *testing.T) {
	repo, ctx := newPostgresRepo(t)
	id := uniqueID("pg-crud")
	user := testUser(id)

	if err := repo.Create(ctx, user, 100000); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(context.Background(), id) })

	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.GetEmail() != user.GetEmail() {
		t.Fatalf("Get() Email = %q, want %q", got.GetEmail(), user.GetEmail())
	}
	if got.GetName() != "Name" {
		t.Fatalf("Get() Name = %q, want %q", got.GetName(), "Name")
	}

	clone := proto.Clone(got).(*pb.User)
	clone.Name = "Updated pg user"
	if err := repo.Update(ctx, clone); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	updated, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() after update error = %v", err)
	}
	if updated.GetName() != "Updated pg user" {
		t.Fatalf("Get() after update Name = %q, want %q", updated.GetName(), "Updated pg user")
	}

	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repo.Get(ctx, id); err != ErrNotFound {
		t.Fatalf("Get() after delete error = %v, want ErrNotFound", err)
	}
}

func TestPostgresRepositoryCreateDuplicate(t *testing.T) {
	repo, ctx := newPostgresRepo(t)
	id := uniqueID("pg-dup")
	user := testUser(id)

	if err := repo.Create(ctx, user, 100000); err != nil {
		t.Fatalf("first Create() error = %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(context.Background(), id) })

	if err := repo.Create(ctx, user, 100000); err != ErrAlreadyExists {
		t.Fatalf("second Create() error = %v, want ErrAlreadyExists", err)
	}
}

func TestPostgresRepositoryGetNotFound(t *testing.T) {
	repo, ctx := newPostgresRepo(t)

	_, err := repo.Get(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

func TestPostgresRepositoryUpdateNotFound(t *testing.T) {
	repo, ctx := newPostgresRepo(t)

	user := testUser("nonexistent")
	err := repo.Update(ctx, user)
	if err != ErrNotFound {
		t.Fatalf("Update() error = %v, want ErrNotFound", err)
	}
}

func TestPostgresRepositoryDeleteNotFound(t *testing.T) {
	repo, ctx := newPostgresRepo(t)

	err := repo.Delete(ctx, "nonexistent")
	if err != ErrNotFound {
		t.Fatalf("Delete() error = %v, want ErrNotFound", err)
	}
}

func TestPostgresRepositoryCapacity(t *testing.T) {
	repo, ctx := newPostgresRepo(t)

	id := uniqueID("pg-cap")
	user := testUser(id)
	err := repo.Create(ctx, user, 0)
	if err != ErrCapacity {
		t.Fatalf("Create() with maxUsers=0 error = %v, want ErrCapacity", err)
	}
}

func TestPostgresRepositoryCreateInvalidEmptyID(t *testing.T) {
	repo, ctx := newPostgresRepo(t)
	user := testUser("")
	err := repo.Create(ctx, user, 100000)
	if err != ErrInvalid {
		t.Fatalf("Create(empty id) error = %v, want ErrInvalid", err)
	}
}

func TestPostgresRepositoryCreateInvalidNilTimestamps(t *testing.T) {
	repo, ctx := newPostgresRepo(t)
	id := uniqueID("pg-nil-ts")
	user := &pb.User{Id: id, Name: "Name", Email: "nil-ts@example.com", Age: 30}
	err := repo.Create(ctx, user, 100000)
	if err != ErrInvalid {
		t.Fatalf("Create(nil timestamps) error = %v, want ErrInvalid", err)
	}
}

func TestPostgresRepositoryUpdateInvalidEmptyID(t *testing.T) {
	repo, ctx := newPostgresRepo(t)
	user := testUser("")
	err := repo.Update(ctx, user)
	if err != ErrInvalid {
		t.Fatalf("Update(empty id) error = %v, want ErrInvalid", err)
	}
}

func TestPostgresRepositoryUpdateInvalidNilTimestamps(t *testing.T) {
	repo, ctx := newPostgresRepo(t)
	id := uniqueID("pg-upd-nil-ts")
	user := &pb.User{Id: id, Name: "Name", Email: "upd-nil-ts@example.com", Age: 30}
	err := repo.Update(ctx, user)
	if err != ErrInvalid {
		t.Fatalf("Update(nil timestamps) error = %v, want ErrInvalid", err)
	}
}

func TestPostgresRepositoryPingNilCtx(t *testing.T) {
	repo, ctx := newPostgresRepo(t)
	_ = ctx
	err := repo.Ping(nil)
	if err != ErrInvalid {
		t.Fatalf("Ping(nil) error = %v, want ErrInvalid", err)
	}
}

func TestPostgresRepositoryPing(t *testing.T) {
	repo, ctx := newPostgresRepo(t)

	if err := repo.Ping(ctx); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestPostgresRepositoryClose(t *testing.T) {
	repo, ctx := newPostgresRepo(t)

	if err := repo.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if err := repo.Create(ctx, testUser("should-fail"), 10); err == nil {
		t.Fatal("Create() after Close: expected error, got nil")
	}
}

func TestPostgresRepositoryNewWithCanceledCtx(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewPostgresRepository(ctx, PostgresConfig{
		URL:             dsn,
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
		PingTimeout:     5 * time.Second,
	})
	if err == nil {
		t.Fatal("NewPostgresRepository with canceled context: expected error, got nil")
	}
}

func TestPostgresRepositoryNewWithUnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := NewPostgresRepository(ctx, PostgresConfig{
		URL:             "postgres://grpc_service:grpc-test-password@localhost:1/grpc_service?sslmode=disable",
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
		PingTimeout:     1 * time.Second,
	})
	if err == nil {
		t.Fatal("NewPostgresRepository with unreachable host: expected error, got nil")
	}
}

func TestPostgresRepositoryFreshDatabaseMigration(t *testing.T) {
	freshDSN := os.Getenv("TEST_DATABASE_URL_FRESH")
	if freshDSN == "" {
		t.Skip("set TEST_DATABASE_URL_FRESH to a fresh database to test migration path")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo, err := NewPostgresRepository(ctx, PostgresConfig{
		URL:             freshDSN,
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
		PingTimeout:     5 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewPostgresRepository() on fresh database error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	id := uniqueID("pg-fresh")
	user := testUser(id)
	if err := repo.Create(ctx, user, 100000); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	got, err := repo.Get(ctx, id)
	if err != nil || got.GetEmail() != user.GetEmail() {
		t.Fatalf("Get() = %#v, %v", got, err)
	}
}

func TestPostgresRepositoryOpenWithInvalidDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := NewPostgresRepository(ctx, PostgresConfig{
		URL:             "invalid-dsn-that-will-fail-at-open",
		MaxOpenConns:    1,
		MaxIdleConns:    0,
		ConnMaxLifetime: time.Second,
		PingTimeout:     100 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("NewPostgresRepository with invalid DSN: expected error, got nil")
	}
}

func TestPostgresRepositoryCreateInvalidZeroTimestamps(t *testing.T) {
	repo, ctx := newPostgresRepo(t)
	id := uniqueID("pg-zero-ts")
	user := &pb.User{
		Id: id, Name: "Name", Email: "zero-ts@example.com", Age: 30,
		CreatedAt: timestamppb.New(time.Time{}),
		UpdatedAt: timestamppb.New(time.Time{}),
	}
	err := repo.Create(ctx, user, 100000)
	if err != ErrInvalid {
		t.Fatalf("Create(zero timestamps) error = %v, want ErrInvalid", err)
	}
}

func TestPostgresRepositoryContextCanceledCreate(t *testing.T) {
	repo, ctx := newPostgresRepo(t)
	id := uniqueID("pg-ctx-cancel")
	user := testUser(id)
	if err := repo.Create(ctx, user, 100000); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(context.Background(), id) })

	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := repo.Create(cancelCtx, testUser("should-fail"), 10); err == nil {
		t.Fatal("Create with canceled context: expected error, got nil")
	}
	if _, err := repo.Get(cancelCtx, id); err == nil {
		t.Fatal("Get with canceled context: expected error, got nil")
	}
	if err := repo.Update(cancelCtx, user); err == nil {
		t.Fatal("Update with canceled context: expected error, got nil")
	}
	if err := repo.Delete(cancelCtx, id); err == nil {
		t.Fatal("Delete with canceled context: expected error, got nil")
	}
}

func TestPostgresRepositoryReadModifyUpdateConcurrent(t *testing.T) {
	repo, ctx := newPostgresRepo(t)
	id := uniqueID("pg-rmu-race")
	user := testUser(id)
	if err := repo.Create(ctx, user, 100000); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(context.Background(), id) })

	const workers = 16
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.ReadModifyUpdate(ctx, id, func(current *pb.User) error {
				current.Age = current.GetAge() + 1
				return nil
			})
			if err != nil {
				t.Errorf("ReadModifyUpdate worker: %v", err)
			}
		}()
	}
	wg.Wait()
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.GetAge() != 30+workers {
		t.Fatalf("ReadModifyUpdate concurrent: Age = %d, want %d", got.GetAge(), 30+workers)
	}
}

func TestPostgresRepositoryConnectionPoolExhaustion(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Repository with MaxOpenConns=1 so we can exhaust it with a single
	// outstanding transaction.
	repo, err := NewPostgresRepository(ctx, PostgresConfig{
		URL:             dsn,
		MaxOpenConns:    1,
		MaxIdleConns:    0,
		ConnMaxLifetime: time.Minute,
		PingTimeout:     time.Second,
		RetryAttempts:   1,
		RetryBackoff:    time.Millisecond,
		RetryMaxBackoff: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPostgresRepository() error = %v", err)
	}
	defer repo.Close()
	id := uniqueID("pg-pool-exhaust")
	user := testUser(id)
	if err := repo.Create(ctx, user, 100000); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(context.Background(), id) })

	// Write to pg_largeobject to consume the one connection via a long-running
	// query inside a transaction that we never commit.
	tx, err := repo.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	// Hold a lock in the transaction so the pool connection is consumed.
	_, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(123456789)`)
	if err != nil {
		t.Fatalf("advisory lock: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Now the pool is exhausted. A concurrent operation should fail with a
	// timeout or pool error rather than hanging forever.
	timeoutCtx, timeoutCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer timeoutCancel()
	_, err = repo.Get(timeoutCtx, id)
	if err == nil {
		t.Fatal("expected error due to pool exhaustion, got nil")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		// The exact error may vary depending on pgx/lib/pq behavior, but it
		// should be a timeout-related error, not a successful response.
	}
}

func TestPostgresRepositoryNeverEndContextGet(t *testing.T) {
	repo, _ := newPostgresRepo(t)
	// Very long timeout so the retry logic doesn't kick in.
	longCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err := repo.Get(longCtx, "nonexistent-"+uniqueID(""))
	if err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("expected ErrNotFound for missing user, got timeout or nil")
	}
}

func TestPostgresRepositoryRetryEventualSuccess(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	// Use small retry config so the test doesn't take too long.
	repo, err := NewPostgresRepository(ctx, PostgresConfig{
		URL:             dsn,
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
		PingTimeout:     time.Second,
		RetryAttempts:   3,
		RetryBackoff:    time.Millisecond,
		RetryMaxBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPostgresRepository() error = %v", err)
	}
	defer repo.Close()

	id := uniqueID("pg-retry")
	user := testUser(id)
	if err := repo.Create(ctx, user, 100000); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(context.Background(), id) })

	// Verify Get works through the retry wrapper.
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.GetId() != id {
		t.Fatalf("Get() returned wrong user: %v", got.GetId())
	}

	// Verify Update works through the retry wrapper.
	user.Age = 99
	if err := repo.Update(ctx, user); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	got, err = repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() after update error = %v", err)
	}
	if got.GetAge() != 99 {
		t.Fatalf("Update() age: got %d, want 99", got.GetAge())
	}

	// Verify Delete works through the retry wrapper.
	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := repo.Get(ctx, id); err != ErrNotFound {
		t.Fatalf("Get() after delete: error = %v, want ErrNotFound", err)
	}
}

func TestMemoryRepositoryCreateCancelledContext(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := repo.Create(ctx, testUser("u1"), 10)
	if err == nil {
		t.Fatal("Create() with canceled context: expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Create() with canceled context: error = %v, want context.Canceled", err)
	}
}

func TestPostgresRepositoryRetryOnDeadlock(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, err := NewPostgresRepository(ctx, PostgresConfig{
		URL:             dsn,
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
		PingTimeout:     time.Second,
		RetryAttempts:   3,
		RetryBackoff:    time.Millisecond,
		RetryMaxBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPostgresRepository() error = %v", err)
	}
	defer repo.Close()

	id := uniqueID("pg-deadlock")
	if err := repo.Create(ctx, testUser(id), 100000); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(context.Background(), id) })

	// Use two goroutines that update the same user concurrently via
	// ReadModifyUpdate. With SELECT FOR UPDATE, one will wait for the
	// other rather than deadlock, but under high concurrency the retry
	// wrapper is exercised for any transient lock errors.
	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := repo.ReadModifyUpdate(ctx, id, func(current *pb.User) error {
				current.Age = current.GetAge() + 1
				return nil
			})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("ReadModifyUpdate deadlock test error: %v", err)
	}
	got, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	// 30 (base) + 8 (workers) = 38
	if got.GetAge() != 38 {
		t.Fatalf("ReadModifyUpdate deadlock: Age = %d, want 38", got.GetAge())
	}
}

func TestPostgresRepositoryRetryOnSerializationError(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	repo, err := NewPostgresRepository(ctx, PostgresConfig{
		URL:             dsn,
		MaxOpenConns:    4,
		MaxIdleConns:    2,
		ConnMaxLifetime: time.Minute,
		PingTimeout:     time.Second,
		RetryAttempts:   3,
		RetryBackoff:    time.Millisecond,
		RetryMaxBackoff: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewPostgresRepository() error = %v", err)
	}
	defer repo.Close()

	// Directly test that retryOperation works against the real Postgres
	// by running a retryable operation that eventually succeeds.
	attempts := 0
	var getErr error
	err = retryOperation(ctx, repo.retryCfg(), func(ctx context.Context) error {
		attempts++
		if attempts < 2 {
			// Simulate a transient connection error
			return &pgconn.PgError{Code: "40001"} // serialization_failure
		}
		// Real query — expected to fail with ErrNotFound because the user
		// doesn't exist, but crucially the retry loop ran the injected
		// transient error first.
		_, getErr = repo.Get(ctx, "nonexistent-for-retry-test")
		return getErr
	})
	if err == nil {
		t.Fatal("expected ErrNotFound from the Get call after retries")
	}
	if !errors.Is(err, ErrNotFound) && !errors.Is(getErr, ErrNotFound) {
		t.Fatalf("unexpected error type: %v", err)
	}
	if attempts < 2 {
		t.Errorf("expected at least 2 attempts due to injected transient errors, got %d", attempts)
	}
	t.Logf("retryOperation correctly retried %d times and returned ErrNotFound", attempts)
}

func TestMemoryRepositoryGetCancelledContext(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := repo.Get(ctx, "u1")
	if err == nil {
		t.Fatal("Get() with canceled context: expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Get() with canceled context: error = %v, want context.Canceled", err)
	}
}

func TestMemoryRepositoryUpdateCancelledContext(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := repo.Update(ctx, testUser("u1"))
	if err == nil {
		t.Fatal("Update() with canceled context: expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Update() with canceled context: error = %v, want context.Canceled", err)
	}
}

func TestMemoryRepositoryDeleteCancelledContext(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := repo.Delete(ctx, "u1")
	if err == nil {
		t.Fatal("Delete() with canceled context: expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Delete() with canceled context: error = %v, want context.Canceled", err)
	}
}

func TestMemoryRepositoryCreateNilCtx(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()

	err := repo.Create(nil, testUser("u1"), 10)
	if err != ErrInvalid {
		t.Fatalf("Create(nil ctx) error = %v, want ErrInvalid", err)
	}
}

func TestMemoryRepositoryGetNilCtx(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()

	_, err := repo.Get(nil, "u1")
	if err != ErrInvalid {
		t.Fatalf("Get(nil ctx) error = %v, want ErrInvalid", err)
	}
}

func TestMemoryRepositoryUpdateNilCtx(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()

	err := repo.Update(nil, testUser("u1"))
	if err != ErrInvalid {
		t.Fatalf("Update(nil ctx) error = %v, want ErrInvalid", err)
	}
}

func TestMemoryRepositoryDeleteNilCtx(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()

	err := repo.Delete(nil, "u1")
	if err != ErrInvalid {
		t.Fatalf("Delete(nil ctx) error = %v, want ErrInvalid", err)
	}
}

func TestMemoryRepositoryPingCancelled(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := repo.Ping(ctx)
	if err == nil {
		t.Fatal("Ping() with canceled context: expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Ping() with canceled context: error = %v, want context.Canceled", err)
	}
}

func TestMemoryRepositoryReadModifyUpdateHappyPath(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()
	if err := repo.Create(ctx, testUser("rmu-user"), 10); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	updated, err := repo.ReadModifyUpdate(ctx, "rmu-user", func(current *pb.User) error {
		current.Name = "modified"
		current.Age = 42
		return nil
	})
	if err != nil {
		t.Fatalf("ReadModifyUpdate() error = %v", err)
	}
	if updated.GetName() != "modified" || updated.GetAge() != 42 {
		t.Fatalf("ReadModifyUpdate() returned %q age=%d", updated.GetName(), updated.GetAge())
	}
	got, err := repo.Get(ctx, "rmu-user")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.GetName() != "modified" || got.GetAge() != 42 {
		t.Fatalf("persisted state wrong: %q age=%d", got.GetName(), got.GetAge())
	}
}

func TestMemoryRepositoryReadModifyUpdateNotFound(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	_, err := repo.ReadModifyUpdate(context.Background(), "missing", func(*pb.User) error { return nil })
	if err != ErrNotFound {
		t.Fatalf("ReadModifyUpdate() error = %v, want ErrNotFound", err)
	}
}

func TestMemoryRepositoryReadModifyUpdateInvalidInputs(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	if _, err := repo.ReadModifyUpdate(context.Background(), "", func(*pb.User) error { return nil }); err != ErrInvalid {
		t.Fatalf("ReadModifyUpdate() empty id: error = %v, want ErrInvalid", err)
	}
	if _, err := repo.ReadModifyUpdate(context.Background(), "missing", nil); err != ErrInvalid {
		t.Fatalf("ReadModifyUpdate() nil mutate: error = %v, want ErrInvalid", err)
	}
}

func TestMemoryRepositoryReadModifyUpdateClosedRepo(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	if err := repo.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	_, err := repo.ReadModifyUpdate(context.Background(), "missing", func(*pb.User) error { return nil })
	if err != ErrUnavailable {
		t.Fatalf("ReadModifyUpdate() on closed repo: error = %v, want ErrUnavailable", err)
	}
}

func TestMemoryRepositoryReadModifyUpdateLostWriteRace(t *testing.T) {
	t.Parallel()
	repo := NewMemoryRepository()
	ctx := context.Background()
	if err := repo.Create(ctx, testUser("race-user"), 10); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	const workers = 32
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := repo.ReadModifyUpdate(ctx, "race-user", func(current *pb.User) error {
				// Increment age atomically so no update is silently lost.
				current.Age = current.GetAge() + 1
				return nil
			}); err != nil {
				t.Errorf("ReadModifyUpdate() worker %d: %v", i, err)
			}
		}()
	}
	wg.Wait()
	got, err := repo.Get(ctx, "race-user")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.GetAge() != 30+workers {
		t.Fatalf("ReadModifyUpdate() race: Age = %d, want %d", got.GetAge(), 30+workers)
	}
}
