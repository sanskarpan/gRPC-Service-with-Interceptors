package interceptors

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/config"
	"github.com/example/grpc-service/pkg/logging"
	"github.com/example/grpc-service/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/rs/zerolog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestRequiresAuth(t *testing.T) {
	tests := map[string]bool{
		"/grpc.health.v1.Health/Check":    false,
		"/grpc.health.v1.Health/Watch":    false,
		"/example.v1.UserService/GetUser":    true,
		"/example.v1.UserService/CreateUser": true,
		"/example.v1.UserService/Unknown":    true,
		"/unknown.Service/Method":         true,
	}
	for method, expected := range tests {
		t.Run(method, func(t *testing.T) {
			if got := requiresAuth(method); got != expected {
				t.Fatalf("requiresAuth(%q) = %v, want %v", method, got, expected)
			}
		})
	}
}

func TestExtractTokenStrictBearer(t *testing.T) {
	tests := []struct {
		name      string
		metadata  metadata.MD
		wantToken string
		wantErr   bool
	}{
		{name: "valid", metadata: metadata.Pairs("authorization", "Bearer test-token"), wantToken: "test-token"},
		{name: "case insensitive scheme", metadata: metadata.Pairs("authorization", "bearer test-token"), wantToken: "test-token"},
		{name: "api key", metadata: metadata.Pairs("x-api-key", "api-key-value"), wantToken: "api-key-value"},
		{name: "missing metadata", wantErr: true},
		{name: "missing authorization", metadata: metadata.Pairs("other", "value"), wantErr: true},
		{name: "empty bearer", metadata: metadata.Pairs("authorization", "Bearer "), wantErr: true},
		{name: "raw token", metadata: metadata.Pairs("authorization", "test-token"), wantErr: true},
		{name: "multiple headers", metadata: metadata.Pairs("authorization", "Bearer one", "authorization", "Bearer two"), wantErr: true},
		{name: "ambiguous credentials prefers authorization", metadata: metadata.Pairs("x-api-key", "api-key-value", "authorization", "Bearer token"), wantToken: "token"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.metadata != nil {
				ctx = metadata.NewIncomingContext(ctx, tt.metadata)
			}
			token, err := extractToken(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("extractToken() error = %v, wantErr %v", err, tt.wantErr)
			}
			if token != tt.wantToken {
				t.Fatalf("extractToken() = %q, want %q", token, tt.wantToken)
			}
		})
	}
}

func TestAuthenticatorAPIKey(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{APIKeys: []string{"valid-api-key-12345"}})
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	valid := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", "valid-api-key-12345"))
	if !auth.Authenticate(valid) {
		t.Fatal("Authenticator rejected a configured API key")
	}
	invalid := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", "wrong-api-key-12345"))
	if auth.Authenticate(invalid) {
		t.Fatal("Authenticator accepted an unknown API key")
	}
}

func TestAuthenticatorJWTClaimsAndSignature(t *testing.T) {
	secret := "jwt-test-secret"
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: secret})
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	token := signTestJWT(t, secret, time.Now().Add(time.Minute).Unix())
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if !auth.Authenticate(ctx) {
		t.Fatal("Authenticator rejected a valid JWT")
	}

	expired := signTestJWT(t, secret, time.Now().Add(-time.Minute).Unix())
	expiredCtx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+expired))
	if auth.Authenticate(expiredCtx) {
		t.Fatal("Authenticator accepted an expired JWT")
	}
}

func TestUnaryAuthInterceptor(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{APIKeys: []string{"valid-api-key-12345"}})
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	handler := func(context.Context, interface{}) (interface{}, error) { return "success", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/example.v1.UserService/CreateUser"}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", "valid-api-key-12345"))
	resp, err := UnaryAuthInterceptorWithAuthenticator(auth)(ctx, "request", info, handler)
	if err != nil || resp != "success" {
		t.Fatalf("valid credentials: resp=%v err=%v", resp, err)
	}

	_, err = UnaryAuthInterceptorWithAuthenticator(auth)(context.Background(), "request", info, handler)
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing credentials code = %v, want Unauthenticated", status.Code(err))
	}

	healthInfo := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}
	if _, err := UnaryAuthInterceptorWithAuthenticator(auth)(context.Background(), "request", healthInfo, handler); err != nil {
		t.Fatalf("health endpoint should be public: %v", err)
	}
}

func TestErrorHelpers(t *testing.T) {
	if got := errToString(nil); got != "" {
		t.Fatalf("errToString(nil) = %q", got)
	}
	err := status.Error(codes.NotFound, "not found")
	if got := errToString(err); got != "rpc error: code = NotFound desc = not found" {
		t.Fatalf("errToString() = %q", got)
	}
	if got := getErrorCode(nil); got != "OK" {
		t.Fatalf("getErrorCode(nil) = %q", got)
	}
	if got := getErrorCode(err); got != "NotFound" {
		t.Fatalf("getErrorCode() = %q", got)
	}
}

func TestRateLimitAndTimeoutInterceptors(t *testing.T) {
	unary, _ := NewRateLimitInterceptors(1, 1)
	info := &grpc.UnaryServerInfo{FullMethod: "/example.v1.UserService/GetUser"}
	handler := func(ctx context.Context, _ interface{}) (interface{}, error) {
		return "ok", nil
	}
	if _, err := unary(context.Background(), nil, info, handler); err != nil {
		t.Fatalf("first request rejected: %v", err)
	}
	if _, err := unary(context.Background(), nil, info, handler); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second request code = %v, want ResourceExhausted", status.Code(err))
	}

	timeout := UnaryTimeoutInterceptor(5 * time.Millisecond)
	_, err := timeout(context.Background(), nil, info, func(ctx context.Context, _ interface{}) (interface{}, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err != context.DeadlineExceeded {
		t.Fatalf("timeout error = %v, want %v", err, context.DeadlineExceeded)
	}
}

func FuzzExtractToken(f *testing.F) {
	f.Add("Bearer token")
	f.Add("not-a-header")
	f.Fuzz(func(t *testing.T, value string) {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", value))
		_, _ = extractToken(ctx)
	})
}

func signTestJWT(t *testing.T, secret string, exp int64) string {
	t.Helper()
	encode := func(value any) string {
		bytes, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal JWT segment: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(bytes)
	}
	header := encode(map[string]string{"alg": "HS256", "typ": "JWT"})
	payload := encode(map[string]int64{"exp": exp})
	input := header + "." + payload
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(input)); err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return fmt.Sprintf("%s.%s", input, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
}

// ---------------------------------------------------------------------------
// Helpers for stream interceptor tests
// ---------------------------------------------------------------------------

type fakeServerStream struct {
	ctx context.Context
	grpc.ServerStream
}

func (s *fakeServerStream) Context() context.Context { return s.ctx }
func (s *fakeServerStream) SendMsg(any) error        { return nil }
func (s *fakeServerStream) RecvMsg(any) error         { return nil }
func (s *fakeServerStream) SetHeader(metadata.MD) error  { return nil }
func (s *fakeServerStream) SendHeader(metadata.MD) error { return nil }
func (s *fakeServerStream) SetTrailer(metadata.MD)       {}

func captureLogOutput(t *testing.T, fn func()) string {
	t.Helper()
	var buf strings.Builder
	old := logging.Logger()
	l := zerolog.New(&buf).With().Timestamp().Logger()
	logging.SetLogger(&l)
	defer func() { logging.SetLogger(old) }()
	fn()
	return buf.String()
}

func signCustomJWT(t *testing.T, secret string, header, claims any) string {
	t.Helper()
	encode := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal JWT segment: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	h := encode(header)
	p := encode(claims)
	input := h + "." + p
	mac := hmac.New(sha256.New, []byte(secret))
	if _, err := mac.Write([]byte(input)); err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return fmt.Sprintf("%s.%s", input, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
}

// ---------------------------------------------------------------------------
// 1. Logging interceptors
// ---------------------------------------------------------------------------

func TestUnaryLoggingInterceptor(t *testing.T) {
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/test/LoggingMethod"}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-id", "req-42"))

	output := captureLogOutput(t, func() {
		_, err := UnaryLoggingInterceptor(ctx, "req", info, handler)
		if err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
	})

	if !strings.Contains(output, "/test/LoggingMethod") {
		t.Fatal("log output missing method")
	}
	if !strings.Contains(output, "req-42") {
		t.Fatal("log output missing request_id")
	}
	if !strings.Contains(output, "gRPC request started") {
		t.Fatal("log output missing started message")
	}
	if !strings.Contains(output, "gRPC request completed") {
		t.Fatal("log output missing completion message")
	}
}

func TestStreamLoggingInterceptor(t *testing.T) {
	handler := func(srv any, stream grpc.ServerStream) error { return nil }
	info := &grpc.StreamServerInfo{FullMethod: "/test/StreamLogging"}
	stream := &fakeServerStream{ctx: context.Background()}

	output := captureLogOutput(t, func() {
		if err := StreamLoggingInterceptor(nil, stream, info, handler); err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
	})

	if !strings.Contains(output, "/test/StreamLogging") {
		t.Fatal("log output missing method")
	}
	if !strings.Contains(output, "gRPC stream started") {
		t.Fatal("log output missing started message")
	}
	if !strings.Contains(output, "gRPC stream completed") {
		t.Fatal("log output missing completion message")
	}
}

// ---------------------------------------------------------------------------
// 2. Metrics interceptors
// ---------------------------------------------------------------------------

func TestUnaryMetricsInterceptor(t *testing.T) {
	method := "/test/UnaryMetrics"
	counter, err := metrics.TotalRequests.GetMetricWithLabelValues(method)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	before := testutil.ToFloat64(counter)

	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: method}
	if _, err := UnaryMetricsInterceptor(context.Background(), "req", info, handler); err != nil {
		t.Fatalf("UnaryMetricsInterceptor error: %v", err)
	}

	after := testutil.ToFloat64(counter)
	if after-before != 1 {
		t.Fatalf("TotalRequests increased by %f, want 1", after-before)
	}
}

func TestStreamMetricsInterceptor(t *testing.T) {
	method := "/test/StreamMetrics"
	counter, err := metrics.TotalRequests.GetMetricWithLabelValues(method)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	beforeCount := testutil.ToFloat64(counter)
	beforeActive := testutil.ToFloat64(metrics.ActiveStreams)

	var capturedActive float64
	handler := func(srv any, stream grpc.ServerStream) error {
		capturedActive = testutil.ToFloat64(metrics.ActiveStreams)
		return nil
	}
	info := &grpc.StreamServerInfo{FullMethod: method}
	stream := &fakeServerStream{ctx: context.Background()}

	if err := StreamMetricsInterceptor(nil, stream, info, handler); err != nil {
		t.Fatalf("StreamMetricsInterceptor error: %v", err)
	}

	afterCount := testutil.ToFloat64(counter)
	afterActive := testutil.ToFloat64(metrics.ActiveStreams)

	if afterCount-beforeCount != 1 {
		t.Fatalf("TotalRequests increased by %f, want 1", afterCount-beforeCount)
	}
	if capturedActive != beforeActive+1 {
		t.Fatalf("ActiveStreams during handler = %f, want %f", capturedActive, beforeActive+1)
	}
	if afterActive != beforeActive {
		t.Fatalf("ActiveStreams after handler = %f, want %f (before)", afterActive, beforeActive)
	}
}

// ---------------------------------------------------------------------------
// 3. Auth interceptors — health exemption
// ---------------------------------------------------------------------------

func TestUnaryAuthInterceptorHealthExempt(t *testing.T) {
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}

	resp, err := UnaryAuthInterceptor(context.Background(), "req", info, handler)
	if err != nil {
		t.Fatalf("health check should pass without auth: %v", err)
	}
	if resp != "ok" {
		t.Fatalf("resp = %v, want ok", resp)
	}
}

func TestStreamAuthInterceptorHealthExempt(t *testing.T) {
	handler := func(srv any, stream grpc.ServerStream) error { return nil }
	info := &grpc.StreamServerInfo{FullMethod: "/grpc.health.v1.Health/Watch"}
	stream := &fakeServerStream{ctx: context.Background()}

	if err := StreamAuthInterceptor(nil, stream, info, handler); err != nil {
		t.Fatalf("health watch should pass without auth: %v", err)
	}
}

func TestStreamAuthInterceptorWithAuthenticator(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{APIKeys: []string{"valid-key"}})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	handler := func(srv any, stream grpc.ServerStream) error { return nil }
	info := &grpc.StreamServerInfo{FullMethod: "/example.Service/Stream"}

	stream := &fakeServerStream{
		ctx: metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-api-key", "valid-key")),
	}
	if err := StreamAuthInterceptorWithAuthenticator(auth)(nil, stream, info, handler); err != nil {
		t.Fatalf("valid creds should pass: %v", err)
	}

	stream2 := &fakeServerStream{ctx: context.Background()}
	if err := StreamAuthInterceptorWithAuthenticator(auth)(nil, stream2, info, handler); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing creds code = %v, want Unauthenticated", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// 4. Panic recovery
// ---------------------------------------------------------------------------

func TestUnaryPanicRecoveryInterceptor(t *testing.T) {
	handler := func(ctx context.Context, req any) (any, error) {
		panic("something broke")
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/test/Panic"}

	_, err := UnaryPanicRecoveryInterceptor(context.Background(), "req", info, handler)
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal error, got %v", err)
	}
}

func TestStreamPanicRecoveryInterceptor(t *testing.T) {
	handler := func(srv any, stream grpc.ServerStream) error {
		panic("stream panic")
	}
	info := &grpc.StreamServerInfo{FullMethod: "/test/StreamPanic"}
	stream := &fakeServerStream{ctx: context.Background()}

	err := StreamPanicRecoveryInterceptor(nil, stream, info, handler)
	if status.Code(err) != codes.Internal {
		t.Fatalf("expected Internal error, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// 5. Rate limiting
// ---------------------------------------------------------------------------

func TestNewRateLimitInterceptorsClamping(t *testing.T) {
	unary, _ := NewRateLimitInterceptors(0, 0)

	info := &grpc.UnaryServerInfo{FullMethod: "/test/Clamp"}
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	if _, err := unary(context.Background(), nil, info, handler); err != nil {
		t.Fatalf("clamped 0/0 unary rejected: %v", err)
	}
	if _, err := unary(context.Background(), nil, info, handler); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second clamped unary should be exhausted, got %v", err)
	}

	_, stream := NewRateLimitInterceptors(0, 0)
	streamInfo := &grpc.StreamServerInfo{FullMethod: "/test/ClampStream"}
	s := &fakeServerStream{ctx: context.Background()}
	if err := stream(nil, s, streamInfo, func(srv any, ss grpc.ServerStream) error { return nil }); err != nil {
		t.Fatalf("clamped 0/0 stream rejected: %v", err)
	}
	if err := stream(nil, s, streamInfo, func(srv any, ss grpc.ServerStream) error { return nil }); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second clamped stream should be exhausted, got %v", err)
	}
}

func TestNewRateLimitInterceptorsStream(t *testing.T) {
	_, stream := NewRateLimitInterceptors(1, 1)
	info := &grpc.StreamServerInfo{FullMethod: "/test/RateLimitStream"}
	s := &fakeServerStream{ctx: context.Background()}
	handler := func(srv any, ss grpc.ServerStream) error { return nil }

	if err := stream(nil, s, info, handler); err != nil {
		t.Fatalf("first stream request rejected: %v", err)
	}
	if err := stream(nil, s, info, handler); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second stream request code = %v, want ResourceExhausted", status.Code(err))
	}
}

// ---------------------------------------------------------------------------
// 6. Timeout interceptors
// ---------------------------------------------------------------------------

func TestUnaryTimeoutInterceptorPassthrough(t *testing.T) {
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/test/Timeout"}

	resp, err := UnaryTimeoutInterceptor(0)(context.Background(), nil, info, handler)
	if err != nil || resp != "ok" {
		t.Fatalf("zero timeout passthrough: resp=%v err=%v", resp, err)
	}
	resp, err = UnaryTimeoutInterceptor(-1)(context.Background(), nil, info, handler)
	if err != nil || resp != "ok" {
		t.Fatalf("negative timeout passthrough: resp=%v err=%v", resp, err)
	}
}

func TestStreamTimeoutInterceptor(t *testing.T) {
	info := &grpc.StreamServerInfo{FullMethod: "/test/StreamTimeout"}
	s := &fakeServerStream{ctx: context.Background()}

	if err := StreamTimeoutInterceptor(0)(nil, s, info, func(srv any, ss grpc.ServerStream) error { return nil }); err != nil {
		t.Fatalf("zero timeout passthrough: %v", err)
	}

	err := StreamTimeoutInterceptor(5*time.Millisecond)(nil, s, info, func(srv any, ss grpc.ServerStream) error {
		<-ss.Context().Done()
		return ss.Context().Err()
	})
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestContextServerStream(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeServerStream{ctx: context.Background()}
	wrapped := &contextServerStream{ServerStream: stream, ctx: ctx}
	if wrapped.Context() != ctx {
		t.Fatal("Context() should return the overridden context")
	}
}

// ---------------------------------------------------------------------------
// 7. Client interceptors
// ---------------------------------------------------------------------------

func TestUnaryClientLoggingInterceptor(t *testing.T) {
	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return nil
	}

	output := captureLogOutput(t, func() {
		if err := UnaryClientLoggingInterceptor(context.Background(), "/test/ClientLog", "req", nil, nil, invoker); err != nil {
			t.Fatalf("interceptor error: %v", err)
		}
	})

	if !strings.Contains(output, "/test/ClientLog") {
		t.Fatal("log output missing method")
	}
	if !strings.Contains(output, "gRPC client request started") {
		t.Fatal("log output missing started")
	}
	if !strings.Contains(output, "gRPC client request completed") {
		t.Fatal("log output missing completed")
	}
}

func TestUnaryClientMetricsInterceptor(t *testing.T) {
	method := "/test/ClientMetrics"
	counter, err := ClientTotalRequests.GetMetricWithLabelValues(method)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	before := testutil.ToFloat64(counter)

	invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		return nil
	}
	if err := UnaryClientMetricsInterceptor(context.Background(), method, "req", nil, nil, invoker); err != nil {
		t.Fatalf("interceptor error: %v", err)
	}

	after := testutil.ToFloat64(counter)
	if after-before != 1 {
		t.Fatalf("ClientTotalRequests increased by %f, want 1", after-before)
	}
}

func TestUnaryClientAuthInterceptorWithToken(t *testing.T) {
	t.Run("empty token", func(t *testing.T) {
		var capturedCtx context.Context
		invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			capturedCtx = ctx
			return nil
		}
		interceptor := UnaryClientAuthInterceptorWithToken("")
		ctx := context.Background()
		if err := interceptor(ctx, "/test/Auth", nil, nil, nil, invoker); err != nil {
			t.Fatalf("interceptor error: %v", err)
		}
		if capturedCtx != ctx {
			t.Fatal("empty token should not modify context")
		}
	})

	t.Run("non-empty token", func(t *testing.T) {
		var capturedCtx context.Context
		invoker := func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
			capturedCtx = ctx
			return nil
		}
		interceptor := UnaryClientAuthInterceptorWithToken("my-token")
		if err := interceptor(context.Background(), "/test/Auth", nil, nil, nil, invoker); err != nil {
			t.Fatalf("interceptor error: %v", err)
		}
		md, ok := metadata.FromOutgoingContext(capturedCtx)
		if !ok {
			t.Fatal("expected outgoing metadata")
		}
		auth := md.Get("authorization")
		if len(auth) != 1 || auth[0] != "Bearer my-token" {
			t.Fatalf("authorization = %v, want [Bearer my-token]", auth)
		}
	})
}

func TestStreamClientAuthInterceptorWithToken(t *testing.T) {
	t.Run("empty token", func(t *testing.T) {
		var capturedCtx context.Context
		streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
			capturedCtx = ctx
			return nil, nil
		}
		interceptor := StreamClientAuthInterceptorWithToken("")
		ctx := context.Background()
		if _, err := interceptor(ctx, &grpc.StreamDesc{}, nil, "/test/StreamAuth", streamer); err != nil {
			t.Fatalf("interceptor error: %v", err)
		}
		if capturedCtx != ctx {
			t.Fatal("empty token should not modify context")
		}
	})

	t.Run("non-empty token", func(t *testing.T) {
		var capturedCtx context.Context
		streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
			capturedCtx = ctx
			return nil, nil
		}
		interceptor := StreamClientAuthInterceptorWithToken("stream-token")
		if _, err := interceptor(context.Background(), &grpc.StreamDesc{}, nil, "/test/StreamAuth", streamer); err != nil {
			t.Fatalf("interceptor error: %v", err)
		}
		md, ok := metadata.FromOutgoingContext(capturedCtx)
		if !ok {
			t.Fatal("expected outgoing metadata")
		}
		auth := md.Get("authorization")
		if len(auth) != 1 || auth[0] != "Bearer stream-token" {
			t.Fatalf("authorization = %v, want [Bearer stream-token]", auth)
		}
	})
}

func TestStreamClientLoggingInterceptor(t *testing.T) {
	streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return nil, nil
	}

	output := captureLogOutput(t, func() {
		if _, err := StreamClientLoggingInterceptor(context.Background(), &grpc.StreamDesc{}, nil, "/test/StreamClientLog", streamer); err != nil {
			t.Fatalf("interceptor error: %v", err)
		}
	})

	if !strings.Contains(output, "/test/StreamClientLog") {
		t.Fatal("log output missing method")
	}
	if !strings.Contains(output, "gRPC client stream started") {
		t.Fatal("log output missing started")
	}
	if !strings.Contains(output, "gRPC client stream created") {
		t.Fatal("log output missing created")
	}
}

func TestStreamClientMetricsInterceptor(t *testing.T) {
	method := "/test/StreamClientMetrics"
	counter, err := ClientTotalRequests.GetMetricWithLabelValues(method)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues: %v", err)
	}
	before := testutil.ToFloat64(counter)

	streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return nil, nil
	}
	if _, err := StreamClientMetricsInterceptor(context.Background(), &grpc.StreamDesc{}, nil, method, streamer); err != nil {
		t.Fatalf("interceptor error: %v", err)
	}

	after := testutil.ToFloat64(counter)
	if after-before != 1 {
		t.Fatalf("ClientTotalRequests increased by %f, want 1", after-before)
	}
}

// ---------------------------------------------------------------------------
// 8. Auth edge cases
// ---------------------------------------------------------------------------

func TestNewAuthenticatorEmptyConfig(t *testing.T) {
	_, err := NewAuthenticator(config.AuthConfig{})
	if err == nil {
		t.Fatal("expected error for empty config")
	}
	if err.Error() != "no authentication credentials configured" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAuthenticatorNilReceiver(t *testing.T) {
	var a *Authenticator
	if a.Authenticate(context.Background()) {
		t.Fatal("nil Authenticator should return false")
	}
}

func TestAuthenticatorJWTWrongAlg(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: "secret"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	token := signCustomJWT(t, "secret", map[string]string{"alg": "none"}, map[string]int64{"exp": time.Now().Add(time.Hour).Unix()})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if auth.Authenticate(ctx) {
		t.Fatal("JWT with alg:none should be rejected")
	}
}

func TestAuthenticatorJWTMissingExp(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: "secret"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	token := signCustomJWT(t, "secret", map[string]string{"alg": "HS256", "typ": "JWT"}, map[string]int64{})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if auth.Authenticate(ctx) {
		t.Fatal("JWT without exp should be rejected")
	}
}

func TestAuthenticatorJWTNbfFuture(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: "secret"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	futureNbf := time.Now().Add(time.Hour).Unix()
	exp := time.Now().Add(2 * time.Hour).Unix()
	token := signCustomJWT(t, "secret", map[string]string{"alg": "HS256", "typ": "JWT"}, map[string]int64{"exp": exp, "nbf": futureNbf})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if auth.Authenticate(ctx) {
		t.Fatal("JWT with nbf in the future should be rejected")
	}
}

func TestAuthenticatorJWTIatFutureRejected(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: "secret"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	exp := time.Now().Add(2 * time.Hour).Unix()
	// 10 minutes in the future is beyond the 60s skew tolerance.
	iatFuture := time.Now().Add(10 * time.Minute).Unix()
	token := signCustomJWT(t, "secret", map[string]string{"alg": "HS256", "typ": "JWT"}, map[string]int64{"exp": exp, "iat": iatFuture})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if auth.Authenticate(ctx) {
		t.Fatal("JWT with iat beyond clock-skew tolerance should be rejected")
	}
}

func TestAuthenticatorJWTIatSkewAccepted(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: "secret"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	exp := time.Now().Add(2 * time.Hour).Unix()
	// 10 seconds in the future is inside the 60s skew tolerance.
	iatNearFuture := time.Now().Add(10 * time.Second).Unix()
	token := signCustomJWT(t, "secret", map[string]string{"alg": "HS256", "typ": "JWT"}, map[string]int64{"exp": exp, "iat": iatNearFuture})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if !auth.Authenticate(ctx) {
		t.Fatal("JWT with iat within clock-skew tolerance should be accepted")
	}
}

func TestAuthenticatorJWTIatPastAccepted(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: "secret"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	exp := time.Now().Add(2 * time.Hour).Unix()
	iatPast := time.Now().Add(-time.Hour).Unix()
	token := signCustomJWT(t, "secret", map[string]string{"alg": "HS256", "typ": "JWT"}, map[string]int64{"exp": exp, "iat": iatPast})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if !auth.Authenticate(ctx) {
		t.Fatal("JWT with past iat should be accepted")
	}
}

func TestAuthenticatorJWTIatMissingAccepted(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: "secret"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	exp := time.Now().Add(time.Hour).Unix()
	// No iat claim.
	token := signCustomJWT(t, "secret", map[string]string{"alg": "HS256", "typ": "JWT"}, map[string]int64{"exp": exp})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if !auth.Authenticate(ctx) {
		t.Fatal("JWT without iat claim should be accepted")
	}
}

func TestAuthenticatorJWTCorruptHeader(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: "secret"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	token := "not-valid-base64!!!" + ".payload.signature"
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if auth.Authenticate(ctx) {
		t.Fatal("JWT with corrupt header should be rejected")
	}
}

func TestAuthenticatorJWTWrongSignature(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: "real-secret"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	token := signTestJWT(t, "wrong-secret", time.Now().Add(time.Hour).Unix())
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if auth.Authenticate(ctx) {
		t.Fatal("JWT signed with wrong secret should be rejected")
	}
}

func TestAuthenticatorJWTWrongTyp(t *testing.T) {
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: "secret"})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	token := signCustomJWT(t, "secret", map[string]string{"alg": "HS256", "typ": "none"}, map[string]int64{"exp": time.Now().Add(time.Hour).Unix()})
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	if auth.Authenticate(ctx) {
		t.Fatal("JWT with typ:none should be rejected")
	}
}

// ---------------------------------------------------------------------------
// 9. Request ID
// ---------------------------------------------------------------------------

func TestGetRequestID(t *testing.T) {
	tests := []struct {
		name string
		md   metadata.MD
		want string
	}{
		{name: "valid", md: metadata.Pairs("x-request-id", "abc-123"), want: "abc-123"},
		{name: "missing", md: metadata.Pairs("other", "val"), want: ""},
		{name: "no metadata", md: nil, want: ""},
		{name: "too long", md: metadata.Pairs("x-request-id", string(make([]byte, 129))), want: ""},
		{name: "control chars", md: metadata.Pairs("x-request-id", "ab\x00cd"), want: ""},
		{name: "non utf-8", md: metadata.Pairs("x-request-id", "ab\xffcd"), want: ""},
		{name: "whitespace only", md: metadata.Pairs("x-request-id", "   "), want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.md != nil {
				ctx = metadata.NewIncomingContext(ctx, tt.md)
			}
			if got := getRequestID(ctx); got != tt.want {
				t.Fatalf("getRequestID() = %q, want %q", got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 10. Helper functions
// ---------------------------------------------------------------------------

func TestClientErrToString(t *testing.T) {
	if got := clientErrToString(nil); got != "" {
		t.Fatalf("clientErrToString(nil) = %q, want empty", got)
	}
	err := errors.New("some error")
	if got := clientErrToString(err); got != "some error" {
		t.Fatalf("clientErrToString(err) = %q, want 'some error'", got)
	}
}

func TestClientGetErrorCode(t *testing.T) {
	if got := clientGetErrorCode(nil); got != "OK" {
		t.Fatalf("clientGetErrorCode(nil) = %q, want OK", got)
	}
	err := status.Error(codes.PermissionDenied, "denied")
	if got := clientGetErrorCode(err); got != "PermissionDenied" {
		t.Fatalf("clientGetErrorCode(status err) = %q, want PermissionDenied", got)
	}
	err2 := errors.New("random error")
	if got := clientGetErrorCode(err2); got != "unknown" {
		t.Fatalf("clientGetErrorCode(non-status err) = %q, want unknown", got)
	}
}

// ---------------------------------------------------------------------------
// 11. Fuzz
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 12. Per-client rate limiter
// ---------------------------------------------------------------------------

func TestNewPerClientRateLimiterDefaults(t *testing.T) {
	t.Parallel()
	_, _, limiter := NewPerClientRateLimitInterceptors(0, 0, 0)
	defer limiter.Stop()

	if limiter.rate != 1 {
		t.Fatalf("expected default rate 1, got %v", limiter.rate)
	}
	if limiter.burst != 1 {
		t.Fatalf("expected default burst 1, got %d", limiter.burst)
	}
}

func TestPerClientRateLimiterAllowDeny(t *testing.T) {
	t.Parallel()
	limiter := NewPerClientRateLimiter(1, 1, 0)
	defer limiter.Stop()

	if !limiter.Allow("client-a") {
		t.Fatal("first request should be allowed")
	}
	if limiter.Allow("client-a") {
		t.Fatal("second request should be denied")
	}
}

func TestPerClientRateLimiterSeparateBuckets(t *testing.T) {
	t.Parallel()
	limiter := NewPerClientRateLimiter(1, 1, 0)
	defer limiter.Stop()

	if !limiter.Allow("client-a") {
		t.Fatal("client-a first request should be allowed")
	}
	if limiter.Allow("client-a") {
		t.Fatal("client-a second request should be denied")
	}
	if !limiter.Allow("client-b") {
		t.Fatal("client-b first request should be allowed (separate bucket)")
	}
}

func TestPerClientRateLimiterEmptyID(t *testing.T) {
	t.Parallel()
	limiter := NewPerClientRateLimiter(10, 5, 0)
	defer limiter.Stop()

	// Empty clientID creates a fresh limiter each time (no caching), so it
	// should always be allowed (new bucket has burst tokens).
	for range 5 {
		if !limiter.Allow("") {
			t.Fatal("empty clientID should always get a fresh bucket")
		}
	}
}

func TestPerClientRateLimiterCleanup(t *testing.T) {
	t.Parallel()
	limiter := NewPerClientRateLimiter(100, 100, 50*time.Millisecond)
	defer limiter.Stop()

	limiter.Allow("client-a")
	limiter.Allow("client-b")

	limiter.mu.RLock()
	initCount := len(limiter.limiters)
	limiter.mu.RUnlock()
	if initCount != 2 {
		t.Fatalf("expected 2 limiters, got %d", initCount)
	}

	time.Sleep(200 * time.Millisecond)

	limiter.mu.RLock()
	count := len(limiter.limiters)
	limiter.mu.RUnlock()
	if count != 0 {
		t.Fatalf("expected 0 limiters after cleanup, got %d", count)
	}
}

func TestPerClientRateLimiterStopNoPanic(t *testing.T) {
	t.Parallel()
	limiter := NewPerClientRateLimiter(10, 10, 0)
	limiter.Stop()
	limiter.Stop()
}

func TestPerClientRateLimitInterceptorsUnary(t *testing.T) {
	t.Parallel()
	unary, _, limiter := NewPerClientRateLimitInterceptors(1, 1, 0)
	defer limiter.Stop()

	info := &grpc.UnaryServerInfo{FullMethod: "/example.v1.UserService/GetUser"}
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }

	// Client with auth context
	ctx := withClientID(context.Background(), "test-client")

	if _, err := unary(ctx, nil, info, handler); err != nil {
		t.Fatalf("first request rejected: %v", err)
	}
	if _, err := unary(ctx, nil, info, handler); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second request code = %v, want ResourceExhausted", status.Code(err))
	}
}

func TestPerClientRateLimitInterceptorsNoClientID(t *testing.T) {
	t.Parallel()
	unary, _, limiter := NewPerClientRateLimitInterceptors(10, 5, 0)
	defer limiter.Stop()

	info := &grpc.UnaryServerInfo{FullMethod: "/example.v1.UserService/GetUser"}
	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }

	if _, err := unary(context.Background(), nil, info, handler); err != nil {
		t.Fatalf("request without client ID: %v", err)
	}
}

func TestPerClientRateLimitInterceptorsStream(t *testing.T) {
	t.Parallel()
	_, stream, limiter := NewPerClientRateLimitInterceptors(1, 1, 0)
	defer limiter.Stop()

	info := &grpc.StreamServerInfo{FullMethod: "/example.v1.UserService/GetUser"}
	handler := func(srv any, ss grpc.ServerStream) error { return nil }

	ctx := withClientID(context.Background(), "stream-client")
	s := &fakeServerStream{ctx: ctx}

	if err := stream(nil, s, info, handler); err != nil {
		t.Fatalf("first stream request rejected: %v", err)
	}
	if err := stream(nil, s, info, handler); status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second stream request code = %v, want ResourceExhausted", status.Code(err))
	}
}

func TestPerClientRateLimiterConcurrent(t *testing.T) {
	limiter := NewPerClientRateLimiter(100, 100, 0)
	defer limiter.Stop()

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limiter.Allow("shared")
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------------
// 13. Request ID interceptor
// ---------------------------------------------------------------------------

func TestRequestIDFromContextMissing(t *testing.T) {
	t.Parallel()
	if got := RequestIDFromContext(context.Background()); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestUnaryRequestIDInterceptorGenerates(t *testing.T) {
	t.Parallel()
	var capturedRID string
	handler := func(ctx context.Context, req any) (any, error) {
		rid := RequestIDFromContext(ctx)
		if rid == "" {
			t.Fatal("expected non-empty request ID")
		}
		if len(rid) != 32 {
			t.Fatalf("expected 32-char hex, got %q (len=%d)", rid, len(rid))
		}
		capturedRID = rid
		// Should also be in outgoing metadata from the handler's context
		md, ok := metadata.FromOutgoingContext(ctx)
		if !ok || len(md["x-request-id"]) == 0 || md["x-request-id"][0] != rid {
			t.Fatalf("x-request-id not in outgoing metadata")
		}
		return rid, nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/test/ReqID"}
	_, err := UnaryRequestIDInterceptor(context.Background(), nil, info, handler)
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
	if capturedRID == "" {
		t.Fatal("handler was not called")
	}
}

func TestUnaryRequestIDInterceptorPropagates(t *testing.T) {
	t.Parallel()
	handler := func(ctx context.Context, req any) (any, error) {
		rid := RequestIDFromContext(ctx)
		if rid != "incoming-req-42" {
			t.Fatalf("expected 'incoming-req-42', got %q", rid)
		}
		return rid, nil
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/test/ReqID"}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-id", "incoming-req-42"))
	resp, err := UnaryRequestIDInterceptor(ctx, nil, info, handler)
	if err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
	if resp.(string) != "incoming-req-42" {
		t.Fatalf("unexpected request ID: %q", resp)
	}
}

func TestStreamRequestIDInterceptorGenerates(t *testing.T) {
	t.Parallel()
	handler := func(srv any, ss grpc.ServerStream) error {
		rid := RequestIDFromContext(ss.Context())
		if rid == "" {
			t.Fatal("expected non-empty request ID")
		}
		if len(rid) != 32 {
			t.Fatalf("expected 32-char hex, got %q (len=%d)", rid, len(rid))
		}
		return nil
	}
	info := &grpc.StreamServerInfo{FullMethod: "/test/StreamReqID"}
	s := &fakeServerStream{ctx: context.Background()}

	if err := StreamRequestIDInterceptor(nil, s, info, handler); err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

func TestStreamRequestIDInterceptorPropagates(t *testing.T) {
	t.Parallel()
	handler := func(srv any, ss grpc.ServerStream) error {
		rid := RequestIDFromContext(ss.Context())
		if rid != "stream-req-99" {
			t.Fatalf("expected 'stream-req-99', got %q", rid)
		}
		return nil
	}
	info := &grpc.StreamServerInfo{FullMethod: "/test/StreamReqID"}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("x-request-id", "stream-req-99"))
	s := &fakeServerStream{ctx: ctx}

	if err := StreamRequestIDInterceptor(nil, s, info, handler); err != nil {
		t.Fatalf("interceptor error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// 14. Fuzz
// ---------------------------------------------------------------------------

func FuzzValidJWT(f *testing.F) {
	secret := "fuzz-secret"
	auth, err := NewAuthenticator(config.AuthConfig{JWTSecret: secret})
	if err != nil {
		f.Fatalf("NewAuthenticator: %v", err)
	}

	fuzzEncode := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	h := fuzzEncode(map[string]string{"alg": "HS256", "typ": "JWT"})
	p := fuzzEncode(map[string]int64{"exp": time.Now().Add(time.Hour).Unix()})
	input := h + "." + p
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	valid := fmt.Sprintf("%s.%s", input, base64.RawURLEncoding.EncodeToString(mac.Sum(nil)))
	f.Add(valid)
	f.Add("invalid.token.here")
	f.Add("a.b.c")
	f.Add("...")
	f.Add("")

	f.Fuzz(func(t *testing.T, token string) {
		ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
		auth.Authenticate(ctx)
	})
}
