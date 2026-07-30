# Request lifecycle

Interceptors run in a fixed order. The ordering is deliberate:

1. **recovery** (outermost) — converts a handler panic into `codes.Internal`
   and logs the panic with method and request id, keeping the process alive.
2. **request-id** — accepts a validated inbound `x-request-id` or generates one,
   and propagates it in context and outbound metadata.
3. **logging** — emits bounded lifecycle fields (method, request_id, duration,
   code) plus `trace_id`/`span_id` when a span is active. No payloads are logged.
4. **metrics** — increments request/error counters and observes latency
   (with a `trace_id` exemplar) for **every** request, including auth failures.
5. **global rate limit** — a process-wide token bucket; rejects excess with
   `ResourceExhausted` before auth work is spent.
6. **auth** — fail-closed API-key/JWT verification; sets the client id in context.
7. **per-client rate limit** — a token bucket keyed by client id (empty id falls
   back to a single shared bucket, never unlimited).
8. **timeout** (innermost, unary) — wraps the handler in a server-owned deadline
   that never extends a shorter client deadline.

Streaming RPCs run the same chain minus the per-request timeout: streams are
legitimately long-lived and are bounded instead by the per-stream message cap.
