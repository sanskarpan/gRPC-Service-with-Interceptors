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

## Scaling reads: read/write split (extension seam)

The `Repository` interface plus the decorator pattern (circuit breaker, cache)
make a read/write split a clean, isolated future change rather than a rewrite. A
`ReadWriteSplitRepository` would wrap two `PostgresRepository` instances — a
primary pool and a read-replica pool (`storage.replica_url`) — routing `Get` and
`List` to the replica and all mutations to the primary.

The one correctness hazard is read-your-writes under replication lag (a
`CreateUser` immediately followed by `GetUser` could miss on a lagging replica).
Handle it by pinning post-write reads to the primary for a short window, or by
serving them from the existing read-through cache. This is deliberately **not**
implemented today: there is no demonstrated read-throughput need, the cache
already offloads reads, and adding replica routing without a real requirement
would make the reference subtly wrong. Multi-region is out of scope for a single
service.
