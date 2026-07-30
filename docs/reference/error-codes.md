# Error codes

Business and storage errors are mapped to standard gRPC status codes:

| Condition | gRPC code |
| --- | --- |
| Missing/invalid field, empty id, oversized string | `InvalidArgument` |
| User not found | `NotFound` |
| Duplicate create (unique violation) | `AlreadyExists` |
| Missing/invalid credential | `Unauthenticated` |
| Rate/message/stream/capacity limit exceeded | `ResourceExhausted` |
| Backend temporarily unavailable / circuit open | `Unavailable` |
| Deadline exceeded | `DeadlineExceeded` |
| Unexpected/internal failure (incl. recovered panic) | `Internal` |

Clients should treat `Unavailable` and `ResourceExhausted` as safe to retry
(with backoff); other codes are terminal. See
[Versioning & compatibility](../contributing/versioning.md) for retry and
idempotency guidance.
