# ADR 0003: Structured Telemetry and Separate Probe Endpoints

## Context

Operators need to distinguish caller errors from service failures, detect load
and latency regressions, and drain instances without terminating in-flight
requests. The original implementation had a no-op tracing package and no
health listener.

## Decision

Use zerolog for structured lifecycle logs, Prometheus counters/histograms and
active-stream gauges, and OpenTelemetry gRPC propagation with OTLP/HTTP export.
Recovery, logging, metrics, rate limiting, authentication, and timeout
interceptors are chained at the gRPC boundary. Expose liveness/readiness on a
dedicated HTTP listener and Prometheus metrics on a separate listener; probes
are unauthenticated but must be network-restricted.

## Consequences

Request payloads and credentials are not serialized into production logs.
Tracing export is optional and shutdown flushes with a bounded timeout. A
collector outage does not prevent the gRPC service from starting when tracing
is disabled. Metrics and health endpoints require firewall/network-policy
controls because they do not implement application authentication.

## Alternatives considered

- Jaeger’s deprecated exporter was rejected in favor of the current OTLP
  protocol.
- Embedding health checks in the gRPC auth path was rejected because
  orchestrators need a credential-free probe.
- Logging full protobuf payloads was rejected because request fields can hold
  PII or secrets.
