# Security model

## Trust boundaries

The gRPC listener is the only externally reachable RPC surface. The health and
metrics HTTP listeners are intended for the cluster network only and should not
be exposed publicly.

## Transport

- TLS 1.2 minimum; the client verifies the server certificate and hostname and
  never uses `InsecureSkipVerify`.
- Optional mutual TLS (`MTLS_ENABLED`) requires and verifies client
  certificates against a configured CA pool.

## Authentication

Fail-closed: every method except the Health checks requires a valid credential.

- **API keys** — compared in constant time; a minimum length is enforced.
- **HS256 JWT** — signature verified in constant time; `exp`/`nbf`/`iat`
  validated; optional `aud`/`iss` binding; the secret has a 32-byte minimum.
- **RS256 / ES256 JWT** — asymmetric verification against a configured PEM
  public key (`auth.jwt_public_key`). The verification path is selected by the
  token `alg` **and** the configured key type, which blocks the classic
  algorithm-confusion attack (an HS256 token signed with the public-key bytes is
  rejected because no HMAC secret is configured). Prefer this in production so
  the signing key never leaves the issuer.

## Authorization

After authentication, an optional scope-based policy (`auth.method_scopes`)
gates each method (any-of): a caller needs at least one of the scopes listed for
the method. JWT callers carry a standard `scope` claim; API-key callers use
`auth.api_key_scopes` (empty by default, so keys can only reach unscoped methods
until scopes are granted). Methods not in the policy are allowed for any
authenticated caller unless `auth.default_deny` is set; health checks are always
exempt so probes are never blocked. Denials are `PermissionDenied` and audited
with `event=authz`.

## Load shedding

Beyond token-bucket rate limiting, `server.max_in_flight_requests` bounds
concurrent unary requests with a semaphore and sheds excess as
`ResourceExhausted`, so a slow database cannot cause an unbounded goroutine or
resource pileup. Streams (bounded by MaxConcurrentStreams) and health checks are
exempt. Runtime log level can be changed live with `SIGHUP` (re-reads the config
file and applies only `logging.level`).

## Audit logging

Authentication decisions (allow and deny, with client id, method, request id,
and remote address) and every state-changing RPC (Create/Update/Delete, with the
terminal status code) are emitted as structured audit records marked
`audit=true`, so an operator or SIEM can select the audit stream with a single
field filter. Request payloads are never logged.

## Secrets

Secrets are injected at runtime only (env / Kubernetes Secrets); the repository
contains no private keys or credentials, and `*.key` files are git-ignored.
Development certificates are generated locally via `make generate-certs`.

## Reporting

Please report vulnerabilities privately per `SECURITY.md`.

## Data classification and PII

| Field | Classification | Notes |
| --- | --- | --- |
| `id` | pseudonymous | server-generated, not derived from PII |
| `name`, `email` | PII | user-provided |
| `age` | personal | user-provided |

Logs, the audit stream, and `google.rpc` error details **exclude request and
response bodies and gRPC metadata by design** — validation errors describe the
violated field (e.g. "must be a valid email address") without echoing the
submitted value. A guardrail test (`pkg/interceptors/pii_test.go`) fails the
build if a payload value ever reaches a log line. Encryption at rest is delegated
to the PostgreSQL/infrastructure layer (disk/volume encryption or TDE); set audit
log retention per your compliance requirements.
