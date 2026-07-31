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
