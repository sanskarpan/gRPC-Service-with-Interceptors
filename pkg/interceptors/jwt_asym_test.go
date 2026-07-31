package interceptors

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/config"
)

func b64(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}

func signingInput(alg string, exp int64) string {
	return b64(map[string]string{"alg": alg, "typ": "JWT"}) + "." + b64(map[string]int64{"exp": exp})
}

func pkixPEM(t *testing.T, pub any) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("marshal pub: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func TestJWTRS256(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	auth, err := NewAuthenticator(config.AuthConfig{JWTPublicKey: pkixPEM(t, &key.PublicKey)})
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	si := signingInput("RS256", time.Now().Add(time.Hour).Unix())
	h := sha256.Sum256([]byte(si))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	token := si + "." + base64.RawURLEncoding.EncodeToString(sig)
	if !auth.validJWT(token) {
		t.Fatal("valid RS256 token should be accepted")
	}
	// A token signed with a different key must be rejected.
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	badSig, _ := rsa.SignPKCS1v15(rand.Reader, other, crypto.SHA256, h[:])
	if auth.validJWT(si + "." + base64.RawURLEncoding.EncodeToString(badSig)) {
		t.Fatal("RS256 token signed by the wrong key must be rejected")
	}
}

func TestJWTES256(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	auth, _ := NewAuthenticator(config.AuthConfig{JWTPublicKey: pkixPEM(t, &key.PublicKey)})
	si := signingInput("ES256", time.Now().Add(time.Hour).Unix())
	h := sha256.Sum256([]byte(si))
	r, s, _ := ecdsa.Sign(rand.Reader, key, h[:])
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	token := si + "." + base64.RawURLEncoding.EncodeToString(sig)
	if !auth.validJWT(token) {
		t.Fatal("valid ES256 token should be accepted")
	}
}

// TestJWTAlgConfusionRejected is the critical security check: with only an RSA
// public key configured, an attacker crafts an HS256 token using the PEM public
// key bytes as the HMAC secret. It MUST be rejected — HS256 requires a shared
// secret, which is not configured.
func TestJWTAlgConfusionRejected(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	pubPEM := pkixPEM(t, &key.PublicKey)
	auth, _ := NewAuthenticator(config.AuthConfig{JWTPublicKey: pubPEM})

	si := signingInput("HS256", time.Now().Add(time.Hour).Unix())
	mac := hmac.New(sha256.New, []byte(pubPEM)) // attacker uses the public key as the "secret"
	mac.Write([]byte(si))
	forged := si + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if auth.validJWT(forged) {
		t.Fatal("SECURITY: HS256 alg-confusion token must be rejected when only an asymmetric key is configured")
	}
}

func TestJWTRS256RejectedWithoutPublicKey(t *testing.T) {
	// Only an HS256 secret configured — an RS256 token must be rejected.
	auth, _ := NewAuthenticator(config.AuthConfig{JWTSecret: "this-is-a-sufficiently-long-jwt-secret-0123456789"})
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	si := signingInput("RS256", time.Now().Add(time.Hour).Unix())
	h := sha256.Sum256([]byte(si))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if auth.validJWT(si + "." + base64.RawURLEncoding.EncodeToString(sig)) {
		t.Fatal("RS256 token must be rejected when no public key is configured")
	}
}

func TestNewAuthenticatorRejectsWeakRSAKey(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 1024) // below the 2048-bit floor
	if _, err := NewAuthenticator(config.AuthConfig{JWTPublicKey: pkixPEM(t, &key.PublicKey)}); err == nil {
		t.Fatal("expected a weak (1024-bit) RSA key to be rejected")
	}
}

func TestNewAuthenticatorRejectsNonP256Curve(t *testing.T) {
	key, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if _, err := NewAuthenticator(config.AuthConfig{JWTPublicKey: pkixPEM(t, &key.PublicKey)}); err == nil {
		t.Fatal("expected a non-P256 EC key to be rejected at startup")
	}
}
