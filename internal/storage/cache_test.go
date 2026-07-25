package storage

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/pb"
)

type mockRepo struct {
	Repository
	getFn            func(ctx context.Context, id string) (*pb.User, error)
	createFn         func(ctx context.Context, user *pb.User, maxUsers int) error
	updateFn         func(ctx context.Context, user *pb.User) error
	readModifyUpdateFn func(ctx context.Context, id string, fn ReadModifyUpdateFn) (*pb.User, error)
	deleteFn         func(ctx context.Context, id string) error
	pingFn           func(ctx context.Context) error
	mu               sync.Mutex
	getCallCount     int
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		getFn:    func(_ context.Context, id string) (*pb.User, error) { return nil, ErrNotFound },
		createFn: func(_ context.Context, _ *pb.User, _ int) error { return nil },
		updateFn: func(_ context.Context, _ *pb.User) error { return nil },
		readModifyUpdateFn: func(_ context.Context, id string, fn ReadModifyUpdateFn) (*pb.User, error) {
			u := &pb.User{Id: id, Name: "test"}
			if err := fn(u); err != nil {
				return nil, err
			}
			return u, nil
		},
		deleteFn: func(_ context.Context, _ string) error { return nil },
		pingFn:   func(_ context.Context) error { return nil },
	}
}

func (m *mockRepo) Close() error { return nil }

func (m *mockRepo) Get(ctx context.Context, id string) (*pb.User, error) {
	m.mu.Lock()
	m.getCallCount++
	m.mu.Unlock()
	return m.getFn(ctx, id)
}

func (m *mockRepo) Create(ctx context.Context, user *pb.User, maxUsers int) error {
	return m.createFn(ctx, user, maxUsers)
}

func (m *mockRepo) Update(ctx context.Context, user *pb.User) error {
	return m.updateFn(ctx, user)
}

func (m *mockRepo) ReadModifyUpdate(ctx context.Context, id string, fn ReadModifyUpdateFn) (*pb.User, error) {
	return m.readModifyUpdateFn(ctx, id, fn)
}

func (m *mockRepo) Delete(ctx context.Context, id string) error {
	return m.deleteFn(ctx, id)
}

func (m *mockRepo) Ping(ctx context.Context) error {
	return m.pingFn(ctx)
}

func TestNewCacheRepositoryDefaultTTL(t *testing.T) {
	t.Parallel()
	r := NewCacheRepository(newMockRepo(), 0)
	if r.ttl != 30*time.Second {
		t.Fatalf("expected default TTL 30s, got %v", r.ttl)
	}
}

func TestCacheGetPopulatesAndServes(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		return &pb.User{Id: id, Name: "cached"}, nil
	}
	cache := NewCacheRepository(mock, time.Minute)
	ctx := context.Background()

	u, err := cache.Get(ctx, "1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if u.GetName() != "cached" {
		t.Fatalf("expected 'cached', got %q", u.GetName())
	}

	// Mutate original to ensure we got a clone
	u.Name = "mutated"

	// Second call should hit cache, not the mock
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		t.Fatal("unexpected call to underlying repo")
		return nil, nil
	}

	u2, err := cache.Get(ctx, "1")
	if err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if u2.GetName() != "cached" {
		t.Fatalf("expected 'cached' from cache, got %q", u2.GetName())
	}
}

func TestCacheGetServesStaleOnUnavailable(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	callCount := 0
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		callCount++
		if callCount == 1 {
			return &pb.User{Id: id, Name: "stale"}, nil
		}
		return nil, ErrUnavailable
	}
	cache := NewCacheRepository(mock, time.Minute)
	ctx := context.Background()

	// First call populates cache
	if _, err := cache.Get(ctx, "1"); err != nil {
		t.Fatalf("first Get: %v", err)
	}

	// Second call fails, serves stale
	u, err := cache.Get(ctx, "1")
	if err != nil {
		t.Fatalf("stale Get: %v", err)
	}
	if u.GetName() != "stale" {
		t.Fatalf("expected 'stale', got %q", u.GetName())
	}
}

func TestCacheGetServesStaleOnContextCancelled(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		return nil, context.Canceled
	}
	cache := NewCacheRepository(mock, time.Minute)
	// Manually inject a cached entry
	cache.set("1", &pb.User{Id: "1", Name: "stale-cancel"})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	u, err := cache.Get(ctx, "1")
	if err != nil {
		t.Fatalf("stale on cancel: %v", err)
	}
	if u.GetName() != "stale-cancel" {
		t.Fatalf("expected 'stale-cancel', got %q", u.GetName())
	}
}

func TestCacheGetReturnsUnderlyingErrorWhenNoStale(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		return nil, ErrUnavailable
	}
	cache := NewCacheRepository(mock, time.Minute)
	ctx := context.Background()

	_, err := cache.Get(ctx, "1")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
}

func TestCacheGetClonesEntry(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		return &pb.User{Id: id, Name: "original"}, nil
	}
	cache := NewCacheRepository(mock, time.Minute)
	ctx := context.Background()

	u1, _ := cache.Get(ctx, "1")
	u2, _ := cache.Get(ctx, "1")

	u1.Name = "modified"
	if u2.GetName() != "original" {
		t.Fatalf("expected 'original' (clone), got %q", u2.GetName())
	}
}

func TestCacheUpdateInvalidates(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		return nil, ErrNotFound
	}
	cache := NewCacheRepository(mock, time.Minute)

	// Manually cache
	cache.set("1", &pb.User{Id: "1", Name: "cached"})

	// Update invalidates
	if err := cache.Update(context.Background(), &pb.User{Id: "1", Name: "updated"}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Should miss cache and call the underlying repo
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		return nil, ErrNotFound
	}
	_, err := cache.Get(context.Background(), "1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound after invalidation, got %v", err)
	}
}

func TestCacheUpdateDoesNotInvalidateOnError(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.updateFn = func(_ context.Context, _ *pb.User) error {
		return ErrNotFound
	}
	cache := NewCacheRepository(mock, time.Minute)

	cache.set("1", &pb.User{Id: "1", Name: "cached"})

	err := cache.Update(context.Background(), &pb.User{Id: "1", Name: "updated"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// Cache should still have the entry
	u, ok := cache.get("1")
	if !ok || u.GetName() != "cached" {
		t.Fatalf("expected cached entry to survive failed update")
	}
}

func TestCacheDeleteInvalidates(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.deleteFn = func(_ context.Context, _ string) error { return nil }
	cache := NewCacheRepository(mock, time.Minute)

	cache.set("1", &pb.User{Id: "1"})
	if err := cache.Delete(context.Background(), "1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := cache.get("1"); ok {
		t.Fatal("expected cache entry to be invalidated after Delete")
	}
}

func TestCacheReadModifyUpdateInvalidates(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	cache := NewCacheRepository(mock, time.Minute)

	cache.set("1", &pb.User{Id: "1", Name: "old"})

	u, err := cache.ReadModifyUpdate(context.Background(), "1", func(u *pb.User) error {
		u.Name = "new"
		return nil
	})
	if err != nil {
		t.Fatalf("ReadModifyUpdate: %v", err)
	}
	if u.GetName() != "new" {
		t.Fatalf("expected 'new', got %q", u.GetName())
	}

	// Should be invalidated
	if _, ok := cache.get("1"); ok {
		t.Fatal("expected cache entry to be invalidated after ReadModifyUpdate")
	}
}

func TestCacheReadModifyUpdateServesStaleOnUnavailable(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.readModifyUpdateFn = func(_ context.Context, id string, fn ReadModifyUpdateFn) (*pb.User, error) {
		return nil, ErrUnavailable
	}
	cache := NewCacheRepository(mock, time.Minute)

	cache.set("1", &pb.User{Id: "1", Name: "stale-rmu"})

	u, err := cache.ReadModifyUpdate(context.Background(), "1", func(u *pb.User) error {
		return nil
	})
	if err != nil {
		t.Fatalf("stale ReadModifyUpdate: %v", err)
	}
	if u.GetName() != "stale-rmu" {
		t.Fatalf("expected 'stale-rmu', got %q", u.GetName())
	}
}

func TestCacheTTLExpires(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		return &pb.User{Id: id, Name: "fresh"}, nil
	}
	cache := NewCacheRepository(mock, 30*time.Millisecond)
	ctx := context.Background()

	// Populate cache
	u, err := cache.Get(ctx, "1")
	if err != nil || u.GetName() != "fresh" {
		t.Fatalf("first Get: %v", err)
	}

	// Wait for TTL to expire
	time.Sleep(100 * time.Millisecond)

	// Should miss cache and call repo again
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		return &pb.User{Id: id, Name: "refreshed"}, nil
	}
	u, err = cache.Get(ctx, "1")
	if err != nil || u.GetName() != "refreshed" {
		t.Fatalf("expected 'refreshed' after TTL, got %q: %v", u.GetName(), err)
	}
}

func TestCacheCreatePassesThrough(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	var called bool
	mock.createFn = func(_ context.Context, user *pb.User, maxUsers int) error {
		called = true
		if user.GetName() != "newuser" {
			t.Fatalf("unexpected name: %q", user.GetName())
		}
		return nil
	}
	cache := NewCacheRepository(mock, time.Minute)
	if err := cache.Create(context.Background(), &pb.User{Name: "newuser"}, 100); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !called {
		t.Fatal("Create did not pass through")
	}
}

func TestCacheConcurrentAccess(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		time.Sleep(time.Millisecond)
		return &pb.User{Id: id, Name: "concurrent"}, nil
	}
	cache := NewCacheRepository(mock, time.Minute)
	ctx := context.Background()

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			u, err := cache.Get(ctx, "1")
			if err != nil || u.GetName() != "concurrent" {
				t.Errorf("concurrent Get: %v, %v", err, u)
			}
		}()
	}
	wg.Wait()

	// All requests should succeed
	c := mock.getCallCount
	if c < 1 || c > 20 {
		t.Fatalf("expected between 1 and 20 calls to underlying repo, got %d", c)
	}
}

func TestCacheProtoCloneIsolation(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, id string) (*pb.User, error) {
		return &pb.User{Id: id, Name: "original", Email: "a@b.com", Age: 25}, nil
	}
	cache := NewCacheRepository(mock, time.Minute)
	ctx := context.Background()

	u1, _ := cache.Get(ctx, "1")
	u2, _ := cache.Get(ctx, "1")

	// Both should be independent clones
	u1.Name = "changed1"
	u2.Email = "changed2@b.com"

	u3, _ := cache.Get(ctx, "1")
	if u3.GetName() != "original" || u3.GetEmail() != "a@b.com" || u3.GetAge() != 25 {
		t.Fatalf("isolation broken: %+v", u3)
	}
}

func TestCacheSetGetRoundTrip(t *testing.T) {
	t.Parallel()
	cache := NewCacheRepository(newMockRepo(), time.Minute)

	u := &pb.User{Id: "x", Name: "direct"}
	cache.set("x", u)
	u.Name = "mutated"

	got, ok := cache.get("x")
	if !ok {
		t.Fatal("entry not found")
	}
	if got.GetName() != "direct" {
		t.Fatalf("expected 'direct', got %q", got.GetName())
	}
}
