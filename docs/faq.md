# FAQ

### Why is the browser frontend "demo-mode"?

The frontend is intentionally a request-builder UI that simulates responses
locally. Browsers cannot speak raw gRPC, and the service ships without a
gRPC-Web proxy by default. See [ADR-0005](adr/0005-no-browser-gateway.md).

### Is there a REST/JSON gateway?

A grpc-gateway is present in the codebase and can be enabled via the `gateway`
config block (disabled by default). The proto sources carry `google.api.http`
annotations for it. See the [API reference](reference/api.md) for the current
state and the mapping.

### memory vs postgres — which backend should I use?

`memory` is for local development and tests (fast, non-durable, per-process).
`postgres` is the production backend: durable, with migrations, pooling, retry,
a circuit breaker and a read-through cache. The shipped default config is
`memory`; set `storage.backend: postgres` (and `DATABASE_URL`) for production.

### Are writes idempotent?

Not by default — see the [Versioning & compatibility](contributing/versioning.md)
page for the write-idempotency caveat and guidance for retrying clients.

### Why is gRPC reflection off by default?

Reflection exposes the service schema and is disabled unless `reflection: true`
is set, to keep production surfaces minimal.
