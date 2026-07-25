package interceptors

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// wrappedServerStream overrides the Context method of a grpc.ServerStream so
// that interceptors can inject values (e.g. request ID) into the stream's
// context without affecting the original stream.
type wrappedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedServerStream) Context() context.Context {
	return w.ctx
}

// requestIDKey is the context key for the request ID.
type requestIDKey struct{}

// RequestIDFromContext extracts the request ID from ctx.
func RequestIDFromContext(ctx context.Context) string {
	if v := ctx.Value(requestIDKey{}); v != nil {
		return v.(string)
	}
	return ""
}

// generateRequestID returns a hex-encoded 16-byte random identifier.
func generateRequestID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b)
}

// UnaryRequestIDInterceptor reads an existing x-request-id from incoming
// metadata or generates one when absent, and stores it in the context.
func UnaryRequestIDInterceptor(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	ctx = withRequestID(ctx)
	return handler(ctx, req)
}

// StreamRequestIDInterceptor reads or generates a request ID for streaming
// RPCs the same way the unary interceptor does.
func StreamRequestIDInterceptor(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	ctx := withRequestID(ss.Context())
	return handler(srv, &wrappedServerStream{ServerStream: ss, ctx: ctx})
}

func withRequestID(ctx context.Context) context.Context {
	rid := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md["x-request-id"]; len(v) > 0 && v[0] != "" {
			rid = v[0]
		}
	}
	if rid == "" {
		rid = generateRequestID()
	}
	ctx = context.WithValue(ctx, requestIDKey{}, rid)
	// Propagate so downstream services can read it from outgoing metadata
	ctx = metadata.AppendToOutgoingContext(ctx, "x-request-id", rid)
	return ctx
}
