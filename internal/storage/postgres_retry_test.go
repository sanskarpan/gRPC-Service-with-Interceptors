package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestIsRetryableError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, false},
		{"context deadline", context.DeadlineExceeded, false},
		{"EOF", io.EOF, true},
		{"unexpected EOF", io.ErrUnexpectedEOF, true},
		{"serialization failure", &pgconn.PgError{Code: "40001"}, true},
		{"deadlock detected", &pgconn.PgError{Code: "40P01"}, true},
		{"lock not available", &pgconn.PgError{Code: "55P03"}, true},
		{"connection failure", &pgconn.PgError{Code: "08006"}, true},
		{"too many connections", &pgconn.PgError{Code: "53300"}, true},
		{"cannot connect now", &pgconn.PgError{Code: "57P03"}, true},
		{"internal error", &pgconn.PgError{Code: "XX000"}, true},
		{"unique violation (not retryable)", &pgconn.PgError{Code: "23505"}, false},
		{"not found (wrapped)", fmt.Errorf("get user: %w", ErrNotFound), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isRetryableError(tt.err)
			if got != tt.want {
				t.Errorf("isRetryableError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestBackoffDuration(t *testing.T) {
	t.Parallel()
	base := 100 * time.Millisecond
	cap := time.Second
	prev := time.Duration(0)
	for attempt := range 5 {
		d := backoffDuration(base, attempt, cap)
		if d <= 0 {
			t.Errorf("backoffDuration(attempt=%d) = %v, want >0", attempt, d)
		}
		if attempt > 0 && d < prev/2 {
			t.Errorf("backoffDuration(attempt=%d) = %v < prev/2=%v, should be approximately non-decreasing", attempt, d, prev/2)
		}
		if d > cap*2 {
			t.Errorf("backoffDuration(attempt=%d) = %v, exceeds 2x cap %v", attempt, d, cap)
		}
		prev = d
	}
}

func TestBackoffDurationJitter(t *testing.T) {
	t.Parallel()
	// With enough samples, we should observe both faster and slower than base.
	base := 100 * time.Millisecond
	seenBelow := false
	seenAbove := false
	for range 50 {
		d := backoffDuration(base, 0, time.Second)
		if d < base {
			seenBelow = true
		}
		if d > base {
			seenAbove = true
		}
	}
	if !seenBelow || !seenAbove {
		t.Errorf("jitter should produce values both below and above base; below=%v above=%v", seenBelow, seenAbove)
	}
}

func TestRetryOperationSucceedsOnFirstAttempt(t *testing.T) {
	t.Parallel()
	cfg := retryConfig{maxAttempts: 3, baseBackoff: time.Millisecond, maxBackoff: 10 * time.Millisecond}
	calls := 0
	err := retryOperation(context.Background(), cfg, func(ctx context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestRetryOperationRetriesTransientError(t *testing.T) {
	t.Parallel()
	cfg := retryConfig{maxAttempts: 3, baseBackoff: time.Millisecond, maxBackoff: 10 * time.Millisecond}
	calls := 0
	err := retryOperation(context.Background(), cfg, func(ctx context.Context) error {
		calls++
		if calls < 3 {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestRetryOperationExhaustsAttempts(t *testing.T) {
	t.Parallel()
	cfg := retryConfig{maxAttempts: 3, baseBackoff: time.Millisecond, maxBackoff: 10 * time.Millisecond}
	expectedErr := io.ErrUnexpectedEOF
	calls := 0
	err := retryOperation(context.Background(), cfg, func(ctx context.Context) error {
		calls++
		return expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Errorf("expected %v, got %v", expectedErr, err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestRetryOperationNonRetryableStopsImmediately(t *testing.T) {
	t.Parallel()
	cfg := retryConfig{maxAttempts: 3, baseBackoff: time.Millisecond, maxBackoff: 10 * time.Millisecond}
	calls := 0
	err := retryOperation(context.Background(), cfg, func(ctx context.Context) error {
		calls++
		return ErrNotFound
	})
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestRetryOperationContextCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := retryConfig{maxAttempts: 3, baseBackoff: time.Millisecond, maxBackoff: 10 * time.Millisecond}
	calls := 0
	err := retryOperation(ctx, cfg, func(ctx context.Context) error {
		calls++
		return io.ErrUnexpectedEOF
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if calls > 1 {
		t.Errorf("expected at most 1 call, got %d", calls)
	}
}

func TestRetryOperationContextDeadline(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), -time.Second)
	defer cancel()
	cfg := retryConfig{maxAttempts: 3, baseBackoff: time.Millisecond, maxBackoff: 10 * time.Millisecond}
	calls := 0
	err := retryOperation(ctx, cfg, func(ctx context.Context) error {
		calls++
		return io.ErrUnexpectedEOF
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected context.DeadlineExceeded, got %v", err)
	}
}

func TestRetryOperationBackoffRespectsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cfg := retryConfig{maxAttempts: 5, baseBackoff: time.Hour, maxBackoff: time.Hour} // huge backoff
	done := make(chan struct{})
	go func() {
		err := retryOperation(ctx, cfg, func(ctx context.Context) error {
			return io.ErrUnexpectedEOF
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("retryOperation did not respect context cancellation during backoff")
	}
}

func TestRetryOperationZeroAttempts(t *testing.T) {
	t.Parallel()
	cfg := retryConfig{maxAttempts: 0, baseBackoff: time.Millisecond, maxBackoff: 10 * time.Millisecond}
	err := retryOperation(context.Background(), cfg, func(ctx context.Context) error {
		return io.ErrUnexpectedEOF
	})
	if err != nil {
		t.Errorf("expected nil error for zero attempts, got %v", err)
	}
}

func TestRetryOperationWrapsContextErrorWithoutRetry(t *testing.T) {
	t.Parallel()
	cfg := retryConfig{maxAttempts: 3, baseBackoff: time.Millisecond, maxBackoff: 10 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := retryOperation(ctx, cfg, func(ctx context.Context) error {
		return context.Canceled
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}
