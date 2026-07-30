# Observability

## Metrics

Prometheus metrics are exposed on `:9090/metrics` (OpenMetrics format, so
histogram exemplars are included).

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `grpc_server_total_requests` | counter | `method` | Requests entering the chain |
| `grpc_server_total_errors` | counter | `method`, `code` | Errors by status code |
| `grpc_server_request_duration_seconds` | histogram | `method` | Latency (with `trace_id` exemplars) |
| `grpc_server_active_streams` | gauge | — | Currently active server streams |
| `grpc_db_*` | gauge/counter | `instance` | Connection-pool stats |

Metric label cardinality is bounded: only the closed method/code sets are used
as labels — never user input.

### Alerting

Recording and alerting rules ship in
`test/deployment/prometheus/alerting-rules.yml` and are validated in CI with
`promtool check rules` and `promtool test rules`
(`alerting-rules_test.yml`), so the rules can never silently drift from the
emitted series.

## Tracing

OpenTelemetry traces are exported over OTLP/HTTP when `tracing.enabled` is set.
Sampling is head-based and parent-respecting; tune with
`TRACING_SAMPLE_RATIO`.

## Correlation

Every request log carries `request_id`, and — when a span is active —
`trace_id` and `span_id`. Latency histograms attach a `trace_id` exemplar. This
lets you pivot from a log line to the exact trace and from a dashboard latency
bucket to a representative trace.

## SLOs

Suggested starting SLOs (tune to your traffic): 99.9% availability and a P99
unary latency target; drive alerting from multi-window burn rates rather than
static thresholds.
