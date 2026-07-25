package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/example/grpc-service/internal/service"
	"github.com/example/grpc-service/internal/storage"
	"github.com/example/grpc-service/pkg/config"
	"github.com/example/grpc-service/pkg/interceptors"
	"github.com/example/grpc-service/pkg/logging"
	"github.com/example/grpc-service/pkg/metrics"
	"github.com/example/grpc-service/pkg/pb"
	"github.com/example/grpc-service/pkg/tracing"
	grpcrecovery "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

const defaultConfigPath = "configs/config.yaml"

func main() {
	configPath := os.Getenv("CONFIG_PATH")
	if strings.TrimSpace(configPath) == "" {
		configPath = defaultConfigPath
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		logging.Logger().Error().Err(err).Msg("server stopped with error")
		os.Exit(1)
	}
}

// run owns all listeners and their shutdown lifecycle. It is intentionally
// separate from main so startup, readiness, and draining can be integration
// tested without sending process signals.
func run(ctx context.Context, cfg *config.Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	logging.Init(cfg.Logging.Level, cfg.Logging.Format)

	authenticator, err := interceptors.NewAuthenticator(cfg.Auth)
	if err != nil {
		return fmt.Errorf("create authenticator: %w", err)
	}
	tracingShutdown, err := tracing.Init(ctx, cfg.Tracing)
	if err != nil {
		return fmt.Errorf("initialize tracing: %w", err)
	}
	defer func() {
		if tracingShutdown == nil {
			return
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tracingShutdown(shutdownCtx); err != nil {
			logging.Logger().Error().Err(err).Msg("tracing shutdown failed")
		}
	}()

	repository, err := openRepository(ctx, cfg.Storage)
	if err != nil {
		return fmt.Errorf("initialize storage: %w", err)
	}
	defer func() {
		if err := repository.Close(); err != nil {
			logging.Logger().Error().Err(err).Msg("storage shutdown failed")
		}
	}()
	userService := service.NewUserServiceWithRepository(repository, service.ServiceLimits{
		MaxUsers:          cfg.Server.MaxUsers,
		MaxStreamMessages: cfg.Server.MaxStreamMessages,
		MaxStringBytes:    cfg.Server.MaxStringBytes,
		StreamInterval:    cfg.Server.StreamInterval,
	})
	if pgRepo, ok := repository.(*storage.PostgresRepository); ok {
		metrics.NewDBStatsCollector(pgRepo.DB(), "db")
	}
	healthServer := health.NewServer()
	readiness := &readinessState{}
	readiness.SetCheck(repository.Ping)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	grpcServer, perClientLimiter, err := newGRPCServer(cfg, authenticator)
	if err != nil {
		return err
	}
	defer perClientLimiter.Stop()
	pb.RegisterUserServiceServer(grpcServer, userService)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	if cfg.Reflection {
		reflection.Register(grpcServer)
	}

	grpcAddress := net.JoinHostPort(cfg.Server.Host, fmt.Sprintf("%d", cfg.Server.Port))
	grpcListener, err := net.Listen("tcp", grpcAddress)
	if err != nil {
		return fmt.Errorf("listen for gRPC on %s: %w", grpcAddress, err)
	}
	defer closeListener(grpcListener)

	healthAddress := net.JoinHostPort(cfg.Server.Host, fmt.Sprintf("%d", cfg.Health.Port))
	healthServerHTTP := newHTTPServer(healthAddress, healthHandler(readiness))
	healthListener, err := net.Listen("tcp", healthAddress)
	if err != nil {
		return fmt.Errorf("listen for health on %s: %w", healthAddress, err)
	}
	defer closeListener(healthListener)

	var metricsServer *http.Server
	var metricsListener net.Listener
	if cfg.Metrics.Enabled {
		if cfg.Metrics.Port == cfg.Health.Port {
			return fmt.Errorf("metrics.port and health.port must differ")
		}
		metricsAddress := net.JoinHostPort(cfg.Server.Host, fmt.Sprintf("%d", cfg.Metrics.Port))
		metricsListener, err = net.Listen("tcp", metricsAddress)
		if err != nil {
			return fmt.Errorf("listen for metrics on %s: %w", metricsAddress, err)
		}
		defer closeListener(metricsListener)
		metricsServer = newHTTPServer(metricsAddress, metricsHandler())
	}

	var gatewayServer *http.Server
	var gatewayListener net.Listener
	if cfg.Gateway.Enabled {
		gwMux := runtime.NewServeMux()
		if err := pb.RegisterUserServiceHandlerServer(ctx, gwMux, userService); err != nil {
			return fmt.Errorf("register gateway handler: %w", err)
		}
		gatewayAddress := net.JoinHostPort(cfg.Server.Host, fmt.Sprintf("%d", cfg.Gateway.Port))
		gatewayListener, err = net.Listen("tcp", gatewayAddress)
		if err != nil {
			return fmt.Errorf("listen for gateway on %s: %w", gatewayAddress, err)
		}
		defer closeListener(gatewayListener)
		gatewayHandler := corsMiddleware(gwMux)
		gatewayServer = newHTTPServer(gatewayAddress, gatewayHandler)
	}

	shutdownCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 4)
	go serveGRPC(grpcServer, grpcListener, errCh)
	go serveHTTP(healthServerHTTP, healthListener, errCh)
	if metricsServer != nil {
		go serveHTTP(metricsServer, metricsListener, errCh)
	}
	if gatewayServer != nil {
		go serveHTTP(gatewayServer, gatewayListener, errCh)
	}

	readiness.Set(true)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	logging.Logger().Info().Str("addr", grpcAddress).Bool("tls", cfg.TLS.Enabled).Msg("gRPC server ready")

	var runErr error
	select {
	case <-ctx.Done():
		logging.Logger().Info().Msg("shutdown signal received")
	case runErr = <-errCh:
		if !errors.Is(runErr, grpc.ErrServerStopped) && !errors.Is(runErr, http.ErrServerClosed) {
			logging.Logger().Error().Err(runErr).Msg("server listener failed")
		}
	}
	readiness.Set(false)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	drainCtx, drainCancel := context.WithTimeout(context.Background(), cfg.Server.Timeout)
	defer drainCancel()
	shutdownGRPC(grpcServer, drainCtx)
	if err := healthServerHTTP.Shutdown(drainCtx); err != nil && runErr == nil {
		runErr = fmt.Errorf("shutdown health server: %w", err)
	}
	if metricsServer != nil {
		if err := metricsServer.Shutdown(drainCtx); err != nil && runErr == nil {
			runErr = fmt.Errorf("shutdown metrics server: %w", err)
		}
	}
	if gatewayServer != nil {
		if err := gatewayServer.Shutdown(drainCtx); err != nil && runErr == nil {
			runErr = fmt.Errorf("shutdown gateway server: %w", err)
		}
	}
	cancel()
	<-shutdownCtx.Done()
	return normalizeServeError(runErr)
}

func openRepository(ctx context.Context, cfg config.StorageConfig) (storage.Repository, error) {
	var repo storage.Repository
	switch cfg.Backend {
	case "memory":
		repo = storage.NewMemoryRepository()
	case "postgres":
		var err error
		repo, err = storage.NewPostgresRepository(ctx, storage.PostgresConfig{
			URL:              cfg.URL,
			MaxOpenConns:     cfg.MaxOpenConns,
			MaxIdleConns:     cfg.MaxIdleConns,
			ConnMaxLifetime:  cfg.ConnMaxLifetime,
			PingTimeout:      cfg.PingTimeout,
			RetryAttempts:    cfg.RetryAttempts,
			RetryBackoff:     cfg.RetryBackoff,
			RetryMaxBackoff:  cfg.RetryMaxBackoff,
		})
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported storage backend %q", cfg.Backend)
	}
	if cfg.CircuitBreaker.Enabled {
		repo = storage.NewCircuitBreakerRepository(repo, storage.CircuitBreakerConfig{
			Enabled:             cfg.CircuitBreaker.Enabled,
			FailureThreshold:    cfg.CircuitBreaker.FailureThreshold,
			SuccessThreshold:    cfg.CircuitBreaker.SuccessThreshold,
			OpenTimeout:         cfg.CircuitBreaker.OpenTimeout,
			MaxHalfOpenRequests: cfg.CircuitBreaker.MaxHalfOpenRequests,
		})
	}
	if cfg.Cache.Enabled {
		repo = storage.NewCacheRepository(repo, cfg.Cache.TTL)
	}
	return repo, nil
}

func newGRPCServer(cfg *config.Config, authenticator *interceptors.Authenticator) (*grpc.Server, *interceptors.PerClientRateLimiter, error) {
	unaryRateLimit, streamRateLimit := interceptors.NewRateLimitInterceptors(cfg.Server.RateLimitPerSecond, cfg.Server.RateLimitBurst)
	perClientUnary, perClientStream, perClientLimiter := interceptors.NewPerClientRateLimitInterceptors(
		cfg.Server.PerClientRateLimitPerSecond,
		cfg.Server.PerClientRateLimitBurst,
		5*time.Minute,
	)
	opts := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(
			grpcrecovery.UnaryServerInterceptor(),
			interceptors.UnaryRequestIDInterceptor,
			interceptors.UnaryLoggingInterceptor,
			interceptors.UnaryMetricsInterceptor,
			unaryRateLimit,
			interceptors.UnaryAuthInterceptorWithAuthenticator(authenticator),
			perClientUnary,
			interceptors.UnaryTimeoutInterceptor(cfg.Server.Timeout),
		),
		grpc.ChainStreamInterceptor(
			grpcrecovery.StreamServerInterceptor(),
			interceptors.StreamRequestIDInterceptor,
			interceptors.StreamLoggingInterceptor,
			interceptors.StreamMetricsInterceptor,
			streamRateLimit,
			interceptors.StreamAuthInterceptorWithAuthenticator(authenticator),
			perClientStream,
		),
		grpc.MaxRecvMsgSize(cfg.Server.MaxRecvMessageMB * 1024 * 1024),
		grpc.MaxSendMsgSize(cfg.Server.MaxSendMessageMB * 1024 * 1024),
		grpc.MaxConcurrentStreams(uint32(cfg.Server.MaxConns)),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: time.Duration(cfg.Server.Timeout),
			Time:              time.Duration(cfg.Server.Timeout / 2),
			Timeout:           10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: false,
		}),
	}
	if cfg.TLS.Enabled {
		creds, err := loadTLSCredentials(cfg)
		if err != nil {
			return nil, perClientLimiter, fmt.Errorf("load TLS credentials: %w", err)
		}
		opts = append(opts, grpc.Creds(creds))
	}
	if cfg.Tracing.Enabled {
		opts = append(opts, grpc.StatsHandler(tracing.NewServerHandler()))
	}
	return grpc.NewServer(opts...), perClientLimiter, nil
}

func loadTLSCredentials(cfg *config.Config) (credentials.TransportCredentials, error) {
	serverCert, err := tls.LoadX509KeyPair(cfg.TLS.CertFile, cfg.TLS.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("load server certificate: %w", err)
	}
	tlsConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCert},
	}
	if cfg.TLS.MTLS.Enabled {
		clientCA, err := os.ReadFile(cfg.TLS.MTLS.ClientCAFile)
		if err != nil {
			return nil, fmt.Errorf("read client CA: %w", err)
		}
		certPool := x509.NewCertPool()
		if ok := certPool.AppendCertsFromPEM(clientCA); !ok {
			return nil, fmt.Errorf("client CA contains no valid certificates")
		}
		tlsConfig.ClientCAs = certPool
		tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return credentials.NewTLS(tlsConfig), nil
}

func serveGRPC(server *grpc.Server, listener net.Listener, errCh chan<- error) {
	errCh <- server.Serve(listener)
}

func serveHTTP(server *http.Server, listener net.Listener, errCh chan<- error) {
	errCh <- server.Serve(listener)
}

func closeListener(listener net.Listener) {
	if listener == nil {
		return
	}
	if err := listener.Close(); err != nil {
		// GracefulStop / Shutdown may have already closed the underlying
		// file descriptor; the deferred closeListener then runs on a
		// closed FD. That is expected and must not surface as an error.
		if !errors.Is(err, net.ErrClosed) {
			logging.Logger().Error().Err(err).Msg("listener close failed")
		}
	}
}

func newHTTPServer(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              address,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func healthHandler(readiness *readinessState) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		// A disconnected probe has no recoverable response path.
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if !readiness.Check(req.Context()) {
			http.Error(w, "not ready\n", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		// A disconnected probe has no recoverable response path.
		_, _ = w.Write([]byte("ready\n"))
	})
	return mux
}

func metricsHandler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	return mux
}

// corsMiddleware returns an http.Handler that sets CORS headers for the
// REST gateway and passes the request through to the wrapped handler.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID, X-Api-Key")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func shutdownGRPC(server *grpc.Server, ctx context.Context) {
	done := make(chan struct{})
	go func() {
		server.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		server.Stop()
		<-done
	}
}

func normalizeServeError(err error) error {
	if err == nil || errors.Is(err, grpc.ErrServerStopped) || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

type readinessState struct {
	ready atomic.Bool
	check atomic.Value
}

func (r *readinessState) Set(value bool) {
	r.ready.Store(value)
}

func (r *readinessState) Get() bool {
	return r.ready.Load()
}

func (r *readinessState) SetCheck(check func(context.Context) error) {
	r.check.Store(check)
}

func (r *readinessState) Check(ctx context.Context) bool {
	if !r.ready.Load() {
		return false
	}
	value := r.check.Load()
	if value == nil {
		return true
	}
	check, ok := value.(func(context.Context) error)
	if !ok || check == nil {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return check(checkCtx) == nil
}
