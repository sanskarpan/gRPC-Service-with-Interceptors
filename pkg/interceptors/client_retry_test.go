package interceptors

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func fastRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:    3,
		BaseBackoff:    time.Millisecond,
		MaxBackoff:     5 * time.Millisecond,
		RetryableCodes: defaultRetryableCodes,
	}
}

func TestUnaryRetryDoesNotRetryInternalWithoutOptIn(t *testing.T) {
	t.Parallel()
	calls := 0
	invoker := func(_ context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		calls++
		return status.Error(codes.Internal, "boom")
	}
	intc := UnaryClientRetryInterceptor(fastRetryConfig())
	err := intc(context.Background(), "/svc/Mutate", nil, nil, nil, invoker)
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("Internal on a non-idempotent call must NOT be retried without opt-in; got %d calls", calls)
	}
}

func TestUnaryRetryRetriesInternalWithOptIn(t *testing.T) {
	t.Parallel()
	calls := 0
	invoker := func(_ context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		calls++
		return status.Error(codes.Internal, "boom")
	}
	intc := UnaryClientRetryInterceptor(fastRetryConfig())
	ctx := WithRetryable(context.Background())
	_ = intc(ctx, "/svc/GetIdempotent", nil, nil, nil, invoker)
	if calls != 3 {
		t.Fatalf("Internal with opt-in must be retried up to MaxAttempts; got %d calls", calls)
	}
}

func TestUnaryRetryRetriesUnavailableWithoutOptIn(t *testing.T) {
	t.Parallel()
	calls := 0
	invoker := func(_ context.Context, _ string, _, _ any, _ *grpc.ClientConn, _ ...grpc.CallOption) error {
		calls++
		return status.Error(codes.Unavailable, "conn refused")
	}
	intc := UnaryClientRetryInterceptor(fastRetryConfig())
	_ = intc(context.Background(), "/svc/Mutate", nil, nil, nil, invoker)
	if calls != 3 {
		t.Fatalf("Unavailable (request never processed) is safe to retry for any RPC; got %d calls", calls)
	}
}

func TestRetryBackoffJitterWithinBounds(t *testing.T) {
	t.Parallel()
	cfg := RetryConfig{BaseBackoff: 10 * time.Millisecond, MaxBackoff: 40 * time.Millisecond}
	values := make(map[time.Duration]struct{})
	for attempt := range 3 {
		expCap := cfg.BaseBackoff * (1 << attempt)
		if expCap > cfg.MaxBackoff {
			expCap = cfg.MaxBackoff
		}
		for range 20 {
			d := cfg.backoff(attempt)
			if d <= 0 || d > expCap {
				t.Fatalf("backoff(%d)=%v out of bounds (0, %v]", attempt, d, expCap)
			}
			values[d] = struct{}{}
		}
	}
	if len(values) < 2 {
		t.Fatalf("expected jittered backoff to produce varied values, got %d distinct", len(values))
	}
}
