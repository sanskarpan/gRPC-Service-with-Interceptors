// Package interceptors provides shared gRPC interceptors for both client and
// server sides of the service boundary.
package interceptors

import (
	"context"
	"math/rand"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// defaultRetryableCodes are gRPC status codes that are safe to retry for
// idempotent or read-only RPCs.
var defaultRetryableCodes = []codes.Code{
	codes.Unavailable,
	codes.DeadlineExceeded,
	codes.ResourceExhausted,
	codes.Aborted,
	codes.Internal,
}

// safeRetryableCodes are codes where the server provably did not process the
// request (connection refused, or admission-rejected before the handler ran),
// so retrying is safe even for non-idempotent RPCs like Create/Update/Delete.
var safeRetryableCodes = []codes.Code{
	codes.Unavailable,
	codes.ResourceExhausted,
}

type retryableKey struct{}

// WithRetryable marks ctx so the retry interceptor may retry the call on the
// full RetryableCodes set (including DeadlineExceeded, Aborted, Internal).
// Use ONLY for idempotent RPCs: retrying a non-idempotent mutation on those
// codes risks duplicate side effects because the server may have applied the
// first attempt.
func WithRetryable(ctx context.Context) context.Context {
	return context.WithValue(ctx, retryableKey{}, true)
}

func isRetryableOptIn(ctx context.Context) bool {
	v, _ := ctx.Value(retryableKey{}).(bool)
	return v
}

// shouldRetry decides whether a call may be retried for the given code. Without
// the WithRetryable opt-in only the always-safe codes are retried, so mutations
// are not replayed on ambiguous failures.
func (c RetryConfig) shouldRetry(ctx context.Context, code codes.Code) bool {
	if isRetryableOptIn(ctx) {
		return c.isRetryableCode(code)
	}
	for _, rc := range safeRetryableCodes {
		if rc == code {
			return true
		}
	}
	return false
}

// RetryConfig controls the client-side retry behaviour.
type RetryConfig struct {
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	RetryableCodes []codes.Code
}

// DefaultRetryConfig returns a sensible default retry configuration.
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxAttempts:    3,
		BaseBackoff:    50 * time.Millisecond,
		MaxBackoff:     2 * time.Second,
		RetryableCodes: defaultRetryableCodes,
	}
}

// isRetryableCode checks whether the given code is in the retryable set.
func (c RetryConfig) isRetryableCode(code codes.Code) bool {
	for _, rc := range c.RetryableCodes {
		if rc == code {
			return true
		}
	}
	return false
}

// backoff returns the next backoff duration for the given attempt number using
// exponential backoff with full jitter, capped at MaxBackoff. Full jitter
// (a uniform random value in (0, cap]) spreads retries so a fleet of clients
// does not synchronize into a retry storm.
func (c RetryConfig) backoff(attempt int) time.Duration {
	d := c.BaseBackoff
	if attempt > 0 {
		// Guard against overflow when shifting by a large attempt count.
		if attempt >= 62 {
			d = c.MaxBackoff
		} else {
			d = c.BaseBackoff * (1 << attempt)
		}
	}
	if d <= 0 || d > c.MaxBackoff {
		d = c.MaxBackoff
	}
	// Full jitter in (0, d]: pick uniformly in [1, d].
	if d <= 1 {
		return d
	}
	return time.Duration(rand.Int63n(int64(d)) + 1)
}

// UnaryClientRetryInterceptor returns a grpc.UnaryClientInterceptor that
// retries unary RPCs on configured transient codes. It does NOT retry
// mutation RPCs (Create, Update, Delete) unless the caller explicitly
// opts in via the WithRetryable context value.
func UnaryClientRetryInterceptor(cfg RetryConfig) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var lastErr error
		for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
			if attempt > 0 {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(cfg.backoff(attempt - 1)):
				}
			}
			lastErr = invoker(ctx, method, req, reply, cc, opts...)
			if lastErr == nil {
				return nil
			}
			st, ok := status.FromError(lastErr)
			if !ok || !cfg.shouldRetry(ctx, st.Code()) {
				return lastErr
			}
		}
		return lastErr
	}
}

// StreamClientRetryInterceptor returns a grpc.StreamClientInterceptor that
// re-establishes client-streaming and bidi streams on transient errors.
func StreamClientRetryInterceptor(cfg RetryConfig) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		var lastErr error
		for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
			if attempt > 0 {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(cfg.backoff(attempt - 1)):
				}
			}
			stream, err := streamer(ctx, desc, cc, method, opts...)
			if err == nil {
				return stream, nil
			}
			lastErr = err
			st, ok := status.FromError(err)
			if !ok || !cfg.shouldRetry(ctx, st.Code()) {
				return nil, lastErr
			}
		}
		return nil, lastErr
	}
}
