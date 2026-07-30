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

## Secrets

Secrets are injected at runtime only (env / Kubernetes Secrets); the repository
contains no private keys or credentials, and `*.key` files are git-ignored.
Development certificates are generated locally via `make generate-certs`.

## Reporting

Please report vulnerabilities privately per `SECURITY.md`.
