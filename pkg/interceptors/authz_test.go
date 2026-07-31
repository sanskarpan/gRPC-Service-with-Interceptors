package interceptors

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func okUnary(context.Context, interface{}) (interface{}, error) { return "ok", nil }

func testPolicy() map[string][]string {
	return map[string][]string{
		"/example.v1.UserService/GetUser":    {"users:read"},
		"/example.v1.UserService/DeleteUser": {"users:write"},
	}
}

func ctxScopes(scopes ...string) context.Context {
	return withClaims(context.Background(), &Claims{Subject: "u", Scopes: scopes})
}

func call(t *testing.T, policy map[string][]string, defaultDeny bool, method string, ctx context.Context) error {
	t.Helper()
	intc := UnaryAuthorizationInterceptor(policy, defaultDeny)
	_, err := intc(ctx, nil, &grpc.UnaryServerInfo{FullMethod: method}, okUnary)
	return err
}

func TestAuthzAllowsWithRequiredScope(t *testing.T) {
	if err := call(t, testPolicy(), false, "/example.v1.UserService/DeleteUser", ctxScopes("users:write")); err != nil {
		t.Fatalf("write scope should allow DeleteUser: %v", err)
	}
}

func TestAuthzDeniesMissingScope(t *testing.T) {
	err := call(t, testPolicy(), false, "/example.v1.UserService/DeleteUser", ctxScopes("users:read"))
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("read-only scope must be denied for DeleteUser, got %v", err)
	}
}

func TestAuthzUnlistedAllowedByDefault(t *testing.T) {
	if err := call(t, testPolicy(), false, "/example.v1.UserService/ListUsers", ctxScopes()); err != nil {
		t.Fatalf("unlisted method should be allowed (authn-only): %v", err)
	}
}

func TestAuthzUnlistedDeniedWithDefaultDeny(t *testing.T) {
	err := call(t, testPolicy(), true, "/example.v1.UserService/ListUsers", ctxScopes("users:read"))
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("default_deny must deny unlisted methods, got %v", err)
	}
}

func TestAuthzHealthExemptEvenWithDefaultDeny(t *testing.T) {
	// Health checks skip auth (no claims) and must never be blocked by authz,
	// even under default_deny — otherwise k8s probes fail.
	if err := call(t, testPolicy(), true, "/grpc.health.v1.Health/Check", context.Background()); err != nil {
		t.Fatalf("health check must be exempt from authz: %v", err)
	}
}
