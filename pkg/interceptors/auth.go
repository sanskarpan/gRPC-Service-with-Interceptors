package interceptors

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/example/grpc-service/pkg/config"
	"github.com/example/grpc-service/pkg/logging"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type clientIDKey struct{}

func withClientID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, clientIDKey{}, id)
}

func extractClientID(ctx context.Context) string {
	if v := ctx.Value(clientIDKey{}); v != nil {
		return v.(string)
	}
	return ""
}

// Authenticator verifies API keys and compact HMAC-SHA256 JWTs at the gRPC boundary.
type Authenticator struct {
	jwtSecret []byte
	apiKeys   [][]byte
}

// NewAuthenticator builds a verifier from already validated configuration.
func NewAuthenticator(cfg config.AuthConfig) (*Authenticator, error) {
	if strings.TrimSpace(cfg.JWTSecret) == "" && len(cfg.APIKeys) == 0 {
		return nil, fmt.Errorf("no authentication credentials configured")
	}
	auth := &Authenticator{jwtSecret: []byte(cfg.JWTSecret)}
	for _, key := range cfg.APIKeys {
		if strings.TrimSpace(key) != "" {
			auth.apiKeys = append(auth.apiKeys, []byte(key))
		}
	}
	return auth, nil
}

// Authenticate validates the credential in ctx without exposing details.
func (a *Authenticator) Authenticate(ctx context.Context) bool {
	if a == nil {
		return false
	}
	_, ok := credentialFromContext(ctx)
	return ok && a.authenticateCredential(ctx)
}

func (a *Authenticator) authenticateCredential(ctx context.Context) bool {
	credential, _ := credentialFromContext(ctx)
	for _, key := range a.apiKeys {
		if hmac.Equal(key, []byte(credential)) {
			return true
		}
	}
	return a.validJWT(credential)
}

func (a *Authenticator) clientID(ctx context.Context) string {
	credential, ok := credentialFromContext(ctx)
	if !ok {
		return ""
	}
	for _, key := range a.apiKeys {
		if hmac.Equal(key, []byte(credential)) {
			return fmt.Sprintf("apikey:%x", sha256.Sum256([]byte(credential)))
		}
	}
	return a.jwtSubject(credential)
}

func (a *Authenticator) jwtSubject(token string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return ""
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return ""
	}
	raw, ok := claims["sub"]
	if !ok {
		return ""
	}
	var sub string
	if err := json.Unmarshal(raw, &sub); err != nil || sub == "" {
		return ""
	}
	return sub
}

func (a *Authenticator) validJWT(token string) bool {
	if len(a.jwtSecret) == 0 {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var header map[string]string
	if err := json.Unmarshal(headerBytes, &header); err != nil || header["alg"] != "HS256" || (header["typ"] != "" && header["typ"] != "JWT") {
		return false
	}
	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, a.jwtSecret)
	if _, err := mac.Write([]byte(signingInput)); err != nil {
		return false
	}
	expected, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(expected, mac.Sum(nil)) {
		return false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return false
	}
	exp, ok := unixClaim(claims["exp"])
	if !ok || !time.Now().Before(time.Unix(exp, 0)) {
		return false
	}
	if raw, ok := claims["nbf"]; ok {
		nbf, valid := unixClaim(raw)
		if !valid || time.Now().Before(time.Unix(nbf, 0)) {
			return false
		}
	}
	if raw, ok := claims["iat"]; ok {
		iat, valid := unixClaim(raw)
		if !valid {
			return false
		}
		if time.Now().Before(time.Unix(iat, 0).Add(-jwtClockSkew)) {
			return false
		}
	}
	return true
}

// jwtClockSkew tolerates small issuer/verifier clock differences on iat checks.
const jwtClockSkew = 60 * time.Second

func credentialFromContext(ctx context.Context) (string, bool) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return "", false
	}
	apiKeyValues := md.Get("x-api-key")
	authorizationValues := md.Get("authorization")
	if len(apiKeyValues) > 0 && len(authorizationValues) > 0 {
		logging.Logger().Warn().Msg("both x-api-key and authorization headers present; preferring authorization")
	}
	if len(authorizationValues) == 1 {
		fields := strings.Fields(authorizationValues[0])
		if len(fields) == 2 && strings.EqualFold(fields[0], "bearer") && fields[1] != "" {
			return fields[1], true
		}
	}
	if len(apiKeyValues) == 1 && strings.TrimSpace(apiKeyValues[0]) != "" {
		return strings.TrimSpace(apiKeyValues[0]), true
	}
	return "", false
}

// UnaryAuthInterceptor is a fail-closed compatibility interceptor.
func UnaryAuthInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	return UnaryAuthInterceptorWithAuthenticator(nil)(ctx, req, info, handler)
}

// UnaryAuthInterceptorWithAuthenticator enforces credentials on every service
// method except public health checks. The authenticated client ID is stored
// in the context for downstream interceptors.
func UnaryAuthInterceptorWithAuthenticator(auth *Authenticator) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if requiresAuth(info.FullMethod) {
			if auth == nil || !auth.authenticateCredential(ctx) {
				return nil, authenticationError()
			}
			ctx = withClientID(ctx, auth.clientID(ctx))
		}
		return handler(ctx, req)
	}
}

// StreamAuthInterceptor is a fail-closed compatibility interceptor.
func StreamAuthInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	return StreamAuthInterceptorWithAuthenticator(nil)(srv, ss, info, handler)
}

// StreamAuthInterceptorWithAuthenticator enforces credentials for streaming
// methods. The authenticated client ID is stored in the stream context.
func StreamAuthInterceptorWithAuthenticator(auth *Authenticator) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if requiresAuth(info.FullMethod) {
			if auth == nil || !auth.authenticateCredential(ss.Context()) {
				return authenticationError()
			}
			ctx := withClientID(ss.Context(), auth.clientID(ss.Context()))
			return handler(srv, &wrappedServerStream{ServerStream: ss, ctx: ctx})
		}
		return handler(srv, ss)
	}
}

func unixClaim(raw json.RawMessage) (int64, bool) {
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, false
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	return value, err == nil
}

func authenticationError() error {
	return status.Error(codes.Unauthenticated, "authentication required")
}
