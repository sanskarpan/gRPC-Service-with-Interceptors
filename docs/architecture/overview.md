# Architecture overview

The service is a single Go binary that owns three listeners — gRPC, health HTTP
and metrics HTTP — plus an optional OTLP trace exporter.

```mermaid
flowchart LR
  client[gRPC client] -- TLS/mTLS --> chain
  subgraph chain[Interceptor chain]
    direction TB
    r[recovery] --> rid[request-id] --> log[logging] --> met[metrics]
    met --> rl[global rate limit] --> auth[auth] --> pcl[per-client rate limit] --> to[timeout]
  end
  to --> svc[UserService]
  svc --> repo{Repository}
  repo --> mem[(memory)]
  repo --> pg[(PostgreSQL)]
  pg --> cb[circuit breaker] --> cache[read-through cache]
```

## Listeners

| Listener | Port (default) | Purpose |
| --- | --- | --- |
| gRPC | `50051` | UserService + Health, TLS/mTLS |
| Health HTTP | `8080` | `/healthz` (liveness), `/readyz` (readiness) |
| Metrics HTTP | `9090` | `/metrics` (Prometheus, OpenMetrics exemplars) |
| OTLP/HTTP | configured | trace export (optional, sampled) |

## Design decisions

The rationale for the transport, auth, storage, observability and deployment
choices is captured in the [Architecture Decision Records](../adr/README.md).
