package storage

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/pb"
)

func TestCircuitBreakerDefaultConfig(t *testing.T) {
	t.Parallel()
	cb := NewCircuitBreakerRepository(newMockRepo(), CircuitBreakerConfig{})
	defaults := DefaultCircuitBreakerConfig()
	if cb.cfg.FailureThreshold != defaults.FailureThreshold {
		t.Fatalf("expected default failure threshold %d, got %d", defaults.FailureThreshold, cb.cfg.FailureThreshold)
	}
	if cb.cfg.OpenTimeout != defaults.OpenTimeout {
		t.Fatalf("expected default open timeout %v, got %v", defaults.OpenTimeout, cb.cfg.OpenTimeout)
	}
	if cb.cfg.MaxHalfOpenRequests != defaults.MaxHalfOpenRequests {
		t.Fatalf("expected default half-open max %d, got %d", defaults.MaxHalfOpenRequests, cb.cfg.MaxHalfOpenRequests)
	}
	if cb.cfg.SuccessThreshold != defaults.SuccessThreshold {
		t.Fatalf("expected default success threshold %d, got %d", defaults.SuccessThreshold, cb.cfg.SuccessThreshold)
	}
	if cb.State() != CircuitBreakerClosed {
		t.Fatalf("expected initial state Closed, got %v", cb.State())
	}
}

func TestCircuitBreakerClosedToOpen(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
		return nil, errors.New("db error")
	}

	cb := NewCircuitBreakerRepository(mock, CircuitBreakerConfig{
		FailureThreshold: 3,
		OpenTimeout:     time.Minute,
		MaxHalfOpenRequests: 1,
	})

	ctx := context.Background()
	// Exhaust threshold
	for range 3 {
		_, err := cb.Get(ctx, "1")
		if err == nil {
			t.Fatal("expected error")
		}
	}
	if cb.State() != CircuitBreakerOpen {
		t.Fatalf("expected Open state, got %v", cb.State())
	}
	// Next call should fast-fail
	_, err := cb.Get(ctx, "1")
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreakerSuccessResetsFailureCount(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	attempts := 0
	mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
		attempts++
		if attempts <= 2 {
			return nil, errors.New("db error")
		}
		// Succeed on 3rd try
		return &pb.User{Id: "1", Name: "ok"}, nil
	}

	cb := NewCircuitBreakerRepository(mock, CircuitBreakerConfig{
		FailureThreshold: 5,
		OpenTimeout:     time.Minute,
		MaxHalfOpenRequests: 1,
	})

	ctx := context.Background()
	for range 2 {
		_, err := cb.Get(ctx, "1")
		if err == nil {
			t.Fatal("expected error")
		}
	}
	if cb.failureCount.Load() != 2 {
		t.Fatalf("expected 2 failures, got %d", cb.failureCount.Load())
	}

	// Succeed
	_, err := cb.Get(ctx, "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cb.failureCount.Load() != 0 {
		t.Fatalf("expected 0 failures after success, got %d", cb.failureCount.Load())
	}
	if cb.State() != CircuitBreakerClosed {
		t.Fatalf("expected Closed state, got %v", cb.State())
	}
}

func TestCircuitBreakerOpenToHalfOpenToClosed(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
		return &pb.User{Id: "1", Name: "recovered"}, nil
	}

	cb := NewCircuitBreakerRepository(mock, CircuitBreakerConfig{
		FailureThreshold:  2,
		OpenTimeout:       50 * time.Millisecond,
		SuccessThreshold:  1,
		MaxHalfOpenRequests: 1,
	})

	// Force open
	ctx := context.Background()
	for range 2 {
		mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
			return nil, errors.New("db error")
		}
		_, err := cb.Get(ctx, "1")
		if err == nil {
			t.Fatal("expected error")
		}
	}

	if cb.State() != CircuitBreakerOpen {
		t.Fatalf("expected Open state, got %v", cb.State())
	}

	// Wait for reset timeout
	time.Sleep(100 * time.Millisecond)

	mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
		return &pb.User{Id: "1", Name: "recovered"}, nil
	}

	u, err := cb.Get(ctx, "1")
	if err != nil {
		t.Fatalf("expected success in half-open, got %v", err)
	}
	if u.GetName() != "recovered" {
		t.Fatalf("expected 'recovered', got %q", u.GetName())
	}
	if cb.State() != CircuitBreakerClosed {
		t.Fatalf("expected Closed after half-open success, got %v", cb.State())
	}
}

func TestCircuitBreakerOpenToHalfOpenToOpen(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
		return nil, errors.New("still failing")
	}

	cb := NewCircuitBreakerRepository(mock, CircuitBreakerConfig{
		FailureThreshold: 2,
		OpenTimeout:     50 * time.Millisecond,
		MaxHalfOpenRequests: 1,
	})

	// Force open
	ctx := context.Background()
	for range 2 {
		_, err := cb.Get(ctx, "1")
		if err == nil {
			t.Fatal("expected error")
		}
	}

	// Wait for reset timeout
	time.Sleep(100 * time.Millisecond)

	// Allowed through half-open — fails, back to open
	_, err := cb.Get(ctx, "1")
	if !errors.Is(err, ErrCircuitOpen) && err.Error() != "still failing" {
		t.Fatalf("expected failure in half-open")
	}
	// Should be open again with failures reset
	if cb.State() != CircuitBreakerOpen {
		t.Fatalf("expected Open state after half-open failure, got %v", cb.State())
	}
	if cb.failureCount.Load() != 0 {
		t.Fatalf("expected 0 failures (reset), got %d", cb.failureCount.Load())
	}
}

func TestCircuitBreakerHalfOpenMaxRequests(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
		return &pb.User{Id: "1", Name: "ok"}, nil
	}

	cb := NewCircuitBreakerRepository(mock, CircuitBreakerConfig{
		FailureThreshold:    1,
		OpenTimeout:         50 * time.Millisecond,
		SuccessThreshold:    3,
		MaxHalfOpenRequests: 2,
	})

	// Force open
	ctx := context.Background()
	for range 1 {
		mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
			return nil, errors.New("db error")
		}
		_, _ = cb.Get(ctx, "1")
	}

	time.Sleep(100 * time.Millisecond)

	// First two requests allowed (go to half-open)
	mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
		return &pb.User{Id: "1", Name: "ok"}, nil
	}

	for range 2 {
		_, err := cb.Get(ctx, "1")
		if err != nil {
			t.Fatalf("expected first two requests through half-open: %v", err)
		}
	}

	// Third should be fast-failed as ErrCircuitOpen (only 2 allowed in half-open)
	_, err := cb.Get(ctx, "1")
	if !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen for third request in half-open, got %v", err)
	}
}

func TestCircuitBreakerCreateUpdateDeleteRMW(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.createFn = func(_ context.Context, _ *pb.User, _ int) error {
		return errors.New("db error")
	}

	cb := NewCircuitBreakerRepository(mock, CircuitBreakerConfig{
		FailureThreshold: 1,
		OpenTimeout:     time.Minute,
		MaxHalfOpenRequests: 1,
	})

	ctx := context.Background()
	// Trigger open with Create
	if err := cb.Create(ctx, &pb.User{Id: "1"}, 100); err == nil {
		t.Fatal("expected error")
	}

	if cb.State() != CircuitBreakerOpen {
		t.Fatalf("expected Open state, got %v", cb.State())
	}

	// All other operations should fast-fail
	if err := cb.Create(ctx, &pb.User{Id: "2"}, 100); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if _, err := cb.Get(ctx, "1"); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if err := cb.Update(ctx, &pb.User{Id: "1"}); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if _, err := cb.ReadModifyUpdate(ctx, "1", func(u *pb.User) error { return nil }); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if err := cb.Delete(ctx, "1"); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
	if err := cb.Ping(ctx); !errors.Is(err, ErrCircuitOpen) {
		t.Fatalf("expected ErrCircuitOpen, got %v", err)
	}
}

func TestCircuitBreakerPingDoesNotAffectFailures(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	var pingErr error
	mock.pingFn = func(_ context.Context) error { return pingErr }

	cb := NewCircuitBreakerRepository(mock, CircuitBreakerConfig{
		FailureThreshold: 2,
		OpenTimeout:     time.Minute,
		MaxHalfOpenRequests: 1,
	})

	// Ping with ErrUnavailable should not record failures
	pingErr = ErrUnavailable
	for range 5 {
		if err := cb.Ping(context.Background()); err == nil {
			t.Fatal("expected ping error")
		}
	}
	if cb.failureCount.Load() != 0 {
		t.Fatalf("expected 0 failures from Ping, got %d", cb.failureCount.Load())
	}
}

func TestCircuitBreakerCloseMethod(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
		return nil, errors.New("db error")
	}

	cb := NewCircuitBreakerRepository(mock, CircuitBreakerConfig{
		FailureThreshold: 1,
		OpenTimeout:     time.Minute,
		MaxHalfOpenRequests: 1,
	})

	_, _ = cb.Get(context.Background(), "1")
	if cb.State() != CircuitBreakerOpen {
		t.Fatalf("expected Open, got %v", cb.State())
	}

	cb.Close()
	if cb.State() != CircuitBreakerClosed {
		t.Fatalf("expected Closed after Close(), got %v", cb.State())
	}
	if cb.failureCount.Load() != 0 {
		t.Fatalf("expected 0 failures after Close(), got %d", cb.failureCount.Load())
	}

	// Should work again
	mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
		return &pb.User{Id: "1", Name: "ok"}, nil
	}
	_, err := cb.Get(context.Background(), "1")
	if err != nil {
		t.Fatalf("expected success after Close(): %v", err)
	}
}

func TestCircuitBreakerConcurrency(t *testing.T) {
	t.Parallel()
	mock := newMockRepo()
	var callCount atomic.Int64
	mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
		callCount.Add(1)
		return nil, errors.New("db error")
	}

	cb := NewCircuitBreakerRepository(mock, CircuitBreakerConfig{
		FailureThreshold: 5,
		OpenTimeout:     100 * time.Millisecond,
		MaxHalfOpenRequests: 3,
	})

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = cb.Get(context.Background(), "x")
		}()
	}
	wg.Wait()

	// Should have reached Open state
	if cb.State() != CircuitBreakerOpen {
		t.Fatalf("expected Open after concurrent failures, got %v", cb.State())
	}

	// Wait for reset
	time.Sleep(150 * time.Millisecond)

	// One more batch — some should go through as half-open probes
	mock.getFn = func(_ context.Context, _ string) (*pb.User, error) {
		return &pb.User{Id: "x", Name: "recovered"}, nil
	}

	var wg2 sync.WaitGroup
	for range 10 {
		wg2.Add(1)
		go func() {
			defer wg2.Done()
			u, err := cb.Get(context.Background(), "x")
			if err != nil && !errors.Is(err, ErrCircuitOpen) {
				t.Errorf("unexpected error: %v", err)
			}
			if err == nil && u.GetName() != "recovered" {
				t.Errorf("unexpected name: %q", u.GetName())
			}
		}()
	}
	wg2.Wait()

	// Should now be closed after successful probes
	if cb.State() != CircuitBreakerClosed {
		t.Fatalf("expected Closed after recovery, got %v", cb.State())
	}
}

func TestCircuitBreakerCloseIdempotent(t *testing.T) {
	t.Parallel()
	cb := NewCircuitBreakerRepository(newMockRepo(), CircuitBreakerConfig{})
	cb.Close()
	cb.Close()
	if cb.State() != CircuitBreakerClosed {
		t.Fatalf("expected Closed")
	}
}

func TestErrCircuitOpenSentinel(t *testing.T) {
	t.Parallel()
	if !errors.Is(ErrCircuitOpen, ErrCircuitOpen) {
		t.Fatal("ErrCircuitOpen should match itself")
	}
}
