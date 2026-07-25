# ADR 0002: Bounded In-Memory Backend for the Current Contract

## Context

The repository defines a small UserService with a local development backend and
a production persistence requirement. The original map was unbounded and
returned mutable protobuf pointers. A production backend needs a repeatable
schema, bounded connections, and an explicit failure contract.

## Decision

Keep the memory backend for development and deterministic tests, but put it
behind a repository interface that clones values, enforces bounds, honors
context cancellation, and fails closed after shutdown. Use the PostgreSQL
repository for production. It applies an embedded versioned migration under a
PostgreSQL advisory lock, keeps a bounded connection pool, and exposes a Ping
contract for readiness. Writes are explicitly non-idempotent until the public
protobuf contract adds idempotency keys.

## Consequences

The memory backend still loses state on restart, but PostgreSQL-backed
deployments retain users across restart and can share state across replicas.
The migration is intentionally additive and startup-applied; a future release
should move migration execution into a separately controlled deployment step
before introducing destructive schema changes.

## Alternatives considered

- Requiring PostgreSQL for local unit tests was rejected to keep tests fast and
  deterministic; real PostgreSQL integration tests are opt-in and run in CI
  or the Compose E2E target.
- Leaving the map unbounded was rejected because it allows straightforward
  memory exhaustion.
- Silent persistence to a local file was rejected because it would not provide
  multi-replica consistency or a recoverable migration story.
