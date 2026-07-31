# Service level objectives

## Availability SLO

**Objective:** 99.9% of gRPC requests succeed over a rolling 30-day window.

- **SLI:** `1 - (errors / requests)`, where errors are non-OK terminal statuses,
  measured from `grpc_server_total_errors` and `grpc_server_total_requests`.
- **Error budget:** 0.1% of requests (about 43 minutes of full outage per 30 days).

Availability excludes client-caused statuses where appropriate — tune the SLI
(e.g. exclude `InvalidArgument`) to match how you attribute failures.

## Burn-rate alerting

Rather than a static threshold, the alerts in
`test/deployment/prometheus/alerting-rules.yml` use **multi-window,
multi-burn-rate** detection (Google SRE workbook):

| Alert | Condition | Action |
| --- | --- | --- |
| `SLOErrorBudgetFastBurn` | >14.4x budget over **1h and 5m** | page (critical) |
| `SLOErrorBudgetSlowBurn` | >6x budget over **6h and 30m** | ticket (warning) |

Requiring the long and short windows to agree makes the fast-burn alert both
sensitive (fires within minutes of a real outage) and stable (a brief spike that
clears does not page). The rules are validated in CI with `promtool test rules`.

## Latency

Track P50/P90/P99 from `grpc_server_request_duration_seconds` (recording rules
`grpc:latency:p50|p90|p99`). Set a latency objective per method and alert on the
P99 recording rule; the histogram carries `trace_id` exemplars so a breaching
bucket links straight to a representative trace.
