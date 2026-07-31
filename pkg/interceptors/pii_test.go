package interceptors

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
)

// piiReq is a request payload carrying PII sentinels. The logging, metrics and
// audit interceptors must never serialize request/response bodies, so these
// sentinels must not appear in any emitted log line.
type piiReq struct {
	Name  string
	Email string
}

func TestInterceptorsDoNotLeakPayloadPII(t *testing.T) {
	const nameSentinel = "SENTINEL_PII_NAME"
	const emailSentinel = "pii@secret.example"

	out := captureLogOutput(t, func() {
		ctx := context.WithValue(context.Background(), requestIDKey{}, "req-1")
		ctx = context.WithValue(ctx, clientIDKey{}, "apikey:abc")
		req := &piiReq{Name: nameSentinel, Email: emailSentinel}
		info := &grpc.UnaryServerInfo{FullMethod: "/example.v1.UserService/CreateUser"}
		handler := func(context.Context, interface{}) (interface{}, error) {
			return &piiReq{Name: nameSentinel, Email: emailSentinel}, nil
		}
		// Exercise the interceptors that run per request and emit logs.
		_, _ = UnaryLoggingInterceptor(ctx, req, info, func(c context.Context, r interface{}) (interface{}, error) {
			return UnaryAuditInterceptor(c, r, info, handler)
		})
	})

	for _, sentinel := range []string{nameSentinel, emailSentinel} {
		if strings.Contains(out, sentinel) {
			t.Fatalf("PII sentinel %q leaked into logs:\n%s", sentinel, out)
		}
	}
	// Sanity: the interceptors did log (method), proving the assertion isn't vacuous.
	if !strings.Contains(out, "CreateUser") {
		t.Fatalf("expected the method to be logged, got:\n%s", out)
	}
}
