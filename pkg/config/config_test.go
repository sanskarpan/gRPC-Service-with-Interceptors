package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadSuccess(t *testing.T) {
	path := writeConfigFile(t, `
server:
  host: "127.0.0.1"
  port: 50051
  max_conns: 100
  timeout: 30s
  max_recv_message_mb: 4
  max_send_message_mb: 4
  max_users: 1000
  max_stream_messages: 100
  max_string_bytes: 1024
  stream_interval: 1ms
  rate_limit_per_second: 100
  rate_limit_burst: 200
  per_client_rate_limit_per_second: 50
  per_client_rate_limit_burst: 100
tls:
  enabled: false
auth:
  jwt_secret: "test-secret"
  api_keys: []
logging:
  level: "debug"
  format: "json"
tracing:
  enabled: false
  service_name: "test"
metrics:
  enabled: true
  port: 9090
health:
  port: 8080
storage:
  backend: "memory"
  max_open_conns: 10
  max_idle_conns: 5
  conn_max_lifetime: 5m
  ping_timeout: 5s
  retry_attempts: 3
  retry_backoff: 250ms
  retry_max_backoff: 5s
reflection: false
client:
  address: "127.0.0.1:50051"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Host != "127.0.0.1" || cfg.Server.Port != 50051 {
		t.Fatalf("unexpected server config: %#v", cfg.Server)
	}
	if cfg.Server.Timeout != 30*time.Second || cfg.Server.MaxUsers != 1000 {
		t.Fatalf("unexpected limits: %#v", cfg.Server)
	}
	if cfg.Auth.JWTSecret != "test-secret" || cfg.Reflection {
		t.Fatalf("unexpected auth/reflection config: %#v %v", cfg.Auth, cfg.Reflection)
	}
}

func TestLoadEnvironmentOverridesAndRejectsInvalidValues(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML)
	t.Setenv("SERVER_PORT", "50052")
	t.Setenv("SERVER_TIMEOUT", "2s")
	t.Setenv("AUTH_API_KEYS", "first-long-api-key, second-long-api-key")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.Server.Port != 50052 || cfg.Server.Timeout != 2*time.Second {
		t.Fatalf("environment override not applied: %#v", cfg.Server)
	}
	if len(cfg.Auth.APIKeys) != 2 || cfg.Auth.APIKeys[1] != "second-long-api-key" {
		t.Fatalf("API_KEYS override not applied: %#v", cfg.Auth.APIKeys)
	}

	t.Setenv("SERVER_PORT", "not-a-port")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted an invalid integer environment override")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML+"\nunknown: true\n")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted an unknown field")
	}
}

func TestLoadRejectsMissingCredentials(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML)
	t.Setenv("JWT_SECRET", "")
	t.Setenv("AUTH_API_KEYS", "")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted a configuration without credentials")
	}
}

func TestLoadRejectsMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Fatal("Load() accepted a missing file")
	}
}

func TestLoadRejectsUnsafeTracingEndpoint(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML)
	t.Setenv("TRACING_ENABLED", "true")
	t.Setenv("TRACING_ENDPOINT", "ftp://collector.example/traces")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted a non-HTTP tracing endpoint")
	}
}

func TestLoadRejectsMTLSWithoutTLS(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML)
	t.Setenv("MTLS_ENABLED", "true")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted mTLS without TLS")
	}
}

func TestLoadRejectsPostgresWithoutURL(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML)
	t.Setenv("STORAGE_BACKEND", "postgres")
	t.Setenv("DATABASE_URL", "")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted postgres without a database URL")
	}
}

func writeConfigFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func validBaseConfig() Config {
	return Config{
		Server: ServerConfig{
			Host:                       "127.0.0.1",
			Port:                       50051,
			MaxConns:                   100,
			Timeout:                    30 * time.Second,
			MaxRecvMessageMB:          4,
			MaxSendMessageMB:          4,
			MaxUsers:                  1000,
			MaxStreamMessages:         100,
			MaxStringBytes:            1024,
			StreamInterval:            time.Millisecond,
			RateLimitPerSecond:         100,
			RateLimitBurst:             200,
			PerClientRateLimitPerSecond: 50,
			PerClientRateLimitBurst:     100,
		},
		TLS: TLSConfig{Enabled: false},
		Auth: AuthConfig{
			JWTSecret: "this-is-a-long-enough-secret",
			APIKeys:   []string{},
		},
		Logging: LoggingConfig{Level: "info", Format: "json"},
		Tracing: TracingConfig{Enabled: false, ServiceName: "test"},
		Metrics: MetricsConfig{Enabled: false, Port: 9090},
		Health:  HealthConfig{Port: 8080},
		Storage: StorageConfig{
			Backend:         "memory",
			MaxOpenConns:    10,
			MaxIdleConns:    5,
			ConnMaxLifetime: 5 * time.Minute,
			PingTimeout:     5 * time.Second,
			RetryAttempts:   3,
			RetryBackoff:    250 * time.Millisecond,
			RetryMaxBackoff: 5 * time.Second,
			Cache:           CacheConfig{Enabled: false},
		},
		Client: ClientConfig{Address: "127.0.0.1:50051"},
	}
}

func TestValidateNilConfig(t *testing.T) {
	t.Parallel()
	var c *Config
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() on nil config: expected error, got nil")
	}
}

func TestValidateEmptyHost(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Server.Host = ""
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with empty host: expected error, got nil")
	}
	c.Server.Host = "   "
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with whitespace host: expected error, got nil")
	}
}

func TestValidatePortOutOfRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		port int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too high", 65536},
		{"way too high", 1000000},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := validBaseConfig()
			c.Server.Port = tt.port
			if err := c.Validate(); err == nil {
				t.Fatalf("Validate() with port=%d: expected error, got nil", tt.port)
			}
		})
	}
}

func TestValidateMaxConnsZero(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Server.MaxConns = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with max_conns=0: expected error, got nil")
	}
	c.Server.MaxConns = -1
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with negative max_conns: expected error, got nil")
	}
}

func TestValidateTimeoutZero(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Server.Timeout = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with timeout=0: expected error, got nil")
	}
	c.Server.Timeout = -time.Second
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with negative timeout: expected error, got nil")
	}
}

func TestValidateMessageLimitsZero(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Server.MaxRecvMessageMB = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with max_recv_message_mb=0: expected error, got nil")
	}
	c.Server.MaxRecvMessageMB = 4
	c.Server.MaxSendMessageMB = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with max_send_message_mb=0: expected error, got nil")
	}
}

func TestValidateResourceLimitsZero(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		mut  func(*Config)
	}{
		{"max_users zero", func(c *Config) { c.Server.MaxUsers = 0 }},
		{"max_users negative", func(c *Config) { c.Server.MaxUsers = -5 }},
		{"max_stream_messages zero", func(c *Config) { c.Server.MaxStreamMessages = 0 }},
		{"max_string_bytes zero", func(c *Config) { c.Server.MaxStringBytes = 0 }},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := validBaseConfig()
			tt.mut(&c)
			if err := c.Validate(); err == nil {
				t.Fatalf("Validate(): expected error, got nil")
			}
		})
	}
}

func TestValidateStreamIntervalZero(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Server.StreamInterval = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with stream_interval=0: expected error, got nil")
	}
	c.Server.StreamInterval = -time.Second
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with negative stream_interval: expected error, got nil")
	}
}

func TestValidateRateLimitZero(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Server.RateLimitPerSecond = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with rate_limit_per_second=0: expected error, got nil")
	}
	c.Server.RateLimitPerSecond = 100
	c.Server.RateLimitBurst = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with rate_limit_burst=0: expected error, got nil")
	}
}

func TestValidateTLSCertMissing(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.TLS.Enabled = true
	c.TLS.CertFile = ""
	c.TLS.KeyFile = "/some/key.pem"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with TLS enabled and empty cert_file: expected error, got nil")
	}
}

func TestValidateTLSKeyMissing(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.TLS.Enabled = true
	c.TLS.CertFile = "/some/cert.pem"
	c.TLS.KeyFile = ""
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with TLS enabled and empty key_file: expected error, got nil")
	}
}

func TestValidateMTLSNoTLS(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.TLS.Enabled = false
	c.TLS.MTLS.Enabled = true
	c.TLS.MTLS.ClientCAFile = "/some/ca.pem"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with mTLS but no TLS: expected error, got nil")
	}
}

func TestValidateMTLSNoCA(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.TLS.Enabled = true
	c.TLS.CertFile = "/some/cert.pem"
	c.TLS.KeyFile = "/some/key.pem"
	c.TLS.MTLS.Enabled = true
	c.TLS.MTLS.ClientCAFile = ""
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with mTLS and empty client_ca_file: expected error, got nil")
	}
	c.TLS.MTLS.ClientCAFile = "   "
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with mTLS and whitespace client_ca_file: expected error, got nil")
	}
}

func TestValidateAPIKeyTooShort(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Auth.APIKeys = []string{"short"}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with too-short api_key: expected error, got nil")
	}
	c.Auth.APIKeys = []string{"this-key-is-long-enough-ok", "bad"}
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with mixed-length api_keys: expected error, got nil")
	}
}

func TestValidateInvalidLogLevel(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Logging.Level = "critical"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with invalid log level: expected error, got nil")
	}
}

func TestValidateInvalidLogFormat(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Logging.Format = "xml"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with invalid log format: expected error, got nil")
	}
}

func TestValidateEmptyLogLevel(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Logging.Level = ""
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with empty log level: expected error, got nil")
	}
}

func TestValidateTracingBadEndpoint(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Tracing.Enabled = true
	c.Tracing.Endpoint = "://no-scheme"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with bad tracing endpoint: expected error, got nil")
	}
}

func TestValidateTracingRelativeEndpoint(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Tracing.Enabled = true
	c.Tracing.Endpoint = "/relative/path"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with relative tracing endpoint: expected error, got nil")
	}
	c.Tracing.Endpoint = "ftp://collector.example/traces"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with ftp tracing endpoint: expected error, got nil")
	}
}

func TestValidateMetricsPortOutOfRange(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Metrics.Enabled = true
	c.Metrics.Port = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with metrics.port=0: expected error, got nil")
	}
	c.Metrics.Port = 70000
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with metrics.port too high: expected error, got nil")
	}
}

func TestValidateHealthPortOutOfRange(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Health.Port = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with health.port=0: expected error, got nil")
	}
	c.Health.Port = 70000
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with health.port too high: expected error, got nil")
	}
}

func TestValidateStorageBackendUnknown(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Storage.Backend = "redis"
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with redis backend: expected error, got nil")
	}
	c.Storage.Backend = ""
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with empty backend: expected error, got nil")
	}
}

func TestValidateStoragePoolInvalid(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Storage.MaxIdleConns = 20
	c.Storage.MaxOpenConns = 10
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with max_idle > max_open: expected error, got nil")
	}
}

func TestValidateStorageNegativePool(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Storage.MaxOpenConns = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with max_open_conns=0: expected error, got nil")
	}
	c.Storage.MaxOpenConns = 10
	c.Storage.MaxIdleConns = -1
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with negative max_idle_conns: expected error, got nil")
	}
}

func TestValidatePostgresWithoutURL(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Storage.Backend = "postgres"
	c.Storage.URL = ""
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with postgres backend and no URL: expected error, got nil")
	}
	c.Storage.URL = "   "
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with whitespace URL: expected error, got nil")
	}
}

func TestValidateStorageRetry(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Storage.RetryAttempts = -1
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with negative retry_attempts: expected error, got nil")
	}
	c.Storage.RetryAttempts = 0
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with retry_attempts=0: unexpected error = %v", err)
	}
	c.Storage.RetryBackoff = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with zero retry_backoff: expected error, got nil")
	}
	c.Storage.RetryBackoff = -time.Millisecond
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with negative retry_backoff: expected error, got nil")
	}
	c.Storage.RetryBackoff = 100 * time.Millisecond
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with valid retry config: unexpected error = %v", err)
	}
	c.Storage.RetryMaxBackoff = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with zero retry_max_backoff: expected error, got nil")
	}
	c.Storage.RetryMaxBackoff = -time.Second
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with negative retry_max_backoff: expected error, got nil")
	}
	c.Storage.RetryMaxBackoff = 5 * time.Second
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with valid retry_max_backoff: unexpected error = %v", err)
	}
}

func TestValidateClientAddressEmpty(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Client.Address = ""
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with empty client.address: expected error, got nil")
	}
}

func TestValidateMetricsPortEqualsHealthPort(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Metrics.Enabled = true
	c.Metrics.Port = 8080
	c.Health.Port = 8080
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with metrics.port == health.port: expected error, got nil")
	}
}

func TestApplyEnvironmentNoVars(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	original := c

	// Truly unset all environment variables applyEnvironment reads. We
	// snapshot any pre-existing values and restore them on cleanup so we
	// don't affect sibling tests.
	envVars := []string{
		"SERVER_HOST", "SERVER_PORT", "SERVER_MAX_CONNS", "SERVER_TIMEOUT",
		"SERVER_MAX_RECV_MESSAGE_MB", "SERVER_MAX_SEND_MESSAGE_MB",
		"SERVER_MAX_USERS", "SERVER_MAX_STREAM_MESSAGES", "SERVER_MAX_STRING_BYTES",
		"SERVER_STREAM_INTERVAL", "SERVER_RATE_LIMIT_PER_SECOND", "SERVER_RATE_LIMIT_BURST",
		"TLS_ENABLED", "TLS_CERT_FILE", "TLS_KEY_FILE", "TLS_CA_FILE",
		"TLS_CLIENT_CERT_FILE", "TLS_CLIENT_KEY_FILE",
		"MTLS_ENABLED", "MTLS_CLIENT_CA_FILE",
		"JWT_SECRET", "AUTH_API_KEYS",
		"LOG_LEVEL", "LOG_FORMAT",
		"TRACING_ENABLED", "TRACING_ENDPOINT", "TRACING_SERVICE_NAME",
		"METRICS_ENABLED", "METRICS_PORT", "HEALTH_PORT",
		"CLIENT_ADDRESS", "CLIENT_SERVER_NAME", "CLIENT_AUTH_TOKEN",
		"STORAGE_BACKEND", "DATABASE_URL",
		"STORAGE_MAX_OPEN_CONNS", "STORAGE_MAX_IDLE_CONNS",
		"STORAGE_CONN_MAX_LIFETIME", "STORAGE_PING_TIMEOUT",
		"STORAGE_RETRY_ATTEMPTS", "STORAGE_RETRY_BACKOFF",
	}
	for _, k := range envVars {
		if prev, ok := os.LookupEnv(k); ok {
			t.Cleanup(func() { _ = os.Setenv(k, prev) })
		} else {
			t.Cleanup(func() { _ = os.Unsetenv(k) })
		}
		_ = os.Unsetenv(k)
	}

	if err := applyEnvironment(&c); err != nil {
		t.Fatalf("applyEnvironment() error = %v", err)
	}
	if c.Server.Host != original.Server.Host {
		t.Errorf("host changed: %q vs %q", c.Server.Host, original.Server.Host)
	}
	if c.Server.Port != original.Server.Port {
		t.Errorf("port changed: %d vs %d", c.Server.Port, original.Server.Port)
	}
	if c.Server.MaxConns != original.Server.MaxConns {
		t.Errorf("max_conns changed: %d vs %d", c.Server.MaxConns, original.Server.MaxConns)
	}
	if c.Server.Timeout != original.Server.Timeout {
		t.Errorf("timeout changed: %v vs %v", c.Server.Timeout, original.Server.Timeout)
	}
	if c.Auth.JWTSecret != original.Auth.JWTSecret {
		t.Errorf("jwt_secret changed: %q vs %q", c.Auth.JWTSecret, original.Auth.JWTSecret)
	}
	if c.Logging.Level != original.Logging.Level {
		t.Errorf("log level changed: %q vs %q", c.Logging.Level, original.Logging.Level)
	}
	if c.Logging.Format != original.Logging.Format {
		t.Errorf("log format changed: %q vs %q", c.Logging.Format, original.Logging.Format)
	}
}

func TestLoadRejectsInvalidEnvInt(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML)
	t.Setenv("SERVER_MAX_CONNS", "not-a-number")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted a non-integer SERVER_MAX_CONNS")
	}
}

func TestLoadRejectsInvalidEnvBool(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML)
	t.Setenv("TLS_ENABLED", "not-a-bool")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted a non-boolean TLS_ENABLED")
	}
}

func TestLoadRejectsInvalidEnvDuration(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML)
	t.Setenv("SERVER_TIMEOUT", "not-a-duration")
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted a non-duration SERVER_TIMEOUT")
	}
}

func TestLoadAcceptsValidEnv(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML)
	t.Setenv("TLS_ENABLED", "false")
	t.Setenv("METRICS_ENABLED", "true")
	if _, err := Load(path); err != nil {
		t.Fatalf("Load() with valid env overrides: error = %v", err)
	}
}

const validConfigYAML = `
server:
  host: "127.0.0.1"
  port: 50051
  max_conns: 100
  timeout: 30s
  max_recv_message_mb: 4
  max_send_message_mb: 4
  max_users: 1000
  max_stream_messages: 100
  max_string_bytes: 1024
  stream_interval: 1ms
  rate_limit_per_second: 100
  rate_limit_burst: 200
  per_client_rate_limit_per_second: 50
  per_client_rate_limit_burst: 100
tls:
  enabled: false
auth:
  jwt_secret: "test-secret"
  api_keys: []
logging:
  level: "info"
  format: "json"
tracing:
  enabled: false
  service_name: "test"
metrics:
  enabled: false
  port: 9090
health:
  port: 8080
storage:
  backend: "memory"
  max_open_conns: 10
  max_idle_conns: 5
  conn_max_lifetime: 5m
  ping_timeout: 5s
  retry_attempts: 3
  retry_backoff: 250ms
  retry_max_backoff: 5s
reflection: false
client:
  address: "127.0.0.1:50051"
`

func TestValidateGatewayPortOutOfRange(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Gateway.Enabled = true
	c.Gateway.Port = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with gateway.port=0: expected error, got nil")
	}
	c.Gateway.Port = 70000
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with gateway.port too high: expected error, got nil")
	}
}

func TestValidateGatewayPortConflictsWithHealth(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Gateway.Enabled = true
	c.Gateway.Port = 8080
	c.Health.Port = 8080
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with gateway.port == health.port: expected error, got nil")
	}
}

func TestValidateGatewayPortConflictsWithMetrics(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Gateway.Enabled = true
	c.Gateway.Port = 9090
	c.Metrics.Enabled = true
	c.Metrics.Port = 9090
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with gateway.port == metrics.port: expected error, got nil")
	}
}

func TestValidateGatewayDisabledNoPortCheck(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Gateway.Enabled = false
	c.Gateway.Port = 0
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with disabled gateway: unexpected error = %v", err)
	}
}

func TestValidateCacheEnabledNoTTL(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Storage.Cache.Enabled = true
	c.Storage.Cache.TTL = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with cache enabled and TTL=0: expected error, got nil")
	}
	c.Storage.Cache.TTL = -time.Second
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with cache enabled and negative TTL: expected error, got nil")
	}
}

func TestValidateCacheDisabledNoTTLCheck(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Storage.Cache.Enabled = false
	c.Storage.Cache.TTL = 0
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with disabled cache: unexpected error = %v", err)
	}
}

func TestValidateCircuitBreakerEnabledInvalidThreshold(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Storage.CircuitBreaker.Enabled = true
	c.Storage.CircuitBreaker.FailureThreshold = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with circuit_breaker enabled and failure_threshold=0: expected error, got nil")
	}
	c.Storage.CircuitBreaker.FailureThreshold = 5
	c.Storage.CircuitBreaker.SuccessThreshold = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with circuit_breaker enabled and success_threshold=0: expected error, got nil")
	}
	c.Storage.CircuitBreaker.SuccessThreshold = 2
	c.Storage.CircuitBreaker.OpenTimeout = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with circuit_breaker enabled and open_timeout=0: expected error, got nil")
	}
	c.Storage.CircuitBreaker.OpenTimeout = time.Minute
	c.Storage.CircuitBreaker.MaxHalfOpenRequests = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with circuit_breaker enabled and max_half_open_requests=0: expected error, got nil")
	}
}

func TestValidateCircuitBreakerDisabledNoCheck(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Storage.CircuitBreaker.Enabled = false
	c.Storage.CircuitBreaker.FailureThreshold = 0
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate() with disabled circuit_breaker: unexpected error = %v", err)
	}
}

func TestValidatePerClientRateLimitZero(t *testing.T) {
	t.Parallel()
	c := validBaseConfig()
	c.Server.PerClientRateLimitPerSecond = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with per_client_rate_limit_per_second=0: expected error, got nil")
	}
	c.Server.PerClientRateLimitPerSecond = 50
	c.Server.PerClientRateLimitBurst = 0
	if err := c.Validate(); err == nil {
		t.Fatal("Validate() with per_client_rate_limit_burst=0: expected error, got nil")
	}
}

func TestLoadValidConfigWithGatewayFull(t *testing.T) {
	path := writeConfigFile(t, `
server:
  host: "127.0.0.1"
  port: 50051
  max_conns: 100
  timeout: 30s
  max_recv_message_mb: 4
  max_send_message_mb: 4
  max_users: 1000
  max_stream_messages: 100
  max_string_bytes: 1024
  stream_interval: 1ms
  rate_limit_per_second: 100
  rate_limit_burst: 200
  per_client_rate_limit_per_second: 50
  per_client_rate_limit_burst: 100
tls:
  enabled: false
auth:
  jwt_secret: "test-secret"
  api_keys: []
logging:
  level: "info"
  format: "json"
tracing:
  enabled: false
  service_name: "test"
metrics:
  enabled: true
  port: 9090
health:
  port: 8080
gateway:
  enabled: true
  port: 7070
storage:
  backend: "memory"
  max_open_conns: 10
  max_idle_conns: 5
  conn_max_lifetime: 5m
  ping_timeout: 5s
  retry_attempts: 3
  retry_backoff: 250ms
  retry_max_backoff: 5s
reflection: false
client:
  address: "127.0.0.1:50051"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Gateway.Enabled || cfg.Gateway.Port != 7070 {
		t.Fatalf("unexpected gateway config: %#v", cfg.Gateway)
	}
}

func TestLoadValidConfigWithCache(t *testing.T) {
	path := writeConfigFile(t, `
server:
  host: "127.0.0.1"
  port: 50051
  max_conns: 100
  timeout: 30s
  max_recv_message_mb: 4
  max_send_message_mb: 4
  max_users: 1000
  max_stream_messages: 100
  max_string_bytes: 1024
  stream_interval: 1ms
  rate_limit_per_second: 100
  rate_limit_burst: 200
  per_client_rate_limit_per_second: 50
  per_client_rate_limit_burst: 100
tls:
  enabled: false
auth:
  jwt_secret: "test-secret"
  api_keys: []
logging:
  level: "info"
  format: "json"
tracing:
  enabled: false
  service_name: "test"
metrics:
  enabled: false
  port: 9090
health:
  port: 8080
storage:
  backend: "memory"
  max_open_conns: 10
  max_idle_conns: 5
  conn_max_lifetime: 5m
  ping_timeout: 5s
  retry_attempts: 3
  retry_backoff: 250ms
  retry_max_backoff: 5s
  cache:
    enabled: true
    ttl: 1m
reflection: false
client:
  address: "127.0.0.1:50051"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Storage.Cache.Enabled || cfg.Storage.Cache.TTL != time.Minute {
		t.Fatalf("unexpected cache config: %#v", cfg.Storage.Cache)
	}
}

func TestLoadValidConfigWithCircuitBreaker(t *testing.T) {
	path := writeConfigFile(t, `
server:
  host: "127.0.0.1"
  port: 50051
  max_conns: 100
  timeout: 30s
  max_recv_message_mb: 4
  max_send_message_mb: 4
  max_users: 1000
  max_stream_messages: 100
  max_string_bytes: 1024
  stream_interval: 1ms
  rate_limit_per_second: 100
  rate_limit_burst: 200
  per_client_rate_limit_per_second: 50
  per_client_rate_limit_burst: 100
tls:
  enabled: false
auth:
  jwt_secret: "test-secret"
  api_keys: []
logging:
  level: "info"
  format: "json"
tracing:
  enabled: false
  service_name: "test"
metrics:
  enabled: false
  port: 9090
health:
  port: 8080
storage:
  backend: "memory"
  max_open_conns: 10
  max_idle_conns: 5
  conn_max_lifetime: 5m
  ping_timeout: 5s
  retry_attempts: 3
  retry_backoff: 250ms
  retry_max_backoff: 5s
  circuit_breaker:
    enabled: true
    failure_threshold: 5
    success_threshold: 2
    open_timeout: 30s
    max_half_open_requests: 3
reflection: false
client:
  address: "127.0.0.1:50051"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.Storage.CircuitBreaker.Enabled || cfg.Storage.CircuitBreaker.FailureThreshold != 5 {
		t.Fatalf("unexpected circuit_breaker config: %#v", cfg.Storage.CircuitBreaker)
	}
}

func TestLoadRejectsCacheEnabledWithoutTTL(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML+`
storage:
  backend: "memory"
  max_open_conns: 10
  max_idle_conns: 5
  conn_max_lifetime: 5m
  ping_timeout: 5s
  retry_attempts: 3
  retry_backoff: 250ms
  retry_max_backoff: 5s
  cache:
    enabled: true
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted cache enabled without TTL")
	}
}

func TestLoadRejectsConfigYAMLWithGatewayPortConflict(t *testing.T) {
	path := writeConfigFile(t, validConfigYAML+`
gateway:
  enabled: true
  port: 8080
`)
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted gateway port conflicting with health port")
	}
}

func TestApplyEnvironmentGateway(t *testing.T) {
	c := validBaseConfig()
	t.Setenv("GATEWAY_ENABLED", "true")
	t.Setenv("GATEWAY_PORT", "7070")
	if err := applyEnvironment(&c); err != nil {
		t.Fatalf("applyEnvironment() error = %v", err)
	}
	if !c.Gateway.Enabled || c.Gateway.Port != 7070 {
		t.Fatalf("gateway env override not applied: %#v", c.Gateway)
	}
}

func TestApplyEnvironmentCache(t *testing.T) {
	c := validBaseConfig()
	t.Setenv("CACHE_ENABLED", "true")
	t.Setenv("CACHE_TTL", "2m")
	if err := applyEnvironment(&c); err != nil {
		t.Fatalf("applyEnvironment() error = %v", err)
	}
	if !c.Storage.Cache.Enabled || c.Storage.Cache.TTL != 2*time.Minute {
		t.Fatalf("cache env override not applied: %#v", c.Storage.Cache)
	}
}

func TestApplyEnvironmentCircuitBreaker(t *testing.T) {
	c := validBaseConfig()
	t.Setenv("CIRCUIT_BREAKER_ENABLED", "true")
	if err := applyEnvironment(&c); err != nil {
		t.Fatalf("applyEnvironment() error = %v", err)
	}
	if !c.Storage.CircuitBreaker.Enabled {
		t.Fatal("circuit_breaker env override not applied")
	}
}
