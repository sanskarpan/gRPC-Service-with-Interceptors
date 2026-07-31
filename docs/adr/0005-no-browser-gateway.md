# ADR 0005: Keep the Browser UI in Explicit Demo Mode

## Context

The repository contains a Next.js request-builder UI, but browsers cannot call
the native gRPC endpoint without a gRPC-Web or HTTP gateway, CORS policy, and a
browser-safe authentication design. None of those contracts existed.

## Decision

Keep the UI as a local simulator and label it visibly as demo mode. The Go
service remains the authoritative gRPC contract. A future browser integration
must introduce a separately reviewed gRPC-Web/gateway boundary rather than
quietly pretending that address, TLS, or metadata fields already connect.

## Consequences

The frontend is useful for interaction and request-shape demos, but its
responses are not evidence that the server is reachable or healthy. Any team
that needs a browser client must define gateway authentication, CORS,
timeouts, rate limits, and observability before removing the demo label.

## Relationship to the optional REST gateway

This ADR is specifically about the **browser** client. The service does ship an
optional server-side **gRPC-Gateway** (REST/JSON transcoding) behind the
`gateway` config block, **disabled by default**, with `google.api.http`
annotations in `proto/service.proto`. That gateway targets programmatic HTTP
clients on the cluster network — it is *not* a browser-safe path: it does not by
itself provide gRPC-Web framing, a CORS policy, or a browser authentication
design. Enabling it therefore does not change this decision; the browser UI
stays in demo mode until those browser-specific contracts are designed and
reviewed. See the [API reference](../reference/api.md) for the current gateway
state and mapping.

## Alternatives considered

- Direct browser-to-native-gRPC calls were rejected because the transport is
  not browser-compatible.
- Exposing the optional REST gateway *as the browser path* was rejected because
  it still lacks gRPC-Web/CORS/browser-auth; it is intended for server-side
  HTTP clients only.
