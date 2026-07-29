package interceptors

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/config"
)

// signJWTClaims mints an HS256 JWT with arbitrary claims for testing.
func signJWTClaims(t *testing.T, secret string, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	input := enc(map[string]string{"alg": "HS256", "typ": "JWT"}) + "." + enc(claims)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(input))
	return input + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func TestJWTIssuerBinding(t *testing.T) {
	t.Parallel()
	const secret = "this-is-a-sufficiently-long-jwt-secret-0123456789"
	auth, err := NewAuthenticator(config.AuthConfig{
		JWTSecret: secret,
		JWTIssuer: "trusted-issuer",
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	exp := time.Now().Add(time.Hour).Unix()

	wrong := signJWTClaims(t, secret, map[string]any{"exp": exp, "iss": "attacker"})
	if auth.validJWT(wrong) {
		t.Fatal("token with wrong issuer must be rejected when issuer is configured")
	}

	right := signJWTClaims(t, secret, map[string]any{"exp": exp, "iss": "trusted-issuer"})
	if !auth.validJWT(right) {
		t.Fatal("token with the configured issuer must be accepted")
	}
}

func TestJWTAudienceBinding(t *testing.T) {
	t.Parallel()
	const secret = "this-is-a-sufficiently-long-jwt-secret-0123456789"
	auth, err := NewAuthenticator(config.AuthConfig{
		JWTSecret:   secret,
		JWTAudience: "my-api",
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	exp := time.Now().Add(time.Hour).Unix()

	wrong := signJWTClaims(t, secret, map[string]any{"exp": exp, "aud": "other-api"})
	if auth.validJWT(wrong) {
		t.Fatal("token with wrong audience must be rejected when audience is configured")
	}

	right := signJWTClaims(t, secret, map[string]any{"exp": exp, "aud": "my-api"})
	if !auth.validJWT(right) {
		t.Fatal("token with the configured audience must be accepted")
	}
}
