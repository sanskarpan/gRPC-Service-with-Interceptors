package interceptors

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func unaryInfo(method string) *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: "/example.v1.UserService/" + method}
}

func TestConcurrencyLimitShedsOverflow(t *testing.T) {
	intc := NewConcurrencyLimitInterceptor(1)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	go func() {
		_, _ = intc(context.Background(), nil, unaryInfo("CreateUser"), func(context.Context, interface{}) (interface{}, error) {
			close(started)
			<-release
			return "ok", nil
		})
		close(done)
	}()
	<-started // one slot held

	_, err := intc(context.Background(), nil, unaryInfo("CreateUser"), okUnary)
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("overflow request must be shed with ResourceExhausted, got %v", err)
	}

	close(release)
	<-done
	// Slot released — a new request succeeds.
	if _, err := intc(context.Background(), nil, unaryInfo("CreateUser"), okUnary); err != nil {
		t.Fatalf("request after release should succeed: %v", err)
	}
}

func TestConcurrencyLimitDisabled(t *testing.T) {
	intc := NewConcurrencyLimitInterceptor(0)
	if _, err := intc(context.Background(), nil, unaryInfo("CreateUser"), okUnary); err != nil {
		t.Fatalf("disabled limiter should pass through: %v", err)
	}
}

func TestConcurrencyLimitHealthExempt(t *testing.T) {
	intc := NewConcurrencyLimitInterceptor(1)
	// Hold the only slot, then a health check must still pass (exempt).
	release := make(chan struct{})
	started := make(chan struct{})
	go func() {
		_, _ = intc(context.Background(), nil, unaryInfo("CreateUser"), func(context.Context, interface{}) (interface{}, error) {
			close(started)
			<-release
			return "ok", nil
		})
	}()
	<-started
	info := &grpc.UnaryServerInfo{FullMethod: "/grpc.health.v1.Health/Check"}
	if _, err := intc(context.Background(), nil, info, okUnary); err != nil {
		t.Fatalf("health check must be exempt from load shedding: %v", err)
	}
	close(release)
}
