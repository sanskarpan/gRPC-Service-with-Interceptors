# API & RPC reference

The service exposes `example.v1.UserService` plus the standard gRPC Health
service. The canonical schema is `proto/service.proto`.

## UserService

| RPC | Kind | Request → Response |
| --- | --- | --- |
| `GetUser` | unary | `GetUserRequest{id}` → `User` |
| `CreateUser` | unary | `CreateUserRequest{name,email,age}` → `User` |
| `UpdateUser` | unary | `UpdateUserRequest{id,name?,email?,age?}` → `User` |
| `DeleteUser` | unary | `DeleteUserRequest{id}` → `google.protobuf.Empty` |
| `ListUsers` | unary | `ListUsersRequest{page_size,page_token}` → `ListUsersResponse{users,next_page_token}` (keyset pagination, ordered by id) |
| `StreamUserEvents` | server-stream | `StreamUserEventsRequest{user_id,event_types}` → stream `UserEvent` |
| `CollectUserMetrics` | client-stream | stream `UserMetric` → `CollectMetricsResponse{count,...}` |
| `ChatStream` | bidi-stream | stream `ChatMessage` ↔ stream `ChatMessage` |

`UpdateUser` treats empty `name`/`email` and `age == 0` as "not provided"; see
[Versioning & compatibility](../contributing/versioning.md) for the field-mask
and idempotency caveats. Error mapping is documented under
[Error codes](error-codes.md).

## Authentication

Every method except the Health checks requires a credential, supplied as gRPC
metadata:

- `x-api-key: <key>`, or
- `authorization: Bearer <hs256-jwt>`

## REST / gRPC-Gateway

`proto/service.proto` carries `google.api.http` annotations (e.g.
`GET /v1/users/{id}`) and the server can expose a JSON gateway when the
`gateway` config block is enabled (disabled by default). The annotations define
the REST mapping for each RPC; when the gateway is off, only gRPC is served.

!!! warning "Documentation note"
    Older design notes (ADR-0005) described the service as gRPC-only with no
    HTTP annotations. The proto and server code now include an optional gateway;
    this reference reflects the current source of truth.

## Health

`grpc.health.v1.Health/Check` and `/Watch` are unauthenticated and back the
Kubernetes gRPC health probes.
