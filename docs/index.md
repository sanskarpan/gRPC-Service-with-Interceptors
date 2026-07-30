# gRPC Service with Interceptors

An authenticated Go gRPC service with bounded request handling, TLS/mTLS,
structured logs, Prometheus metrics, OpenTelemetry tracing, health probes,
circuit breaking, per-client rate limiting, and reproducible development and
deployment tooling.

```text
gRPC client ── TLS/mTLS ──► recovery → requestID → logging → metrics
                              → rate limit → auth → per-client limit → timeout
                                      │
                                      ▼
                             UserService (business logic)
                                      │
                         repository interface
                         ├─ memory (dev/test)
                         └─ PostgreSQL (production)  ── migrations, pool,
                                                        retry, circuit breaker,
                                                        graceful-degradation cache

HTTP :8080 ── /healthz, /readyz ──► liveness / readiness
HTTP :9090 ── /metrics ───────────► Prometheus (with trace-id exemplars)
OTLP/HTTP ────────────────────────► configured collector (optional, sampled)
```

## Highlights

- **Fail-closed auth** — API keys (constant-time) and HS256 JWT with optional
  audience/issuer binding.
- **Transport hardening** — TLS 1.2+ with optional mTLS; no `InsecureSkipVerify`.
- **Resilience** — process-wide + per-client rate limiting, panic recovery,
  server-owned timeouts, circuit breaker and a read-through cache over Postgres.
- **Observability** — RED metrics, DB-pool metrics, OTLP tracing, and
  `trace_id`/`span_id` correlation across logs, metrics exemplars and traces.
- **Operable** — graceful shutdown with drain, readiness gated on a real DB
  ping, hardened Kubernetes manifests (non-root, read-only rootfs, PDB, HPA,
  NetworkPolicy).

## Where to next

- New here? Start with the [Quickstart](getting-started/quickstart.md).
- Deploying? See the [Deployment guide](operations/deployment.md) and
  [Runbook](operations/runbook.md).
- Integrating? See the [API reference](reference/api.md) and
  [Configuration reference](reference/configuration.md).
