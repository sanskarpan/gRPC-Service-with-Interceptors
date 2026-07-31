// Package config owns the validated, explicit configuration contract for the
// service. Keeping validation at the process boundary prevents individual
// listeners and handlers from silently inventing unsafe defaults.
package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the complete runtime configuration for the gRPC service.
type Config struct {
	Server     ServerConfig  `yaml:"server"`
	TLS        TLSConfig     `yaml:"tls"`
	Auth       AuthConfig    `yaml:"auth"`
	Logging    LoggingConfig `yaml:"logging"`
	Tracing    TracingConfig `yaml:"tracing"`
	Metrics    MetricsConfig `yaml:"metrics"`
	Health     HealthConfig  `yaml:"health"`
	Storage    StorageConfig `yaml:"storage"`
	Gateway    GatewayConfig `yaml:"gateway"`
	Reflection bool          `yaml:"reflection"`
	Client     ClientConfig  `yaml:"client"`
}

// ServerConfig contains listener, deadline, and resource limits for gRPC.
type ServerConfig struct {
	Host                        string        `yaml:"host"`
	Port                        int           `yaml:"port"`
	MaxConns                    int           `yaml:"max_conns"`
	Timeout                     time.Duration `yaml:"timeout"`
	MaxRecvMessageMB            int           `yaml:"max_recv_message_mb"`
	MaxSendMessageMB            int           `yaml:"max_send_message_mb"`
	MaxUsers                    int           `yaml:"max_users"`
	MaxStreamMessages           int           `yaml:"max_stream_messages"`
	MaxStringBytes              int           `yaml:"max_string_bytes"`
	StreamInterval              time.Duration `yaml:"stream_interval"`
	RateLimitPerSecond          int           `yaml:"rate_limit_per_second"`
	RateLimitBurst              int           `yaml:"rate_limit_burst"`
	PerClientRateLimitPerSecond int           `yaml:"per_client_rate_limit_per_second"`
	PerClientRateLimitBurst     int           `yaml:"per_client_rate_limit_burst"`
}

// TLSConfig describes server-side TLS and optional mutual TLS validation.
type TLSConfig struct {
	Enabled        bool       `yaml:"enabled"`
	CertFile       string     `yaml:"cert_file"`
	KeyFile        string     `yaml:"key_file"`
	CAFile         string     `yaml:"ca_file"`
	ClientCertFile string     `yaml:"client_cert_file"`
	ClientKeyFile  string     `yaml:"client_key_file"`
	MTLS           MTLSConfig `yaml:"mtls"`
}

// MTLSConfig contains the CA bundle and enforcement switch for client certs.
type MTLSConfig struct {
	Enabled      bool   `yaml:"enabled"`
	ClientCAFile string `yaml:"client_ca_file"`
}

// AuthConfig contains server credential material. Secrets must be injected at
// runtime; checked-in configuration intentionally contains no credentials.
type AuthConfig struct {
	JWTSecret string   `yaml:"jwt_secret"`
	APIKeys   []string `yaml:"api_keys"`
	// JWTAudience and JWTIssuer, when set, bind accepted JWTs to the expected
	// aud/iss claims. Empty means the corresponding claim is not checked.
	JWTAudience string `yaml:"jwt_audience"`
	JWTIssuer   string `yaml:"jwt_issuer"`
	// JWTPublicKey is a PEM-encoded RSA or EC (P-256) public key. When set, the
	// server also accepts asymmetric RS256/ES256 JWTs verified against it (in
	// addition to HS256 when jwt_secret is set). Prefer this over a shared HS256
	// secret in production so the signing key never leaves the issuer.
	JWTPublicKey string `yaml:"jwt_public_key"`
	// AuditAllows, when true, additionally emits an audit record for every
	// successful authentication (not just denials). Off by default because it is
	// one record per authenticated request; enable it when a full
	// who-accessed-what trail is required.
	AuditAllows bool `yaml:"audit_allows"`
}

// minJWTSecretBytes is the minimum HMAC-SHA256 key length. 256 bits matches the
// hash output size; shorter keys weaken the MAC.
const minJWTSecretBytes = 32

// LoggingConfig controls the structured logger.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// TracingConfig controls OpenTelemetry export.
type TracingConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Endpoint    string `yaml:"endpoint"`
	ServiceName string `yaml:"service_name"`
	// SampleRatio is the head-based sampling probability in [0,1] for root
	// spans (parent-based sampling always respects an upstream decision). A
	// value <= 0 is treated as 1.0 (always sample) for backward compatibility.
	SampleRatio float64 `yaml:"sample_ratio"`
}

// MetricsConfig controls the Prometheus listener.
type MetricsConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

// GatewayConfig controls the REST-to-gRPC gateway listener.
type GatewayConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

// HealthConfig controls the liveness/readiness listener.
type HealthConfig struct {
	Port int `yaml:"port"`
}

// CacheConfig controls the optional read-through cache layer for graceful
// degradation. When enabled, Get operations serve stale data on backend failure.
type CacheConfig struct {
	Enabled bool          `yaml:"enabled"`
	TTL     time.Duration `yaml:"ttl"`
}

// CircuitBreakerConfig controls the storage circuit breaker wrapper.
type CircuitBreakerConfig struct {
	Enabled             bool          `yaml:"enabled"`
	FailureThreshold    int           `yaml:"failure_threshold"`
	SuccessThreshold    int           `yaml:"success_threshold"`
	OpenTimeout         time.Duration `yaml:"open_timeout"`
	MaxHalfOpenRequests int           `yaml:"max_half_open_requests"`
}

// StorageConfig defines the persistence backend and database pool contract.
// Memory is intended for development/tests; production requires PostgreSQL.
type StorageConfig struct {
	Backend         string               `yaml:"backend"`
	URL             string               `yaml:"url"`
	MaxOpenConns    int                  `yaml:"max_open_conns"`
	MaxIdleConns    int                  `yaml:"max_idle_conns"`
	ConnMaxLifetime time.Duration        `yaml:"conn_max_lifetime"`
	PingTimeout     time.Duration        `yaml:"ping_timeout"`
	RetryAttempts   int                  `yaml:"retry_attempts"`
	RetryBackoff    time.Duration        `yaml:"retry_backoff"`
	RetryMaxBackoff time.Duration        `yaml:"retry_max_backoff"`
	CircuitBreaker  CircuitBreakerConfig `yaml:"circuit_breaker"`
	Cache           CacheConfig          `yaml:"cache"`
}

// ClientConfig contains settings used by the example client.
type ClientConfig struct {
	Address    string `yaml:"address"`
	ServerName string `yaml:"server_name"`
	AuthToken  string `yaml:"auth_token"`
}

// Load reads a YAML configuration, applies supported environment overrides,
// and rejects unknown or invalid values before any listener is opened.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := applyEnvironment(&cfg); err != nil {
		return nil, fmt.Errorf("apply environment: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	return &cfg, nil
}

// Validate checks security-sensitive values, listener ranges, and resource
// bounds. It is public so startup tests and deployment tooling can validate
// the same contract as the running process.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is nil")
	}
	if strings.TrimSpace(c.Server.Host) == "" {
		return fmt.Errorf("server.host is required")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	if c.Server.MaxConns < 1 {
		return fmt.Errorf("server.max_conns must be positive")
	}
	if c.Server.Timeout <= 0 {
		return fmt.Errorf("server.timeout must be positive")
	}
	if c.Server.MaxRecvMessageMB < 1 || c.Server.MaxSendMessageMB < 1 {
		return fmt.Errorf("server message limits must be positive")
	}
	if c.Server.MaxUsers < 1 || c.Server.MaxStreamMessages < 1 || c.Server.MaxStringBytes < 1 {
		return fmt.Errorf("server resource limits must be positive")
	}
	if c.Server.StreamInterval <= 0 {
		return fmt.Errorf("server.stream_interval must be positive")
	}
	if c.Server.RateLimitPerSecond < 1 || c.Server.RateLimitBurst < 1 {
		return fmt.Errorf("server rate limit values must be positive")
	}
	if c.Server.PerClientRateLimitPerSecond < 1 || c.Server.PerClientRateLimitBurst < 1 {
		return fmt.Errorf("server per-client rate limit values must be positive")
	}
	if c.TLS.Enabled {
		if strings.TrimSpace(c.TLS.CertFile) == "" || strings.TrimSpace(c.TLS.KeyFile) == "" {
			return fmt.Errorf("tls.cert_file and tls.key_file are required when TLS is enabled")
		}
		if c.TLS.MTLS.Enabled && strings.TrimSpace(c.TLS.MTLS.ClientCAFile) == "" {
			return fmt.Errorf("tls.mtls.client_ca_file is required when mTLS is enabled")
		}
	}
	if c.TLS.MTLS.Enabled && !c.TLS.Enabled {
		return fmt.Errorf("tls.mtls.enabled requires tls.enabled")
	}
	if strings.TrimSpace(c.Auth.JWTSecret) == "" && len(c.Auth.APIKeys) == 0 && strings.TrimSpace(c.Auth.JWTPublicKey) == "" {
		return fmt.Errorf("at least one of auth.jwt_secret, auth.jwt_public_key, or auth.api_keys is required")
	}
	if s := c.Auth.JWTSecret; s != "" && len(s) < minJWTSecretBytes {
		return fmt.Errorf("auth.jwt_secret must be at least %d bytes", minJWTSecretBytes)
	}
	if pk := strings.TrimSpace(c.Auth.JWTPublicKey); pk != "" && !strings.Contains(pk, "-----BEGIN") {
		return fmt.Errorf("auth.jwt_public_key must be a PEM-encoded public key")
	}
	for i, key := range c.Auth.APIKeys {
		if len(key) < 16 {
			return fmt.Errorf("auth.api_keys[%d] must be at least 16 characters", i)
		}
	}
	if c.Logging.Level == "" {
		return fmt.Errorf("logging.level is required")
	}
	if !isLogLevel(c.Logging.Level) {
		return fmt.Errorf("logging.level %q is invalid", c.Logging.Level)
	}
	if c.Logging.Format != "json" && c.Logging.Format != "console" {
		return fmt.Errorf("logging.format must be json or console")
	}
	if c.Tracing.Enabled {
		if c.Tracing.ServiceName == "" {
			return fmt.Errorf("tracing.service_name is required when tracing is enabled")
		}
		u, err := url.ParseRequestURI(c.Tracing.Endpoint)
		if err != nil || u == nil {
			return fmt.Errorf("tracing.endpoint must be an absolute URL when tracing is enabled")
		}
		scheme := strings.ToLower(u.Scheme)
		if u.Host == "" || (scheme != "http" && scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("tracing.endpoint must be an absolute URL when tracing is enabled")
		}
	}
	if c.Metrics.Enabled && !validPort(c.Metrics.Port) {
		return fmt.Errorf("metrics.port must be between 1 and 65535")
	}
	if c.Metrics.Enabled && c.Metrics.Port == c.Health.Port {
		return fmt.Errorf("metrics.port and health.port must differ")
	}
	if c.Gateway.Enabled && !validPort(c.Gateway.Port) {
		return fmt.Errorf("gateway.port must be between 1 and 65535")
	}
	if c.Gateway.Enabled && (c.Gateway.Port == c.Health.Port || (c.Metrics.Enabled && c.Gateway.Port == c.Metrics.Port)) {
		return fmt.Errorf("gateway.port must differ from health.port and metrics.port")
	}
	if !validPort(c.Health.Port) {
		return fmt.Errorf("health.port must be between 1 and 65535")
	}
	if c.Storage.Backend != "memory" && c.Storage.Backend != "postgres" {
		return fmt.Errorf("storage.backend must be memory or postgres")
	}
	if c.Storage.MaxOpenConns < 1 || c.Storage.MaxIdleConns < 0 || c.Storage.MaxIdleConns > c.Storage.MaxOpenConns {
		return fmt.Errorf("storage connection pool limits are invalid")
	}
	if c.Storage.ConnMaxLifetime <= 0 || c.Storage.PingTimeout <= 0 {
		return fmt.Errorf("storage timeouts must be positive")
	}
	if c.Storage.RetryAttempts < 0 {
		return fmt.Errorf("storage.retry_attempts must be non-negative")
	}
	if c.Storage.RetryBackoff <= 0 {
		return fmt.Errorf("storage.retry_backoff must be positive")
	}
	if c.Storage.RetryMaxBackoff <= 0 {
		return fmt.Errorf("storage.retry_max_backoff must be positive")
	}
	if c.Storage.CircuitBreaker.Enabled {
		if c.Storage.CircuitBreaker.FailureThreshold < 1 {
			return fmt.Errorf("storage.circuit_breaker.failure_threshold must be positive")
		}
		if c.Storage.CircuitBreaker.SuccessThreshold < 1 {
			return fmt.Errorf("storage.circuit_breaker.success_threshold must be positive")
		}
		if c.Storage.CircuitBreaker.OpenTimeout <= 0 {
			return fmt.Errorf("storage.circuit_breaker.open_timeout must be positive")
		}
		if c.Storage.CircuitBreaker.MaxHalfOpenRequests < 1 {
			return fmt.Errorf("storage.circuit_breaker.max_half_open_requests must be positive")
		}
	}
	if c.Storage.Cache.Enabled {
		if c.Storage.Cache.TTL <= 0 {
			return fmt.Errorf("storage.cache.ttl must be positive when cache is enabled")
		}
	}
	if c.Storage.Backend == "postgres" && strings.TrimSpace(c.Storage.URL) == "" {
		return fmt.Errorf("storage.url is required for postgres")
	}
	if c.Client.Address == "" {
		return fmt.Errorf("client.address is required")
	}
	return nil
}

func applyEnvironment(c *Config) error {
	var err error
	setString("SERVER_HOST", &c.Server.Host)
	err = firstError(err, setInt("SERVER_PORT", &c.Server.Port))
	err = firstError(err, setInt("SERVER_MAX_CONNS", &c.Server.MaxConns))
	err = firstError(err, setDuration("SERVER_TIMEOUT", &c.Server.Timeout))
	err = firstError(err, setInt("SERVER_MAX_RECV_MESSAGE_MB", &c.Server.MaxRecvMessageMB))
	err = firstError(err, setInt("SERVER_MAX_SEND_MESSAGE_MB", &c.Server.MaxSendMessageMB))
	err = firstError(err, setInt("SERVER_MAX_USERS", &c.Server.MaxUsers))
	err = firstError(err, setInt("SERVER_MAX_STREAM_MESSAGES", &c.Server.MaxStreamMessages))
	err = firstError(err, setInt("SERVER_MAX_STRING_BYTES", &c.Server.MaxStringBytes))
	err = firstError(err, setDuration("SERVER_STREAM_INTERVAL", &c.Server.StreamInterval))
	err = firstError(err, setInt("SERVER_RATE_LIMIT_PER_SECOND", &c.Server.RateLimitPerSecond))
	err = firstError(err, setInt("SERVER_RATE_LIMIT_BURST", &c.Server.RateLimitBurst))
	err = firstError(err, setInt("SERVER_PER_CLIENT_RATE_LIMIT_PER_SECOND", &c.Server.PerClientRateLimitPerSecond))
	err = firstError(err, setInt("SERVER_PER_CLIENT_RATE_LIMIT_BURST", &c.Server.PerClientRateLimitBurst))
	err = firstError(err, setBool("CIRCUIT_BREAKER_ENABLED", &c.Storage.CircuitBreaker.Enabled))
	err = firstError(err, setBool("CACHE_ENABLED", &c.Storage.Cache.Enabled))
	err = firstError(err, setDuration("CACHE_TTL", &c.Storage.Cache.TTL))
	err = firstError(err, setBool("TLS_ENABLED", &c.TLS.Enabled))
	setString("TLS_CERT_FILE", &c.TLS.CertFile)
	setString("TLS_KEY_FILE", &c.TLS.KeyFile)
	setString("TLS_CA_FILE", &c.TLS.CAFile)
	setString("TLS_CLIENT_CERT_FILE", &c.TLS.ClientCertFile)
	setString("TLS_CLIENT_KEY_FILE", &c.TLS.ClientKeyFile)
	err = firstError(err, setBool("MTLS_ENABLED", &c.TLS.MTLS.Enabled))
	setString("MTLS_CLIENT_CA_FILE", &c.TLS.MTLS.ClientCAFile)
	setString("JWT_SECRET", &c.Auth.JWTSecret)
	setString("JWT_PUBLIC_KEY", &c.Auth.JWTPublicKey)
	setString("AUTH_JWT_AUDIENCE", &c.Auth.JWTAudience)
	setString("AUTH_JWT_ISSUER", &c.Auth.JWTIssuer)
	err = firstError(err, setBool("AUTH_AUDIT_ALLOWS", &c.Auth.AuditAllows))
	if value, ok := os.LookupEnv("AUTH_API_KEYS"); ok {
		c.Auth.APIKeys = splitNonEmpty(value)
	}
	setString("LOG_LEVEL", &c.Logging.Level)
	setString("LOG_FORMAT", &c.Logging.Format)
	err = firstError(err, setBool("TRACING_ENABLED", &c.Tracing.Enabled))
	setString("TRACING_ENDPOINT", &c.Tracing.Endpoint)
	setString("TRACING_SERVICE_NAME", &c.Tracing.ServiceName)
	err = firstError(err, setBool("METRICS_ENABLED", &c.Metrics.Enabled))
	err = firstError(err, setInt("METRICS_PORT", &c.Metrics.Port))
	err = firstError(err, setInt("HEALTH_PORT", &c.Health.Port))
	err = firstError(err, setBool("GATEWAY_ENABLED", &c.Gateway.Enabled))
	err = firstError(err, setInt("GATEWAY_PORT", &c.Gateway.Port))
	setString("CLIENT_ADDRESS", &c.Client.Address)
	setString("CLIENT_SERVER_NAME", &c.Client.ServerName)
	setString("CLIENT_AUTH_TOKEN", &c.Client.AuthToken)
	setString("STORAGE_BACKEND", &c.Storage.Backend)
	setString("DATABASE_URL", &c.Storage.URL)
	err = firstError(err, setInt("STORAGE_MAX_OPEN_CONNS", &c.Storage.MaxOpenConns))
	err = firstError(err, setInt("STORAGE_MAX_IDLE_CONNS", &c.Storage.MaxIdleConns))
	err = firstError(err, setDuration("STORAGE_CONN_MAX_LIFETIME", &c.Storage.ConnMaxLifetime))
	err = firstError(err, setDuration("STORAGE_PING_TIMEOUT", &c.Storage.PingTimeout))
	err = firstError(err, setInt("STORAGE_RETRY_ATTEMPTS", &c.Storage.RetryAttempts))
	err = firstError(err, setDuration("STORAGE_RETRY_BACKOFF", &c.Storage.RetryBackoff))
	err = firstError(err, setDuration("STORAGE_RETRY_MAX_BACKOFF", &c.Storage.RetryMaxBackoff))
	return err
}

func setString(name string, target *string) {
	if value, ok := os.LookupEnv(name); ok {
		*target = value
	}
}

func setInt(name string, target *int) error {
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("%s must be an integer: %w", name, err)
	}
	*target = parsed
	return nil
}

func setBool(name string, target *bool) error {
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("%s must be boolean: %w", name, err)
	}
	*target = parsed
	return nil
}

func setDuration(name string, target *time.Duration) error {
	value, ok := os.LookupEnv(name)
	if !ok {
		return nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%s must be a duration: %w", name, err)
	}
	*target = parsed
	return nil
}

func splitNonEmpty(value string) []string {
	parts := strings.Split(value, ",")
	keys := make([]string, 0, len(parts))
	for _, part := range parts {
		if key := strings.TrimSpace(part); key != "" {
			keys = append(keys, key)
		}
	}
	return keys
}

func firstError(current, next error) error {
	if current != nil {
		return current
	}
	return next
}

func validPort(port int) bool {
	return port >= 1 && port <= 65535
}

func isLogLevel(level string) bool {
	switch strings.ToLower(level) {
	case "trace", "debug", "info", "warn", "error", "fatal", "panic":
		return true
	default:
		return false
	}
}
