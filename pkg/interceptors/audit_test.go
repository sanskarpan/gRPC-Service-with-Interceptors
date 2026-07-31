package interceptors

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsMutation(t *testing.T) {
	cases := map[string]bool{
		"/example.v1.UserService/CreateUser":         true,
		"/example.v1.UserService/UpdateUser":         true,
		"/example.v1.UserService/DeleteUser":         true,
		"/example.v1.UserService/CollectUserMetrics": true, // client-streaming ingest
		"/example.v1.UserService/GetUser":            false,
		"/example.v1.UserService/ListUsers":          false,
		"/example.v1.UserService/StreamUserEvents":   false,
		"/example.v1.UserService/ChatStream":         false,
	}
	for method, want := range cases {
		if got := isMutation(method); got != want {
			t.Errorf("isMutation(%q) = %v, want %v", method, got, want)
		}
	}
}

func TestUnaryAuditInterceptorLogsMutations(t *testing.T) {
	out := captureLogOutput(t, func() {
		ctx := context.WithValue(context.Background(), clientIDKey{}, "apikey:abc")
		_, _ = UnaryAuditInterceptor(ctx, nil,
			&grpc.UnaryServerInfo{FullMethod: "/example.v1.UserService/CreateUser"},
			func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	})
	for _, want := range []string{`"audit":true`, `"event":"mutation"`, `CreateUser`, `"client_id":"apikey:abc"`, `"code":"OK"`} {
		if !strings.Contains(out, want) {
			t.Errorf("audit log missing %q\ngot: %s", want, out)
		}
	}
}

func TestUnaryAuditInterceptorSkipsReads(t *testing.T) {
	out := captureLogOutput(t, func() {
		_, _ = UnaryAuditInterceptor(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: "/example.v1.UserService/GetUser"},
			func(context.Context, interface{}) (interface{}, error) { return nil, nil })
	})
	if strings.Contains(out, "mutation") {
		t.Errorf("read RPC should not be audited as a mutation, got: %s", out)
	}
}

func TestUnaryAuditInterceptorRecordsErrorCode(t *testing.T) {
	out := captureLogOutput(t, func() {
		_, _ = UnaryAuditInterceptor(context.Background(), nil,
			&grpc.UnaryServerInfo{FullMethod: "/example.v1.UserService/DeleteUser"},
			func(context.Context, interface{}) (interface{}, error) {
				return nil, status.Error(codes.NotFound, "nope")
			})
	})
	if !strings.Contains(out, `"code":"NotFound"`) {
		t.Errorf("expected code NotFound in audit, got: %s", out)
	}
}

func TestStreamAuditInterceptorAuditsMetricsIngest(t *testing.T) {
	out := captureLogOutput(t, func() {
		ctx := context.WithValue(context.Background(), clientIDKey{}, "apikey:xyz")
		_ = StreamAuditInterceptor(nil, &fakeServerStream{ctx: ctx},
			&grpc.StreamServerInfo{FullMethod: "/example.v1.UserService/CollectUserMetrics"},
			func(interface{}, grpc.ServerStream) error { return nil })
	})
	for _, want := range []string{`"audit":true`, `"event":"mutation"`, `CollectUserMetrics`, `apikey:xyz`} {
		if !strings.Contains(out, want) {
			t.Errorf("stream audit log missing %q\ngot: %s", want, out)
		}
	}
}
