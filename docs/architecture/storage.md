# Storage backends

The service depends only on a `Repository` interface, selected by
`storage.backend`.

## memory

An in-process, mutex-guarded store with a bounded capacity. Fast and
non-durable; intended for local development and tests. Replicas do not share
state, so horizontal scaling requires the `postgres` backend.

## postgres

The production backend, layered as:

- **PostgresRepository** — parameterized SQL (no injection), a tuned
  connection pool, embedded up/down migrations, and retry with backoff on
  transient errors.
- **CircuitBreakerRepository** — opens on repeated failures and fails fast while
  the backend is unhealthy; recovers through a half-open probe window.
- **CacheRepository** — a bounded, TTL-based read-through cache that can serve
  stale reads when the backend is briefly unavailable.

Migrations live in `internal/storage/migrations` as paired `*.sql` (up) and
`*.down.sql` (down) files and run automatically on startup for the postgres
backend.
