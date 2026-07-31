package interceptors

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
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

// Authenticator verifies API keys and compact JWTs (HS256 with a shared secret,
// or RS256/ES256 against a configured PEM public key) at the gRPC boundary.
// The verification path is selected by the token's alg AND the configured key
// type, so a token cannot use HS256 to trick an asymmetric public key into being
// treated as an HMAC secret (the classic algorithm-confusion attack).
type Authenticator struct {
	jwtSecret   []byte
	apiKeys     [][]byte
	jwtAudience string
	jwtIssuer   string
	jwtRSAKey   *rsa.PublicKey
	jwtECKey    *ecdsa.PublicKey
	auditAllows bool
}

// minRSABits is the smallest RSA modulus accepted as a JWT trust anchor. A key
// weaker than 2048 bits is a forgeable anchor and is rejected at startup.
const minRSABits = 2048

// NewAuthenticator builds a verifier from already validated configuration.
func NewAuthenticator(cfg config.AuthConfig) (*Authenticator, error) {
	if strings.TrimSpace(cfg.JWTSecret) == "" && len(cfg.APIKeys) == 0 && strings.TrimSpace(cfg.JWTPublicKey) == "" {
		return nil, fmt.Errorf("no authentication credentials configured")
	}
	auth := &Authenticator{
		jwtSecret:   []byte(cfg.JWTSecret),
		jwtAudience: cfg.JWTAudience,
		jwtIssuer:   cfg.JWTIssuer,
		auditAllows: cfg.AuditAllows,
	}
	for _, key := range cfg.APIKeys {
		if strings.TrimSpace(key) != "" {
			auth.apiKeys = append(auth.apiKeys, []byte(key))
		}
	}
	if strings.TrimSpace(cfg.JWTPublicKey) != "" {
		switch key := parsePublicKey(cfg.JWTPublicKey).(type) {
		case *rsa.PublicKey:
			if key.N.BitLen() < minRSABits {
				return nil, fmt.Errorf("auth.jwt_public_key RSA modulus must be at least %d bits", minRSABits)
			}
			auth.jwtRSAKey = key
		case *ecdsa.PublicKey:
			// verifySignature only supports ES256 (P-256); reject other curves
			// at startup rather than starting up broken (every token failing).
			if key.Curve != elliptic.P256() {
				return nil, fmt.Errorf("auth.jwt_public_key EC key must use the P-256 curve (ES256)")
			}
			auth.jwtECKey = key
		default:
			return nil, fmt.Errorf("auth.jwt_public_key must be a PEM-encoded RSA or EC (P-256) public key")
		}
	}
	return auth, nil
}

// parsePublicKey decodes a PEM public key (PKIX/SPKI, or a PKCS#1 RSA key) and
// returns the crypto.PublicKey, or nil if it cannot be parsed.
func parsePublicKey(pemKey string) crypto.PublicKey {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil
	}
	if pub, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		return pub
	}
	if pub, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return pub
	}
	return nil
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

// verifySignature validates the JWT signature for the given alg using ONLY the
// key material configured for that algorithm class. An unconfigured alg fails
// closed, which blocks algorithm-confusion attacks (e.g. an HS256 token signed
// with the RSA public key bytes).
func (a *Authenticator) verifySignature(alg, signingInput string, sig []byte) bool {
	digest := sha256.Sum256([]byte(signingInput))
	switch alg {
	case "HS256":
		if len(a.jwtSecret) == 0 {
			return false
		}
		mac := hmac.New(sha256.New, a.jwtSecret)
		_, _ = mac.Write([]byte(signingInput))
		return hmac.Equal(sig, mac.Sum(nil))
	case "RS256":
		if a.jwtRSAKey == nil {
			return false
		}
		return rsa.VerifyPKCS1v15(a.jwtRSAKey, crypto.SHA256, digest[:], sig) == nil
	case "ES256":
		if a.jwtECKey == nil || a.jwtECKey.Curve != elliptic.P256() || len(sig) != 64 {
			return false
		}
		r := new(big.Int).SetBytes(sig[:32])
		s := new(big.Int).SetBytes(sig[32:])
		return ecdsa.Verify(a.jwtECKey, digest[:], r, s)
	default:
		return false
	}
}

func (a *Authenticator) validJWT(token string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return false
	}
	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var header map[string]string
	if err := json.Unmarshal(headerBytes, &header); err != nil || (header["typ"] != "" && header["typ"] != "JWT") {
		return false
	}
	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	if !a.verifySignature(header["alg"], signingInput, sig) {
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
	if a.jwtIssuer != "" {
		iss, _ := stringClaim(claims["iss"])
		if iss != a.jwtIssuer {
			return false
		}
	}
	if a.jwtAudience != "" && !audienceMatches(claims["aud"], a.jwtAudience) {
		return false
	}
	return true
}

// stringClaim decodes a JSON string claim.
func stringClaim(raw json.RawMessage) (string, bool) {
	if raw == nil {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// audienceMatches reports whether the JWT "aud" claim (a string or an array of
// strings per RFC 7519) contains want.
func audienceMatches(raw json.RawMessage, want string) bool {
	if raw == nil {
		return false
	}
	if s, ok := stringClaim(raw); ok {
		return s == want
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil {
		return false
	}
	for _, a := range list {
		if a == want {
			return true
		}
	}
	return false
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
				auditAuthDecision(ctx, info.FullMethod, "deny", "")
				return nil, authenticationError()
			}
			clientID := auth.clientID(ctx)
			ctx = withClientID(ctx, clientID)
			if auth.auditAllows {
				auditAuthDecision(ctx, info.FullMethod, "allow", clientID)
			}
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
				auditAuthDecision(ss.Context(), info.FullMethod, "deny", "")
				return authenticationError()
			}
			clientID := auth.clientID(ss.Context())
			ctx := withClientID(ss.Context(), clientID)
			if auth.auditAllows {
				auditAuthDecision(ctx, info.FullMethod, "allow", clientID)
			}
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
