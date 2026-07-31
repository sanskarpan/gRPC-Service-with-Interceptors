package interceptors

import (
	"context"
	"strings"
	"time"

	"github.com/example/grpc-service/pkg/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

// auditedMethods are RPCs that change or ingest server state and must be in the
// audit trail even though their name does not start with a Create/Update/Delete
// verb (e.g. the client-streaming CollectUserMetrics ingest).
var auditedMethods = map[string]bool{
	"CollectUserMetrics": true,
}

// isMutation reports whether a method changes server state and therefore belongs
// in the audit trail. It recognizes the conventional write verbs
// (Create/Update/Delete) plus an explicit allow-list for writes that do not
// follow that naming.
func isMutation(fullMethod string) bool {
	name := fullMethod[strings.LastIndex(fullMethod, "/")+1:]
	if auditedMethods[name] {
		return true
	}
	return strings.HasPrefix(name, "Create") ||
		strings.HasPrefix(name, "Update") ||
		strings.HasPrefix(name, "Delete")
}

func remoteAddr(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		return p.Addr.String()
	}
	return ""
}

// auditMutation records a state-changing RPC and its outcome. Records are marked
// with audit=true so an operator or SIEM can select the audit stream with a
// single field filter. No request payload is logged.
func auditMutation(ctx context.Context, method, code string, dur time.Duration) {
	logger := logging.With().
		Bool("audit", true).
		Str("event", "mutation").
		Str("method", method).
		Str("client_id", extractClientID(ctx)).
		Str("request_id", getRequestID(ctx)).
		Str("code", code).
		Dur("duration", dur).
		Logger()
	logger.Info().Msg("audit: mutation")
}

// auditAuthDecision records an authentication decision. Denials are the primary
// security signal; allows give a full who-accessed-what trail.
func auditAuthDecision(ctx context.Context, method, decision, clientID string) {
	logger := logging.With().
		Bool("audit", true).
		Str("event", "authn").
		Str("decision", decision).
		Str("method", method).
		Str("client_id", clientID).
		Str("request_id", getRequestID(ctx)).
		Str("remote_addr", remoteAddr(ctx)).
		Logger()
	logger.Info().Msg("audit: authn")
}

// UnaryAuditInterceptor writes an audit record for every mutating unary RPC,
// including its terminal status code. Place it after the auth interceptor so the
// authenticated client id is available.
func UnaryAuditInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()
	resp, err := handler(ctx, req)
	if isMutation(info.FullMethod) {
		auditMutation(ctx, info.FullMethod, getErrorCode(err), time.Since(start))
	}
	return resp, err
}

// StreamAuditInterceptor writes an audit record for every mutating streaming
// RPC (e.g. the CollectUserMetrics ingest), so streaming writes are not left out
// of the audit trail.
func StreamAuditInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	start := time.Now()
	err := handler(srv, ss)
	if isMutation(info.FullMethod) {
		auditMutation(ss.Context(), info.FullMethod, getErrorCode(err), time.Since(start))
	}
	return err
}
