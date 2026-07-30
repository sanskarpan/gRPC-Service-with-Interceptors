# Versioning & compatibility

## Semantic versioning

Releases follow SemVer. The gRPC API package is versioned (`example.v1`); a
breaking change to an RPC or message is a major-version event.

## API compatibility

Backward-compatible evolution (adding RPCs, adding fields) is preferred. Proto
changes should preserve field numbers and avoid changing the meaning of an
existing field.

## Write idempotency

`CreateUser` is **not** idempotent: the server assigns the id, so a retried
create produces a duplicate. Clients that retry mutations should either use the
provided client-retry interceptor's explicit opt-in (idempotent methods only)
or de-duplicate on their side. `Update`/`Delete` on a specific id are
naturally idempotent.

`UpdateUser` uses a "provided-if-non-empty" convention: empty `name`/`email`
and `age == 0` are treated as unset. To clear a field or set age to zero, a
future field-mask-based update would be required.
