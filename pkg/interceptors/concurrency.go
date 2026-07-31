package interceptors

import (
	"context"

	"github.com/example/grpc-service/pkg/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewConcurrencyLimitInterceptor bounds the number of unary requests executing
// concurrently and sheds excess with ResourceExhausted, so a slow dependency
// (e.g. a struggling database) cannot cause an unbounded goroutine/resource
// pileup. Token-bucket rate limiting caps arrival *rate*; this caps in-flight
// *concurrency*, which is the dimension that grows when downstream latency rises.
//
// Design notes:
//   - Unary only. Streams are long-lived; a fixed slot held for a stream's
//     lifetime would starve unary traffic. Streams are already bounded by
//     MaxConcurrentStreams and per-stream message caps. (The CollectUserMetrics
//     streaming ingest is therefore not bounded by this limiter.)
//   - Health checks are exempt so k8s liveness/readiness probes never shed under
//     the very overload this feature targets.
//   - Place it after authentication so a slot represents real, authorized,
//     DB-bound work — an unauthenticated flood cannot occupy slots.
//   - Size max relative to the database pool (Storage.MaxOpenConns): protecting
//     the DB from a thundering herd is the goal.
//
// max <= 0 disables the limiter (pass-through).
func NewConcurrencyLimitInterceptor(max int) grpc.UnaryServerInterceptor {
	if max <= 0 {
		return func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			return handler(ctx, req)
		}
	}
	sem := make(chan struct{}, max)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !requiresAuth(info.FullMethod) {
			return handler(ctx, req)
		}
		select {
		case sem <- struct{}{}:
			// acquired — release only on this path.
		default:
			metrics.IncLoadShed(info.FullMethod)
			return nil, status.Error(codes.ResourceExhausted, "server at capacity")
		}
		defer func() { <-sem }()
		metrics.AddInFlight(1)
		defer metrics.AddInFlight(-1)
		return handler(ctx, req)
	}
}
