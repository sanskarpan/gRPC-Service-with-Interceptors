// Package interceptors contains the authentication, resilience, logging, and
// metrics middleware used at gRPC client and server boundaries.
package interceptors

import (
	"context"
	"time"

	"github.com/example/grpc-service/pkg/logging"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

var (
	// ClientTotalRequests counts outbound gRPC calls by method.
	ClientTotalRequests = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grpc_client_total_requests",
			Help: "Total number of gRPC client requests",
		},
		[]string{"method"},
	)

	// ClientTotalErrors counts outbound gRPC failures by method and status code.
	ClientTotalErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grpc_client_total_errors",
			Help: "Total number of gRPC client errors",
		},
		[]string{"method", "code"},
	)

	// ClientRequestDuration measures outbound gRPC latency in seconds.
	ClientRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "grpc_client_request_duration_seconds",
			Help:    "gRPC client request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)
)

// IncClientTotalRequests records one outbound gRPC request for the method.
func IncClientTotalRequests(method string) {
	ClientTotalRequests.WithLabelValues(method).Inc()
}

// IncClientTotalErrors records an outbound gRPC request that ended with a status code.
func IncClientTotalErrors(method, code string) {
	ClientTotalErrors.WithLabelValues(method, code).Inc()
}

// ObserveClientRequestDuration records the elapsed outbound request duration.
func ObserveClientRequestDuration(method string, duration time.Duration) {
	ClientRequestDuration.WithLabelValues(method).Observe(duration.Seconds())
}

// UnaryClientLoggingInterceptor logs the start and completion of outbound unary calls.
func UnaryClientLoggingInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	start := time.Now()

	logger := logging.With().Str("method", method).Logger()

	logger.Info().Msg("gRPC client request started")

	err := invoker(ctx, method, req, reply, cc, opts...)

	duration := time.Since(start)
	logger.Info().Dur("duration", duration).Str("error", clientErrToString(err)).Msg("gRPC client request completed")

	return err
}

// UnaryClientMetricsInterceptor records outbound unary request count, latency, and errors.
func UnaryClientMetricsInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	start := time.Now()

	IncClientTotalRequests(method)

	err := invoker(ctx, method, req, reply, cc, opts...)

	duration := time.Since(start)
	ObserveClientRequestDuration(method, duration)

	if err != nil {
		IncClientTotalErrors(method, clientGetErrorCode(err))
	}

	return err
}

// UnaryClientAuthInterceptor preserves the compatibility API without inventing credentials.
func UnaryClientAuthInterceptor(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	return UnaryClientAuthInterceptorWithToken("")(ctx, method, req, reply, cc, invoker, opts...)
}

// UnaryClientAuthInterceptorWithToken adds a caller-provided bearer token and
// never invents credentials when the caller intentionally leaves it empty.
func UnaryClientAuthInterceptorWithToken(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if token != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		}
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// StreamClientLoggingInterceptor logs the creation of outbound streaming calls.
func StreamClientLoggingInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	logger := logging.With().Str("method", method).Logger()

	logger.Info().Msg("gRPC client stream started")

	stream, err := streamer(ctx, desc, cc, method, opts...)

	logger.Info().Str("error", clientErrToString(err)).Msg("gRPC client stream created")

	return stream, err
}

// StreamClientMetricsInterceptor records outbound streaming call count, latency, and errors.
func StreamClientMetricsInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	IncClientTotalRequests(method)

	start := time.Now()
	stream, err := streamer(ctx, desc, cc, method, opts...)
	duration := time.Since(start)

	ObserveClientRequestDuration(method, duration)

	if err != nil {
		IncClientTotalErrors(method, clientGetErrorCode(err))
	}

	return stream, err
}

// StreamClientAuthInterceptorWithToken adds the same caller-provided
// credential to streaming RPC metadata.
func StreamClientAuthInterceptorWithToken(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		if token != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		}
		return streamer(ctx, desc, cc, method, opts...)
	}
}

func clientErrToString(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

func clientGetErrorCode(err error) string {
	if s, ok := status.FromError(err); ok {
		return s.Code().String()
	}
	return "unknown"
}
