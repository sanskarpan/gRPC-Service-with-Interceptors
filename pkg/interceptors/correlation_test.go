package interceptors

import (
	"context"
	"strings"
	"testing"

	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
)

func spanCtx(t *testing.T) (context.Context, string) {
	t.Helper()
	tid, err := oteltrace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	sid, err := oteltrace.SpanIDFromHex("0123456789abcdef")
	if err != nil {
		t.Fatalf("span id: %v", err)
	}
	sc := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    tid,
		SpanID:     sid,
		TraceFlags: oteltrace.FlagsSampled,
	})
	return oteltrace.ContextWithSpanContext(context.Background(), sc), tid.String()
}

func TestUnaryLoggingIncludesTraceID(t *testing.T) {
	ctx, wantTrace := spanCtx(t)
	out := captureLogOutput(t, func() {
		_, _ = UnaryLoggingInterceptor(ctx, nil,
			&grpc.UnaryServerInfo{FullMethod: "/svc/M"},
			func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	})
	if !strings.Contains(out, wantTrace) {
		t.Fatalf("expected trace_id %q in logs, got:\n%s", wantTrace, out)
	}
	if !strings.Contains(out, `"trace_id"`) || !strings.Contains(out, `"span_id"`) {
		t.Fatalf("expected trace_id/span_id fields in logs, got:\n%s", out)
	}
}

func TestStreamLoggingIncludesRequestID(t *testing.T) {
	ctx, _ := spanCtx(t)
	ctx = context.WithValue(ctx, requestIDKey{}, "req-xyz")
	out := captureLogOutput(t, func() {
		_ = StreamLoggingInterceptor(nil, &fakeServerStream{ctx: ctx},
			&grpc.StreamServerInfo{FullMethod: "/svc/S"},
			func(interface{}, grpc.ServerStream) error { return nil })
	})
	if !strings.Contains(out, "req-xyz") {
		t.Fatalf("expected request_id in stream logs, got:\n%s", out)
	}
	if !strings.Contains(out, `"trace_id"`) {
		t.Fatalf("expected trace_id in stream logs, got:\n%s", out)
	}
}

func TestTraceIDFromContext(t *testing.T) {
	if got := traceIDFromContext(context.Background()); got != "" {
		t.Fatalf("expected empty trace id without span, got %q", got)
	}
	ctx, want := spanCtx(t)
	if got := traceIDFromContext(ctx); got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}
