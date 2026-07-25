package interceptors

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/example/grpc-service/pkg/logging"
	"github.com/example/grpc-service/pkg/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryLoggingInterceptor records bounded request lifecycle fields without
// serializing user payloads or metadata into logs.
func UnaryLoggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	logger := logging.With().Str("method", info.FullMethod).Str("request_id", getRequestID(ctx)).Logger()
	logger.Info().Msg("gRPC request started")
	resp, err := handler(ctx, req)
	logger.Info().Dur("duration", time.Since(start)).Str("code", getErrorCode(err)).Msg("gRPC request completed")
	return resp, err
}

// UnaryMetricsInterceptor records all requests that reach the interceptor,
// including authentication failures when it is placed outside auth.
func UnaryMetricsInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	metrics.IncTotalRequests(info.FullMethod)
	resp, err := handler(ctx, req)
	metrics.ObserveRequestDuration(info.FullMethod, time.Since(start).Seconds())
	if err != nil {
		metrics.IncTotalErrors(info.FullMethod, getErrorCode(err))
	}
	return resp, err
}

// UnaryPanicRecoveryInterceptor converts handler panics into a generic internal
// status while retaining the panic value for operator logs.
func UnaryPanicRecoveryInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logging.Logger().Error().Interface("panic", recovered).Str("method", info.FullMethod).Msg("panic recovered in gRPC handler")
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	return handler(ctx, req)
}

// StreamLoggingInterceptor records stream lifecycle without logging messages.
func StreamLoggingInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	start := time.Now()
	logger := logging.With().Str("method", info.FullMethod).Str("stream", "true").Logger()
	logger.Info().Msg("gRPC stream started")
	err := handler(srv, ss)
	logger.Info().Dur("duration", time.Since(start)).Str("code", getErrorCode(err)).Msg("gRPC stream completed")
	return err
}

// StreamMetricsInterceptor records active streams and their terminal status.
func StreamMetricsInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	metrics.IncTotalRequests(info.FullMethod)
	metrics.IncActiveStreams()
	defer metrics.DecActiveStreams()
	start := time.Now()
	err := handler(srv, ss)
	metrics.ObserveRequestDuration(info.FullMethod, time.Since(start).Seconds())
	if err != nil {
		metrics.IncTotalErrors(info.FullMethod, getErrorCode(err))
	}
	return err
}

// StreamPanicRecoveryInterceptor converts stream handler panics to a generic
// internal status and ensures the process remains alive.
func StreamPanicRecoveryInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logging.Logger().Error().Interface("panic", recovered).Str("method", info.FullMethod).Msg("panic recovered in gRPC stream handler")
			err = status.Error(codes.Internal, "internal server error")
		}
	}()
	return handler(srv, ss)
}

func requiresAuth(method string) bool {
	switch method {
	case "/grpc.health.v1.Health/Check", "/grpc.health.v1.Health/Watch":
		return false
	default:
		return true
	}
}

func extractToken(ctx context.Context) (string, error) {
	credential, ok := credentialFromContext(ctx)
	if !ok {
		return "", authenticationError()
	}
	return credential, nil
}

func getRequestID(ctx context.Context) string {
	if rid := RequestIDFromContext(ctx); rid != "" {
		return rid
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	values := md.Get("x-request-id")
	if len(values) != 1 || len(values[0]) > 128 || !utf8.ValidString(values[0]) {
		return ""
	}
	for _, r := range values[0] {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return strings.TrimSpace(values[0])
}

func errToString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func getErrorCode(err error) string {
	if err == nil {
		return codes.OK.String()
	}
	return status.Code(err).String()
}
