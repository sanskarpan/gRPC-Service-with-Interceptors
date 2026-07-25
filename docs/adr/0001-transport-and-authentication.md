# ADR 0001: Fail-Closed TLS and Boundary Authentication

## Context

The service accepts network traffic carrying user operations. A plaintext
fallback or a permissive token check would turn configuration or credential
mistakes into an authorization bypass.

## Decision

TLS is explicit configuration and startup fails when enabled credentials cannot
be loaded. TLS uses at least TLS 1.2; mTLS uses `RequireAndVerifyClientCert`.
Every UserService RPC is authenticated by a constant-time API-key comparison or
HS256 JWT verification with a required future `exp` claim. Only standard health
RPCs are public. Reflection is opt-in.

## Consequences

Deployments must provision certificates and one authentication mode before
serving traffic. Health probes remain usable without credentials. JWT claims
and API-key support are intentionally narrow; asymmetric JWT algorithms,
audience/issuer policy, and an external identity provider are future contract
changes, not implicit behavior.

## Alternatives considered

- A plaintext fallback was rejected because it fails open during certificate
  rotation or mounting errors.
- Accepting any bearer token was rejected because it is not authentication.
- Making reflection public was rejected because it increases API discovery at
  the trust boundary.
