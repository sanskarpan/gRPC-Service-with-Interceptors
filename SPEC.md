# Service Contract

This file is the concise implementation contract for the repository. The
operator-facing setup, configuration, deployment, and recovery procedures live
in [README.md](README.md) and [docs/operations/runbook.md](docs/operations/runbook.md).

## Transport

- The primary API is gRPC over HTTP/2.
- TLS is enabled by the checked-in baseline configuration and must fail closed
  if its certificate or key cannot be loaded.
- mTLS is available through `tls.mtls.enabled` and requires a valid client CA.
- Reflection is disabled by default and is only enabled explicitly.
- `/healthz` and `/readyz` are unauthenticated HTTP probe endpoints.
- `/metrics` is a Prometheus endpoint and must be restricted at the network
  boundary in production.
- REST/gRPC-Gateway is not part of the current contract. The protobuf sources
  intentionally do not contain HTTP annotations.

## RPCs

`example.UserService` exposes unary `GetUser`, `CreateUser`, `UpdateUser`, and
`DeleteUser` methods; server-streaming `StreamUserEvents`; client-streaming
`CollectUserMetrics`; and bidirectional `ChatStream`. The standard
`grpc.health.v1.Health` service is also registered.

All UserService methods require a configured API key (`x-api-key`) or a valid
HS256 JWT in `authorization: Bearer <token>`. JWTs must contain a future `exp`
claim. Health checks are public for orchestrator probes.

## State and limits

The service uses a repository interface. The memory implementation is bounded
and reserved for development and deterministic unit tests. The PostgreSQL
implementation applies a versioned, embedded migration, uses a bounded pool,
and is the production backend. Startup and readiness fail closed when
PostgreSQL is unavailable. The current write contract remains explicitly
non-idempotent because the protobufs do not yet carry idempotency keys; callers
must not blindly retry Create or Delete without an application-level policy.

All configured message, string, user-count, stream-count, timeout, and request
rate limits are enforced at the server boundary or service boundary.
