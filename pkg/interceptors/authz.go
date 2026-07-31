package interceptors

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UnaryAuthorizationInterceptor enforces per-method scope requirements after
// authentication. Semantics:
//   - Public methods (health checks, which skip auth) are never authorized —
//     they early-out so k8s probes are never blocked, even with defaultDeny.
//   - A method absent from the policy is allowed (authentication still applied)
//     unless defaultDeny is set, in which case it is denied.
//   - Required scopes are any-of: the caller needs at least one listed scope.
//
// Place it after the authentication and per-client rate-limit interceptors so
// claims are present and denied-but-authenticated spam counts against the
// caller's per-client quota.
func UnaryAuthorizationInterceptor(policy map[string][]string, defaultDeny bool) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if !requiresAuth(info.FullMethod) {
			return handler(ctx, req)
		}
		if err := authorize(ctx, info.FullMethod, policy, defaultDeny); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// StreamAuthorizationInterceptor is the streaming counterpart.
func StreamAuthorizationInterceptor(policy map[string][]string, defaultDeny bool) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if !requiresAuth(info.FullMethod) {
			return handler(srv, ss)
		}
		if err := authorize(ss.Context(), info.FullMethod, policy, defaultDeny); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}

func authorize(ctx context.Context, method string, policy map[string][]string, defaultDeny bool) error {
	required, listed := policy[method]
	if !listed {
		if defaultDeny {
			auditAuthDecision(ctx, "authz", method, "deny", extractClientID(ctx))
			return status.Error(codes.PermissionDenied, "no authorization policy for method")
		}
		return nil
	}
	if len(required) == 0 {
		return nil
	}
	if c := ClaimsFromContext(ctx); c != nil && hasAnyScope(c.Scopes, required) {
		return nil
	}
	auditAuthDecision(ctx, "authz", method, "deny", extractClientID(ctx))
	return status.Error(codes.PermissionDenied, "insufficient scope")
}

func hasAnyScope(have, required []string) bool {
	set := make(map[string]struct{}, len(have))
	for _, s := range have {
		set[s] = struct{}{}
	}
	for _, r := range required {
		if _, ok := set[r]; ok {
			return true
		}
	}
	return false
}
