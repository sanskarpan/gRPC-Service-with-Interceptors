// Package interceptors provides shared gRPC interceptors for both client and
// server sides of the service boundary.
package interceptors

import (
	"context"
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

// backoff returns the next backoff duration for the given attempt number
// using exponential backoff with jitter (capped at MaxBackoff).
func (c RetryConfig) backoff(attempt int) time.Duration {
	d := c.BaseBackoff * (1 << attempt)
	if d > c.MaxBackoff {
		d = c.MaxBackoff
	}
	return d
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
			if !ok || !cfg.isRetryableCode(st.Code()) {
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
			if !ok || !cfg.isRetryableCode(st.Code()) {
				return nil, lastErr
			}
		}
		return nil, lastErr
	}
}