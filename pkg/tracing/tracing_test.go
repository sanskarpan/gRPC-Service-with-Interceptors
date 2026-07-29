package tracing

import (
	"context"
	"testing"

	"github.com/example/grpc-service/pkg/config"
)

func TestTracingInitDisabled(t *testing.T) {
	ctx := context.Background()
	shutdown, err := Init(ctx, config.TracingConfig{Enabled: false})
	if err != nil {
		t.Fatalf("Init with Enabled=false: %v", err)
	}
	if shutdown == nil {
		t.Fatal("shutdown function is nil")
	}
	if err := shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestTracingInitBadEndpoint(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Init(ctx, config.TracingConfig{
		Enabled:     true,
		Endpoint:    "http://0.0.0.0:1",
		ServiceName: "test",
	})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestTracingNewServerHandler(t *testing.T) {
	handler := NewServerHandler()
	if handler == nil {
		t.Fatal("NewServerHandler() returned nil")
	}
}

func TestTracingStart(t *testing.T) {
	ctx := context.Background()
	Init(ctx, config.TracingConfig{Enabled: false})
	_, span := Start(ctx, "test-span")
	if span == nil {
		t.Fatal("Start() returned nil span")
	}
}

func TestTracingAddEventNoSpan(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AddEvent panicked: %v", r)
		}
	}()
	AddEvent(context.Background(), "test-event")
}

func TestTracingAddEventWithSpan(t *testing.T) {
	ctx := context.Background()
	Init(ctx, config.TracingConfig{Enabled: false})
	ctx, _ = Start(ctx, "test-span")
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AddEvent panicked: %v", r)
		}
	}()
	AddEvent(ctx, "test-event")
}
