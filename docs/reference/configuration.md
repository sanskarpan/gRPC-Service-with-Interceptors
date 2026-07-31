# Configuration reference

`CONFIG_PATH` selects the YAML file for both binaries and defaults to
`configs/config.yaml`. Environment variables override YAML values. The server
refuses unknown YAML fields, invalid ranges, missing credentials, and missing
TLS paths when TLS is enabled.

## Environment variables

| Variable | Type | Default | Required | Meaning |
| --- | --- | --- | --- | --- |
| `CONFIG_PATH` | path | `configs/config.yaml` | no | Configuration file |
| `SERVER_HOST` | string | YAML | no | Bind address for gRPC/HTTP listeners |
| `SERVER_PORT` | int | `50051` | no | gRPC port |
| `SERVER_MAX_CONNS` | int | `100` | no | Max concurrent gRPC streams |
| `SERVER_TIMEOUT` | duration | `30s` | no | Per-request and drain deadline |
| `SERVER_MAX_RECV_MESSAGE_MB` | int | `4` | no | Max inbound message size |
| `SERVER_MAX_SEND_MESSAGE_MB` | int | `4` | no | Max outbound message size |
| `SERVER_MAX_USERS` | int | `10000` | no | Repository capacity |
| `SERVER_MAX_STREAM_MESSAGES` | int | `100` | no | Per-stream message/event cap |
| `SERVER_MAX_STRING_BYTES` | int | `4096` | no | Max validated string size |
| `SERVER_RATE_LIMIT_PER_SECOND` | int | `100` | no | Process-wide admission rate |
| `SERVER_RATE_LIMIT_BURST` | int | `200` | no | Process-wide admission burst |
| `TLS_ENABLED` | bool | `true` | no | Verified TLS transport |
| `TLS_CERT_FILE` / `TLS_KEY_FILE` | path | `certs/server.*` | if TLS | Server cert/key |
| `TLS_CA_FILE` | path | `certs/ca.crt` | no | CA reference |
| `MTLS_ENABLED` | bool | `false` | no | Require and verify client certs |
| `AUTH_API_KEYS` | csv | — | one of | Accepted API keys (≥16 chars each) |
| `JWT_SECRET` | string | — | one of | HS256 secret (≥32 bytes when set) |
| `JWT_PUBLIC_KEY` | PEM | — | one of | RSA/EC (P-256) public key for RS256/ES256 JWTs |
| `AUTH_JWT_AUDIENCE` / `AUTH_JWT_ISSUER` | string | — | no | Optional JWT `aud`/`iss` binding |
| `STORAGE_BACKEND` | enum | `memory` | no | `memory` or `postgres` |
| `DATABASE_URL` | dsn | — | if postgres | PostgreSQL connection string |
| `TRACING_ENABLED` | bool | `false` | no | Enable OTLP export |
| `TRACING_ENDPOINT` | url | — | if tracing | OTLP/HTTP collector URL |
| `TRACING_SAMPLE_RATIO` | float | `1.0` | no | Head sampling probability [0,1] |

!!! note "Secrets"
    At least one of `AUTH_API_KEYS`, `JWT_SECRET`, or `JWT_PUBLIC_KEY` is
    required. Prefer `JWT_PUBLIC_KEY` (asymmetric RS256/ES256) in production so
    the signing key never leaves the issuer. Secrets must be
    injected at runtime; the checked-in config contains no credentials. See the
    [Security model](../operations/security.md).

## Tuning notes

- **Rate limits** — the global limiter protects the process; the per-client
  limiter fairly bounds each authenticated identity.
- **Trace sampling** — set `TRACING_SAMPLE_RATIO` below `1.0` at scale to cap
  trace volume; parent decisions are always honored.
- **Timeouts** — `SERVER_TIMEOUT` bounds unary handlers and the shutdown drain;
  it does not apply to long-lived streams.
