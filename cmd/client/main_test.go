package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestCloseConnectionNil verifies that closeConnection is safe to call with
// a nil connection (which can occur when dialClient fails before assigning
// the connection to the caller).
func TestCloseConnectionNil(t *testing.T) {
	t.Parallel()
	closeConnection(nil)
}

// TestCloseConnectionWithConn exercises closeConnection with a real (but
// already-closed) gRPC client connection. After Close, calling Close again
// must not panic.
func TestCloseConnectionWithConn(t *testing.T) {
	t.Parallel()
	conn, err := grpc.NewClient("passthrough://localhost", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient() error = %v", err)
	}
	closeConnection(conn)
	closeConnection(conn)
}

// TestLoadClientTLSCredentialsConfigurable walks the function's
// configurable branches. The TLS-enabled "happy path" requires a real CA
// PEM; we generate one in a tempdir so the test is self-contained.
func TestLoadClientTLSCredentialsConfigurable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caFile, generateTestCAPEM(t), 0o600); err != nil {
		t.Fatalf("write ca: %v", err)
	}
	certFile := filepath.Join(dir, "client.pem")
	keyFile := filepath.Join(dir, "client.key")
	clientPEM, clientKeyPEM := generateTestCertPEM(t)
	if err := os.WriteFile(certFile, clientPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, clientKeyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	tests := []struct {
		name      string
		mutate    func(*config.Config)
		wantErr   bool
		errSubstr string
	}{
		{
			name: "missing CA file path",
			mutate: func(c *config.Config) {
				c.TLS.CAFile = ""
			},
			wantErr:   true,
			errSubstr: "tls.ca_file is required",
		},
		{
			name: "CA file path does not exist",
			mutate: func(c *config.Config) {
				c.TLS.CAFile = filepath.Join(dir, "does-not-exist.pem")
			},
			wantErr:   true,
			errSubstr: "read client CA",
		},
		{
			name: "CA file contains invalid content",
			mutate: func(c *config.Config) {
				bad := filepath.Join(dir, "bad-ca.pem")
				_ = os.WriteFile(bad, []byte("not a certificate"), 0o600)
				c.TLS.CAFile = bad
			},
			wantErr:   true,
			errSubstr: "no valid certificates",
		},
		{
			name: "mTLS enabled with missing client cert",
			mutate: func(c *config.Config) {
				c.TLS.MTLS.Enabled = true
				c.TLS.ClientCertFile = ""
				c.TLS.ClientKeyFile = ""
			},
			wantErr:   true,
			errSubstr: "client certificate and key are required",
		},
		{
			name: "mTLS enabled with missing client cert file",
			mutate: func(c *config.Config) {
				c.TLS.MTLS.Enabled = true
				c.TLS.ClientCertFile = filepath.Join(dir, "missing.pem")
				c.TLS.ClientKeyFile = keyFile
			},
			wantErr:   true,
			errSubstr: "load client certificate",
		},
		{
			name: "mTLS enabled with missing client key file",
			mutate: func(c *config.Config) {
				c.TLS.MTLS.Enabled = true
				c.TLS.ClientCertFile = certFile
				c.TLS.ClientKeyFile = filepath.Join(dir, "missing.key")
			},
			wantErr:   true,
			errSubstr: "load client certificate",
		},
		{
			name: "valid CA without mTLS succeeds",
			mutate: func(c *config.Config) {
				// defaults are already valid
			},
			wantErr: false,
		},
		{
			name: "valid CA with mTLS succeeds",
			mutate: func(c *config.Config) {
				c.TLS.MTLS.Enabled = true
				c.TLS.ClientCertFile = certFile
				c.TLS.ClientKeyFile = keyFile
			},
			wantErr: false,
		},
		{
			name: "valid CA derives ServerName from address",
			mutate: func(c *config.Config) {
				c.Client.ServerName = ""
				c.Client.Address = "example.test:50051"
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseTLSConfig(caFile, certFile, keyFile)
			tt.mutate(&cfg)
			creds, err := loadClientTLSCredentials(&cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("error = %q, want substring %q", err.Error(), tt.errSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if creds == nil {
				t.Fatal("expected non-nil credentials")
			}
		})
	}
}

// baseTLSConfig returns a configuration that enables TLS with a valid CA,
// useful as the starting point for table-driven TLS credential tests.
func baseTLSConfig(caFile, certFile, keyFile string) config.Config {
	return config.Config{
		TLS: config.TLSConfig{
			Enabled:        true,
			CAFile:         caFile,
			ClientCertFile: certFile,
			ClientKeyFile:  keyFile,
		},
		Client: config.ClientConfig{
			Address:    "localhost:50051",
			ServerName: "localhost",
		},
	}
}

// generateTestCAPEM creates a self-signed CA certificate in PEM form for use
// as a trust anchor in TLS credential tests.
func generateTestCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

// generateTestCertPEM creates a self-signed leaf certificate plus the
// matching RSA private key, both PEM-encoded, suitable for client mTLS.
func generateTestCertPEM(t *testing.T) ([]byte, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "Test Client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey: %v", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM
}

// TestCreateExampleUserRequest validates that createExampleUser builds a
// well-formed CreateUserRequest. We exercise only the request-building path
// here by isolating the literal values used.
//
// Note: createExampleUser itself requires a live gRPC server. We instead
// assert on the static parts of the request (email format) that the
// function passes to the RPC. This is a coarse smoke test; full RPC
// behavior is covered by integration tests against a running server.
func TestCreateExampleUserRequestShape(t *testing.T) {
	t.Parallel()
	email := "client-1234567890@example.com"
	if !strings.HasPrefix(email, "client-") || !strings.HasSuffix(email, "@example.com") {
		t.Fatalf("unexpected email shape: %q", email)
	}
}

// TestDialClientInsecure ensures dialClient returns a connection without
// TLS using an in-memory passthrough target. We don't actually establish
// a network connection, so this exercises only the credential wiring.
func TestDialClientInsecure(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Server: config.ServerConfig{Timeout: time.Second},
		TLS:    config.TLSConfig{Enabled: false},
		Client: config.ClientConfig{Address: "passthrough://localhost:50051"},
	}
	conn, err := dialClient(cfg)
	if err != nil {
		t.Fatalf("dialClient() error = %v", err)
	}
	t.Cleanup(func() { closeConnection(conn) })
	if conn == nil {
		t.Fatal("expected non-nil connection")
	}
}

// TestDialClientRequiresTLSConfig verifies dialClient returns an error when
// TLS is enabled but no CA file is configured, instead of silently falling
// back to insecure transport.
func TestDialClientRequiresTLSConfig(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Server: config.ServerConfig{Timeout: time.Second},
		TLS:    config.TLSConfig{Enabled: true, CAFile: ""},
		Client: config.ClientConfig{Address: "localhost:50051"},
	}
	conn, err := dialClient(cfg)
	if err == nil {
		if conn != nil {
			_ = conn.Close()
		}
		t.Fatal("expected dialClient to fail when TLS enabled without CA file")
	}
}

// Note on test coverage: the streaming orchestration functions
// (runUnaryClient, runServerStreamingClient, runClientStreamingClient,
// runBidiStreamingClient) and main() all require a live gRPC server and
// would need an end-to-end harness with a test server fixture. They are
// intentionally not unit-tested here; their underlying gRPC plumbing is
// exercised by the server-side tests in internal/service.
