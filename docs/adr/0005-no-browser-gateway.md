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

## Alternatives considered

- Direct browser-to-native-gRPC calls were rejected because the transport is
  not browser-compatible.
- Adding an unplanned gateway was rejected because it would create a new trust
  boundary without an API and deployment design.
