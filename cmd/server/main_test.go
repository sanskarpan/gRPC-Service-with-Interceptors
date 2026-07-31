package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/config"
	"github.com/example/grpc-service/pkg/interceptors"
	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Shared test constants used across all *_test.go files in this package.
const (
	testAPIKey    = "integration-api-key-12345"
	testJWTSecret = "test-jwt-secret-very-long-and-secure-for-hs256"
)

// Shared test helpers used by main_test.go and other test files in this package.

// newBaseTestConfig builds a fully valid configuration that binds to free
// loopback ports so parallel test execution cannot collide. Tests that need
// to tweak individual fields should copy the returned struct.
func newBaseTestConfig(t *testing.T) *config.Config {
	t.Helper()
	grpcPort := freePort(t)
	healthPort := freePort(t)
	return &config.Config{
		Server: config.ServerConfig{
			Host:                        "127.0.0.1",
			Port:                        grpcPort,
			MaxConns:                    10,
			Timeout:                     3 * time.Second,
			MaxRecvMessageMB:            1,
			MaxSendMessageMB:            1,
			MaxUsers:                    100,
			MaxStreamMessages:           10,
			MaxStringBytes:              256,
			StreamInterval:              time.Millisecond,
			RateLimitPerSecond:          100,
			RateLimitBurst:              200,
			PerClientRateLimitPerSecond: 100,
			PerClientRateLimitBurst:     200,
		},
		Auth: config.AuthConfig{
			JWTSecret: testJWTSecret,
			APIKeys:   []string{testAPIKey},
		},
		Logging: config.LoggingConfig{Level: "error", Format: "json"},
		Health:  config.HealthConfig{Port: healthPort},
		Storage: config.StorageConfig{
			Backend:         "memory",
			MaxOpenConns:    1,
			MaxIdleConns:    1,
			ConnMaxLifetime: time.Minute,
			PingTimeout:     time.Second,
			RetryAttempts:   1,
			RetryBackoff:    time.Millisecond,
			RetryMaxBackoff: 5 * time.Second,
		},
		Client: config.ClientConfig{Address: "127.0.0.1:" + itoa(grpcPort)},
	}
}

// runningServer holds the state required to interact with a run() invocation
// that is executing in a goroutine.
type runningServer struct {
	cancel    context.CancelFunc
	runErr    <-chan error
	healthURL string
	grpcAddr  string
	stopped   bool
}

// startTestServer runs run() in a goroutine and waits for /readyz to report
// SERVING before returning. The caller must invoke Stop to shut the server
// down cleanly.
func startTestServer(t *testing.T, cfg *config.Config) *runningServer {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, cfg) }()
	healthURL := "http://127.0.0.1:" + itoa(cfg.Health.Port)
	waitForStatus(t, healthURL+"/readyz", http.StatusOK)
	return &runningServer{
		cancel:    cancel,
		runErr:    errCh,
		healthURL: healthURL,
		grpcAddr:  "127.0.0.1:" + itoa(cfg.Server.Port),
	}
}

// Stop cancels the server context and asserts that run() returned without an
// error within the configured shutdown window. It is idempotent: a second call
// (e.g. an explicit Stop followed by a deferred Stop) is a no-op, since run()'s
// result is delivered exactly once.
func (s *runningServer) Stop(t *testing.T) {
	t.Helper()
	if s.stopped {
		return
	}
	s.stopped = true
	s.cancel()
	select {
	case err := <-s.runErr:
		if err != nil {
			t.Fatalf("run() returned error during drain: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not drain after cancellation")
	}
}

// dialTestClient connects to the test gRPC server with insecure credentials.
func dialTestClient(t *testing.T, addr string) *grpc.ClientConn {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("create gRPC client: %v", err)
	}
	return conn
}

// authContext returns a copy of ctx with the test API key attached for
// outbound gRPC metadata.
func authContext(ctx context.Context, apiKey string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "x-api-key", apiKey)
}

// mustGenerateTLSCert creates a self-signed leaf certificate and matching
// ECDSA private key, returning both in PEM form. The cert is valid for
// 127.0.0.1 and "localhost" so the resulting credentials can be loaded by
// loadTLSCredentials and used by a TLS-enabled gRPC server.
func mustGenerateTLSCert(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate ECDSA key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{"grpc-service-test"}},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost"},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return
}

// writeCertFiles persists certPEM and keyPEM into a t.TempDir()-managed
// directory and returns the absolute paths for use with loadTLSCredentials.
func writeCertFiles(t *testing.T, certPEM, keyPEM []byte) (certFile, keyFile string) {
	t.Helper()
	dir := t.TempDir()
	certFile = filepath.Join(dir, "cert.pem")
	keyFile = filepath.Join(dir, "key.pem")
	if err := os.WriteFile(certFile, certPEM, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return
}

// generateTestJWT produces an HS256 JWT signed with secret. The token is
// compatible with the Authenticator's validJWT expectations: the header
// advertises alg=HS256 and typ=JWT, and the payload carries an exp claim
// (and any extra claims requested by the caller).
func generateTestJWT(secret string, exp time.Time, extraClaims map[string]any) string {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	payload := map[string]any{"exp": exp.Unix()}
	for k, v := range extraClaims {
		payload[k] = v
	}
	headerBytes, err := json.Marshal(header)
	if err != nil {
		panic(err)
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	headerB64 := base64.RawURLEncoding.EncodeToString(headerBytes)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signingInput := headerB64 + "." + payloadB64
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig
}

// jwtAuthorizationContext attaches a Bearer JWT to ctx using the metadata
// header the Authenticator expects.
func jwtAuthorizationContext(ctx context.Context, token string) context.Context {
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
}

// mustAuthenticator constructs an Authenticator from cfg.Auth, failing the
// test if configuration is missing the required credentials.
func mustAuthenticator(t *testing.T, cfg *config.Config) *interceptors.Authenticator {
	t.Helper()
	auth, err := interceptors.NewAuthenticator(cfg.Auth)
	if err != nil {
		t.Fatalf("build authenticator: %v", err)
	}
	return auth
}

// blockingUserService is a minimal UserService implementation that blocks
// GetUser until the supplied channel is closed or the caller's context is
// canceled. Used to keep RPCs in flight while testing shutdown paths.
type blockingUserService struct {
	pb.UnimplementedUserServiceServer
	block chan struct{}
}

func (s *blockingUserService) GetUser(ctx context.Context, _ *pb.GetUserRequest) (*pb.User, error) {
	select {
	case <-s.block:
		return &pb.User{Id: "blocked"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestHealthHandlerReadiness(t *testing.T) {
	readiness := &readinessState{}
	handler := healthHandler(readiness)

	req, err := http.NewRequest(http.MethodGet, "/healthz", nil)
	if err != nil {
		t.Fatalf("create health request: %v", err)
	}
	response := recordHTTP(handler, req)
	if response.status != http.StatusOK || response.body != "ok\n" {
		t.Fatalf("healthz = %d %q", response.status, response.body)
	}

	req.URL.Path = "/readyz"
	response = recordHTTP(handler, req)
	if response.status != http.StatusServiceUnavailable {
		t.Fatalf("readyz before startup = %d, want %d", response.status, http.StatusServiceUnavailable)
	}
	readiness.Set(true)
	response = recordHTTP(handler, req)
	if response.status != http.StatusOK || response.body != "ready\n" {
		t.Fatalf("readyz after startup = %d %q", response.status, response.body)
	}
}

func TestRunStartsAndDrainsListeners(t *testing.T) {
	grpcPort := freePort(t)
	healthPort := freePort(t)
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:                        "127.0.0.1",
			Port:                        grpcPort,
			MaxConns:                    10,
			Timeout:                     2 * time.Second,
			MaxRecvMessageMB:            1,
			MaxSendMessageMB:            1,
			MaxUsers:                    10,
			MaxStreamMessages:           2,
			MaxStringBytes:              128,
			StreamInterval:              time.Millisecond,
			RateLimitPerSecond:          100,
			RateLimitBurst:              200,
			PerClientRateLimitPerSecond: 100,
			PerClientRateLimitBurst:     200,
		},
		Auth:    config.AuthConfig{APIKeys: []string{"integration-api-key-12345"}},
		Logging: config.LoggingConfig{Level: "error", Format: "json"},
		Health:  config.HealthConfig{Port: healthPort},
		Storage: config.StorageConfig{Backend: "memory", MaxOpenConns: 1, MaxIdleConns: 1, ConnMaxLifetime: time.Minute, PingTimeout: time.Second, RetryAttempts: 1, RetryBackoff: time.Millisecond, RetryMaxBackoff: 5 * time.Second},
		Client:  config.ClientConfig{Address: "127.0.0.1:" + itoa(grpcPort)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, cfg) }()

	readyURL := "http://127.0.0.1:" + itoa(healthPort) + "/readyz"
	waitForStatus(t, readyURL, http.StatusOK)
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run() returned error during drain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not drain after cancellation")
	}
}

func TestRunServesAuthenticatedRPCs(t *testing.T) {
	grpcPort := freePort(t)
	healthPort := freePort(t)
	const apiKey = "integration-api-key-12345"
	cfg := &config.Config{
		Server: config.ServerConfig{
			Host:                        "127.0.0.1",
			Port:                        grpcPort,
			MaxConns:                    10,
			Timeout:                     2 * time.Second,
			MaxRecvMessageMB:            1,
			MaxSendMessageMB:            1,
			MaxUsers:                    10,
			MaxStreamMessages:           2,
			MaxStringBytes:              128,
			StreamInterval:              time.Millisecond,
			RateLimitPerSecond:          100,
			RateLimitBurst:              200,
			PerClientRateLimitPerSecond: 100,
			PerClientRateLimitBurst:     200,
		},
		Auth:    config.AuthConfig{APIKeys: []string{apiKey}},
		Logging: config.LoggingConfig{Level: "error", Format: "json"},
		Health:  config.HealthConfig{Port: healthPort},
		Storage: config.StorageConfig{Backend: "memory", MaxOpenConns: 1, MaxIdleConns: 1, ConnMaxLifetime: time.Minute, PingTimeout: time.Second, RetryAttempts: 1, RetryBackoff: time.Millisecond, RetryMaxBackoff: 5 * time.Second},
		Client:  config.ClientConfig{Address: "127.0.0.1:" + itoa(grpcPort)},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, cfg) }()
	waitForStatus(t, "http://127.0.0.1:"+itoa(healthPort)+"/readyz", http.StatusOK)

	conn, err := grpc.NewClient("127.0.0.1:"+itoa(grpcPort), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("create gRPC client: %v", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close gRPC client: %v", err)
		}
	}()
	client := pb.NewUserServiceClient(conn)
	unauthenticatedCtx, requestCancel := context.WithTimeout(context.Background(), time.Second)
	_, err = client.CreateUser(unauthenticatedCtx, &pb.CreateUserRequest{Name: "Unauthorized", Email: "bad@example.com", Age: 20})
	requestCancel()
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated CreateUser code = %v, want Unauthenticated", status.Code(err))
	}

	authenticatedCtx, requestCancel := context.WithTimeout(metadata.AppendToOutgoingContext(context.Background(), "x-api-key", apiKey), time.Second)
	user, err := client.CreateUser(authenticatedCtx, &pb.CreateUserRequest{Name: "Integration User", Email: "integration@example.com", Age: 30})
	requestCancel()
	if err != nil {
		t.Fatalf("authenticated CreateUser: %v", err)
	}
	if user.GetId() == "" || user.GetName() != "Integration User" || user.GetEmail() != "integration@example.com" {
		t.Fatalf("unexpected created user: %+v", user)
	}
	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run() returned error during drain: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run() did not drain after cancellation")
	}
}

func TestNewGRPCServerFailsClosedForInvalidTLS(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{
			MaxConns:           1,
			Timeout:            time.Second,
			MaxRecvMessageMB:   1,
			MaxSendMessageMB:   1,
			MaxUsers:           1,
			MaxStreamMessages:  1,
			MaxStringBytes:     1,
			StreamInterval:     time.Millisecond,
			RateLimitPerSecond: 1,
			RateLimitBurst:     1,
		},
		TLS: config.TLSConfig{
			Enabled:  true,
			CertFile: "/does-not-exist/server.crt",
			KeyFile:  "/does-not-exist/server.key",
		},
	}
	if _, _, err := newGRPCServer(cfg, nil); err == nil {
		t.Fatal("newGRPCServer accepted invalid TLS credentials")
	}
}

func TestNewHTTPServer(t *testing.T) {
	addr := "127.0.0.1:" + itoa(freePort(t))
	server := newHTTPServer(addr, http.NewServeMux())
	if server.Addr != addr {
		t.Fatalf("Addr = %q, want %q", server.Addr, addr)
	}
	if server.Handler == nil {
		t.Fatal("Handler is nil")
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v, want 5s", server.ReadHeaderTimeout)
	}
	if server.ReadTimeout != 10*time.Second {
		t.Fatalf("ReadTimeout = %v, want 10s", server.ReadTimeout)
	}
	if server.WriteTimeout != 10*time.Second {
		t.Fatalf("WriteTimeout = %v, want 10s", server.WriteTimeout)
	}
	if server.IdleTimeout != 60*time.Second {
		t.Fatalf("IdleTimeout = %v, want 60s", server.IdleTimeout)
	}
}

func TestCloseListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	closeListener(listener)
	closeListener(listener)
}

func TestNormalizeServeError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{name: "nil", err: nil, want: nil},
		{name: "grpc stopped", err: grpc.ErrServerStopped, want: nil},
		{name: "http closed", err: http.ErrServerClosed, want: nil},
		{name: "wrapped grpc stopped", err: errors.Join(grpc.ErrServerStopped, errors.New("context")), want: nil},
		{name: "wrapped http closed", err: errors.Join(http.ErrServerClosed, errors.New("context")), want: nil},
		{name: "unexpected", err: errors.New("boom"), want: errors.New("boom")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeServeError(tc.err)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("normalizeServeError(%v) = %v, want nil", tc.err, got)
				}
				return
			}
			if got == nil || got.Error() != tc.want.Error() {
				t.Fatalf("normalizeServeError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestShutdownGRPC_Graceful(t *testing.T) {
	server := grpc.NewServer()
	pb.RegisterUserServiceServer(server, &blockingUserService{block: make(chan struct{})})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = server.Serve(listener) }()

	done := make(chan struct{})
	start := time.Now()
	go func() {
		shutdownGRPC(server, context.Background())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdownGRPC did not return on idle server")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("graceful shutdown of idle server took %v", elapsed)
	}
}

func TestShutdownGRPC_ForcedAfterTimeout(t *testing.T) {
	block := make(chan struct{})
	defer close(block)
	server := grpc.NewServer()
	pb.RegisterUserServiceServer(server, &blockingUserService{block: block})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = server.Serve(listener) }()

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)

	rpcErr := make(chan error, 1)
	go func() {
		_, e := client.GetUser(context.Background(), &pb.GetUserRequest{Id: "x"})
		rpcErr <- e
	}()
	time.Sleep(50 * time.Millisecond)

	shortCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		shutdownGRPC(server, shortCtx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdownGRPC did not force-stop after deadline")
	}
	select {
	case e := <-rpcErr:
		if e == nil {
			t.Fatal("blocking RPC returned without error after forced shutdown")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocking RPC did not return after forced shutdown")
	}
}

func TestOpenRepositoryMemory(t *testing.T) {
	repo, err := openRepository(context.Background(), config.StorageConfig{
		Backend:         "memory",
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Minute,
		PingTimeout:     time.Second,
	})
	if err != nil {
		t.Fatalf("openRepository(memory): %v", err)
	}
	if repo == nil {
		t.Fatal("openRepository(memory) returned nil repo")
	}
	if err := repo.Ping(context.Background()); err != nil {
		t.Fatalf("memory Ping: %v", err)
	}
	if err := repo.Close(); err != nil {
		t.Fatalf("memory Close: %v", err)
	}
}

func TestOpenRepositoryUnsupported(t *testing.T) {
	repo, err := openRepository(context.Background(), config.StorageConfig{
		Backend:         "sqlite",
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Minute,
		PingTimeout:     time.Second,
	})
	if err == nil {
		t.Fatal("openRepository(sqlite) returned nil error")
	}
	if repo != nil {
		t.Fatalf("openRepository(sqlite) returned non-nil repo: %#v", repo)
	}
	if !contains(err.Error(), "unsupported storage backend") {
		t.Fatalf("error %v does not mention unsupported backend", err)
	}
}

func TestOpenRepositoryPostgresInvalidURL(t *testing.T) {
	cfg := config.StorageConfig{
		Backend:         "postgres",
		URL:             "postgres://nobody:nopass@127.0.0.1:1/none?sslmode=disable&connect_timeout=1",
		MaxOpenConns:    1,
		MaxIdleConns:    1,
		ConnMaxLifetime: time.Minute,
		PingTimeout:     200 * time.Millisecond,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := openRepository(ctx, cfg); err == nil {
		t.Fatal("openRepository(invalid postgres) returned nil error")
	}
}

func TestMetricsHandler(t *testing.T) {
	handler := metricsHandler()
	if handler == nil {
		t.Fatal("metricsHandler returned nil")
	}
	req, err := http.NewRequest(http.MethodGet, "/metrics", nil)
	if err != nil {
		t.Fatalf("create metrics request: %v", err)
	}
	rec := &responseRecorder{header: make(http.Header)}
	handler.ServeHTTP(rec, req)
	if rec.status != http.StatusOK {
		t.Fatalf("/metrics status = %d, want 200", rec.status)
	}
	if !contains(rec.body, "grpc_server_active_streams") {
		t.Fatalf("/metrics body missing grpc_server_active_streams:\n%s", rec.body)
	}
}

func TestReadinessState(t *testing.T) {
	r := &readinessState{}
	if r.Get() {
		t.Fatal("default readiness.Get() = true, want false")
	}
	r.Set(true)
	if !r.Get() {
		t.Fatal("after Set(true), readiness.Get() = false, want true")
	}
	r.Set(false)
	if r.Get() {
		t.Fatal("after Set(false), readiness.Get() = true, want false")
	}

	if r.Check(context.Background()) {
		t.Fatal("Check with no SetCheck and not ready returned true")
	}
	r.Set(true)
	if !r.Check(context.Background()) {
		t.Fatal("Check with no SetCheck and ready returned false")
	}

	r.SetCheck(func(_ context.Context) error { return nil })
	if !r.Check(context.Background()) {
		t.Fatal("Check with passing check returned false")
	}
	r.SetCheck(func(_ context.Context) error { return errors.New("down") })
	if r.Check(context.Background()) {
		t.Fatal("Check with failing check returned true")
	}
}

func TestReadinessStateCheckTimeout(t *testing.T) {
	r := &readinessState{}
	r.Set(true)
	r.SetCheck(func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if r.Check(ctx) {
		t.Fatal("Check returned true when check function timed out")
	}
}

func TestLoadTLSCredentials(t *testing.T) {
	certPEM, keyPEM := mustGenerateTLSCert(t)
	certFile, keyFile := writeCertFiles(t, certPEM, keyPEM)
	cfg := &config.Config{
		TLS: config.TLSConfig{
			Enabled:  true,
			CertFile: certFile,
			KeyFile:  keyFile,
		},
	}
	creds, err := loadTLSCredentials(cfg)
	if err != nil {
		t.Fatalf("loadTLSCredentials: %v", err)
	}
	if creds == nil {
		t.Fatal("loadTLSCredentials returned nil credentials")
	}
}

func TestLoadTLSCredentialsMissingFile(t *testing.T) {
	cfg := &config.Config{
		TLS: config.TLSConfig{
			Enabled:  true,
			CertFile: "/does-not-exist/cert.pem",
			KeyFile:  "/does-not-exist/key.pem",
		},
	}
	creds, err := loadTLSCredentials(cfg)
	if err == nil {
		t.Fatal("loadTLSCredentials returned nil error for missing files")
	}
	if creds != nil {
		t.Fatalf("loadTLSCredentials returned non-nil credentials: %#v", creds)
	}
}

func TestLoadTLSCredentialsMTLS(t *testing.T) {
	caPEM, _ := mustGenerateTLSCert(t)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o644); err != nil {
		t.Fatalf("write CA: %v", err)
	}
	certPEM, keyPEM := mustGenerateTLSCert(t)
	certFile, keyFile := writeCertFiles(t, certPEM, keyPEM)
	cfg := &config.Config{
		TLS: config.TLSConfig{
			Enabled:  true,
			CertFile: certFile,
			KeyFile:  keyFile,
			MTLS:     config.MTLSConfig{Enabled: true, ClientCAFile: caFile},
		},
	}
	creds, err := loadTLSCredentials(cfg)
	if err != nil {
		t.Fatalf("loadTLSCredentials(mTLS): %v", err)
	}
	tlsConfig := creds.Info().SecurityProtocol
	if tlsConfig != "tls" {
		t.Fatalf("SecurityProtocol = %q, want tls", tlsConfig)
	}
	if _, ok := any(creds).(credentials.TransportCredentials); !ok {
		t.Fatalf("credentials is not TransportCredentials: %T", creds)
	}
}

func TestLoadTLSCredentialsMTLSInvalidCA(t *testing.T) {
	certPEM, keyPEM := mustGenerateTLSCert(t)
	certFile, keyFile := writeCertFiles(t, certPEM, keyPEM)
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, []byte("not a valid PEM certificate"), 0o644); err != nil {
		t.Fatalf("write invalid CA: %v", err)
	}
	cfg := &config.Config{
		TLS: config.TLSConfig{
			Enabled:  true,
			CertFile: certFile,
			KeyFile:  keyFile,
			MTLS:     config.MTLSConfig{Enabled: true, ClientCAFile: caFile},
		},
	}
	creds, err := loadTLSCredentials(cfg)
	if err == nil {
		t.Fatal("loadTLSCredentials(invalid CA) returned nil error")
	}
	if creds != nil {
		t.Fatalf("loadTLSCredentials(invalid CA) returned non-nil credentials")
	}
	if !contains(err.Error(), "client CA") {
		t.Fatalf("error %v does not mention client CA", err)
	}
}

func TestNewGRPCServerWithReflection(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Reflection = true
	auth := mustAuthenticator(t, cfg)
	server, limiter, err := newGRPCServer(cfg, auth)
	if err != nil {
		t.Fatalf("newGRPCServer(reflection): %v", err)
	}
	if server == nil {
		t.Fatal("newGRPCServer(reflection) returned nil")
	}
	limiter.Stop()
	server.Stop()
}

func TestNewGRPCServerWithTracing(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Tracing = config.TracingConfig{Enabled: true}
	auth := mustAuthenticator(t, cfg)
	server, limiter, err := newGRPCServer(cfg, auth)
	if err != nil {
		t.Fatalf("newGRPCServer(tracing): %v", err)
	}
	if server == nil {
		t.Fatal("newGRPCServer(tracing) returned nil")
	}
	limiter.Stop()
	server.Stop()
}

func TestHealthHandlerServesOK(t *testing.T) {
	handler := healthHandler(&readinessState{})
	req, err := http.NewRequest(http.MethodGet, "/healthz", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	rec := &responseRecorder{header: make(http.Header)}
	handler.ServeHTTP(rec, req)
	if rec.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.status)
	}
	if rec.body != "ok\n" {
		t.Fatalf("body = %q, want %q", rec.body, "ok\n")
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/plain; charset=utf-8", got)
	}
}

func TestServeGRPCAndServeHTTP(t *testing.T) {
	server := grpc.NewServer()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	errCh := make(chan error, 1)
	go serveGRPC(server, listener, errCh)
	server.GracefulStop()
	select {
	case err := <-errCh:
		if !errors.Is(err, grpc.ErrServerStopped) {
			t.Fatalf("serveGRPC error = %v, want ErrServerStopped", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveGRPC did not report an error after GracefulStop")
	}

	httpServer := &http.Server{Handler: http.NewServeMux()}
	listener2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen2: %v", err)
	}
	errCh2 := make(chan error, 1)
	go serveHTTP(httpServer, listener2, errCh2)
	_ = listener2.Close()
	select {
	case err := <-errCh2:
		if err == nil {
			t.Fatal("serveHTTP error = nil, want non-nil after listener close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serveHTTP did not report an error after listener close")
	}
}

func TestRunFailsForInvalidConfig(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Storage.Backend = "bogus"
	cfg.Storage.URL = ""
	err := run(context.Background(), cfg)
	if err == nil {
		t.Fatal("run() accepted invalid storage backend")
	}
	if !contains(err.Error(), "storage") {
		t.Fatalf("run() error %v does not mention storage", err)
	}
}

func TestRunFailsForAuthenticatorError(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Auth = config.AuthConfig{}
	if err := run(context.Background(), cfg); err == nil {
		t.Fatal("run() accepted config without authentication")
	}
}

func TestRunFailsWhenMetricsPortEqualsHealthPort(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Metrics.Enabled = true
	cfg.Metrics.Port = cfg.Health.Port
	err := run(context.Background(), cfg)
	if err == nil {
		t.Fatal("run() accepted equal metrics and health ports")
	}
	if !contains(err.Error(), "metrics") {
		t.Fatalf("run() error %v does not mention metrics", err)
	}
}

func recordHTTP(handler http.Handler, req *http.Request) struct {
	status int
	body   string
} {
	recorder := &responseRecorder{header: make(http.Header)}
	handler.ServeHTTP(recorder, req)
	return struct {
		status int
		body   string
	}{status: recorder.status, body: recorder.body}
}

type responseRecorder struct {
	header http.Header
	status int
	body   string
}

func (r *responseRecorder) Header() http.Header    { return r.header }
func (r *responseRecorder) WriteHeader(status int) { r.status = status }
func (r *responseRecorder) Write(body []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	r.body += string(body)
	return len(body), nil
}

func waitForStatus(t *testing.T, url string, expected int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == expected {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("did not observe HTTP %d at %s", expected, url)
}

// allocatedPorts guards against the intra-process freePort race: two parallel
// tests can each ask the kernel for an ephemeral port (:0), close it, and be
// handed the *same* number before either server binds it. We record every port
// handed out for the life of the test binary and retry until we get one no
// other test in this binary has taken, so a just-released port the kernel
// re-offers is rejected rather than double-assigned. (The residual close-then-
// rebind window versus unrelated processes is inherent to the :0 pattern.)
var (
	allocatedPortsMu sync.Mutex
	allocatedPorts   = map[int]bool{}
)

func freePort(t *testing.T) int {
	t.Helper()
	for attempt := 0; attempt < 100; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("reserve port: %v", err)
		}
		port := listener.Addr().(*net.TCPAddr).Port
		if err := listener.Close(); err != nil {
			t.Fatalf("release port: %v", err)
		}
		allocatedPortsMu.Lock()
		taken := allocatedPorts[port]
		if !taken {
			allocatedPorts[port] = true
		}
		allocatedPortsMu.Unlock()
		if !taken {
			return port
		}
	}
	t.Fatal("could not find an unallocated free port after 100 attempts")
	return 0
}

func itoa(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var reversed [20]byte
	index := len(reversed)
	for value > 0 {
		index--
		reversed[index] = digits[value%10]
		value /= 10
	}
	return string(reversed[index:])
}

// contains reports whether substr is present in s.
func contains(s, substr string) bool {
	if substr == "" {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// lintUnusedReferences forces the compiler to keep helpers used only in
// some build configurations; it has no runtime effect.
var _ = tls.VersionTLS12
