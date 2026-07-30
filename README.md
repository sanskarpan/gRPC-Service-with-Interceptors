# gRPC Service with Interceptors

An authenticated Go gRPC service with bounded request handling, TLS/mTLS,
structured logs, Prometheus metrics, OpenTelemetry tracing, health probes, and
reproducible development/deployment tooling.

📚 **Documentation site:** https://sanskarpan.github.io/gRPC-Service-with-Interceptors/

## Walkthrough

A full end-to-end walkthrough — starting the authenticated server, health and
readiness probes, the example client driving unary + streaming RPCs, Prometheus
metrics, and the request-builder UI:

[![Project walkthrough](docs/assets/walkthrough.gif)](docs/assets/walkthrough.mp4)

▶ **[Watch the full-quality MP4](docs/assets/walkthrough.mp4)** (terminal + UI, ~47s).
Reproduce the terminal part with `./scripts/demo/run.sh` (or
`go build -o bin/server ./cmd/server && go build -o bin/client ./cmd/client`).

### Frontend (request-builder UI)

| Method tree & editor | Unary create → `200 OK` |
| --- | --- |
| ![UI home](docs/assets/ui-home.png) | ![Create user response](docs/assets/ui-response.png) |
| **Filled request** | **Streaming RPC** |
| ![Create request](docs/assets/ui-create.png) | ![Stream events](docs/assets/ui-stream.png) |

The browser UI is an intentional demo-mode request builder (it simulates
responses locally); the real gRPC traffic in the walkthrough is server ↔ client.
See [Frontend](#frontend).

## System map

```mermaid
flowchart LR
  client([gRPC client]) -- TLS/mTLS --> chain
  subgraph chain[Interceptor chain]
    direction TB
    r[recovery] --> log[logging] --> met[metrics] --> rl[rate limit] --> auth[auth] --> to[timeout]
  end
  to --> svc[UserService]
  svc --> repo{repository}
  repo --> mem[(memory · dev/test)]
  repo --> pg[(PostgreSQL · prod)]
  client -. HTTP :8080 .-> health[/healthz · /readyz/]
  client -. HTTP :9090 .-> metrics[/metrics · Prometheus/]
  svc -. OTLP/HTTP .-> otel[(trace collector · optional)]
```

<details><summary>Text version</summary>

```text
gRPC client ── TLS/mTLS ──► recovery → logging → metrics → rate limit
                              → auth → timeout → UserService
                                      │
                         repository interface
                         ├─ memory (dev/test)
                         └─ PostgreSQL (production)

HTTP :8080 ── /healthz, /readyz ──► liveness/readiness
HTTP :9090 ── /metrics ───────────► Prometheus
OTLP/HTTP ────────────────────────► configured collector (optional)
```

</details>

The browser frontend is intentionally a demo-mode request-builder UI. It
simulates responses locally and does not send browser traffic to the gRPC
server; see [Frontend](#frontend). The service has no REST gateway; production
state is stored in PostgreSQL, while the memory backend is reserved for local
development and tests.

## Prerequisites

The CI contract pins Go `1.26.5` and Node.js `22.14.0`. The verified local
toolchain for this checkout is:

| Tool | Version |
| --- | --- |
| Go | 1.26.5 |
| Node.js | 22.14.0 in CI; 25.6.1 also passed locally |
| npm | 11.x |
| protoc | 33.4 |
| OpenSSL | 3.6.2 for the development certificate script |
| Docker Engine | 28.4.0 |
| Docker Compose | v2.39.4 |
| kubectl | v1.34.1 for manifest validation |

## Local setup

From the repository root:

```sh
make generate-certs
export AUTH_API_KEYS='local-api-key-change-me-12345' CLIENT_AUTH_TOKEN='local-api-key-change-me-12345'
go run ./cmd/server
```

The server listens on gRPC `:50051`, health `:8080`, and metrics `:9090`.
The generated certificates are development-only. Keep the private keys out of
source control and replace them with certificates from your organization in
production.

In another terminal, with the same environment variables, run the example
client:

```sh
go run ./cmd/client
```

The client runs unary, server-streaming, client-streaming, and bidirectional
streaming examples. Every operation uses a bounded context and configured TLS
verification.

## Configuration

`CONFIG_PATH` selects the YAML file for both binaries and defaults to
`configs/config.yaml`. Environment variables below override YAML values. The
server refuses unknown YAML fields, invalid ranges, missing credentials, and
missing TLS paths when TLS is enabled.

### Environment variables

| Variable | Type | Default | Required | Meaning |
| --- | --- | --- | --- | --- |
| `CONFIG_PATH` | path | `configs/config.yaml` | no | Configuration file |
| `SERVER_HOST` | string | YAML value | no | Bind address for gRPC/HTTP listeners |
| `SERVER_PORT` | int | `50051` | no | gRPC port |
| `SERVER_MAX_CONNS` | int | `100` | no | Maximum concurrent gRPC streams |
| `SERVER_TIMEOUT` | duration | `30s` | no | Server operation and drain deadline |
| `SERVER_MAX_RECV_MESSAGE_MB` | int | `4` | no | Maximum inbound gRPC message size |
| `SERVER_MAX_SEND_MESSAGE_MB` | int | `4` | no | Maximum outbound gRPC message size |
| `SERVER_MAX_USERS` | int | `10000` | no | Maximum user capacity enforced by the repository |
| `SERVER_MAX_STREAM_MESSAGES` | int | `100` | no | Per-stream message/event cap |
| `SERVER_MAX_STRING_BYTES` | int | `4096` | no | Maximum validated string size |
| `SERVER_STREAM_INTERVAL` | duration | `2s` | no | Server event emission interval |
| `SERVER_RATE_LIMIT_PER_SECOND` | int | `100` | no | Process-wide admission rate |
| `SERVER_RATE_LIMIT_BURST` | int | `200` | no | Process-wide admission burst |
| `TLS_ENABLED` | bool | `true` | no | Enable verified TLS transport |
| `TLS_CERT_FILE` | path | `certs/server.crt` | if TLS | Server certificate |
| `TLS_KEY_FILE` | path | `certs/server.key` | if TLS | Server private key |
| `TLS_CA_FILE` | path | `certs/ca.crt` | no | Configured CA reference |
| `TLS_CLIENT_CERT_FILE` | path | `certs/client.crt` | if mTLS client | Client certificate |
| `TLS_CLIENT_KEY_FILE` | path | `certs/client.key` | if mTLS client | Client private key |
| `MTLS_ENABLED` | bool | `false` | no | Require and verify client certificates |
| `MTLS_CLIENT_CA_FILE` | path | `certs/ca.crt` | if mTLS | Client CA bundle |
| `JWT_SECRET` | secret | empty | one auth mode | HS256 JWT signing secret |
| `AUTH_API_KEYS` | comma list | empty | one auth mode | Constant-time API keys |
| `LOG_LEVEL` | enum | `info` | no | `trace`, `debug`, `info`, `warn`, `error`, `fatal`, `panic` |
| `LOG_FORMAT` | enum | `json` | no | `json` or `console` |
| `TRACING_ENABLED` | bool | `false` | no | Enable OTLP/HTTP span export |
| `TRACING_ENDPOINT` | URL | empty | if tracing | OTLP trace endpoint, e.g. `http://jaeger:4318/v1/traces` |
| `TRACING_SERVICE_NAME` | string | `grpc-service` | if tracing | OpenTelemetry service name |
| `METRICS_ENABLED` | bool | `true` | no | Start the Prometheus listener |
| `METRICS_PORT` | int | `9090` | if metrics | Prometheus port |
| `HEALTH_PORT` | int | `8080` | yes | Liveness/readiness port |
| `CLIENT_ADDRESS` | host:port | `localhost:50051` | client | Example-client target |
| `CLIENT_SERVER_NAME` | string | `localhost` | client with TLS | TLS server-name verification value |
| `CLIENT_AUTH_TOKEN` | secret | empty | client auth | API key or JWT sent as Bearer metadata |
| `STORAGE_BACKEND` | enum | `memory` | no; postgres in production | `memory` for dev/test or `postgres` for durable state |
| `DATABASE_URL` | URL/secret | empty | if postgres | PostgreSQL connection URL; URL-encode credentials and inject from a secret |
| `STORAGE_MAX_OPEN_CONNS` | int | `10` | no | Per-process PostgreSQL connection ceiling |
| `STORAGE_MAX_IDLE_CONNS` | int | `5` | no | Per-process idle PostgreSQL connection ceiling |
| `STORAGE_CONN_MAX_LIFETIME` | duration | `5m` | no | Maximum lifetime of a PostgreSQL connection |
| `STORAGE_PING_TIMEOUT` | duration | `5s` | no | PostgreSQL startup/readiness ping deadline |

For production, inject secrets from a secret manager or orchestrator Secret;
do not put them in YAML, command history, images, or logs. Restrict access to
the metrics listener because it is intentionally unauthenticated for scraping.

## Tests and quality gates

```sh
make test                 # unit and in-process integration tests
make test-race            # race detector
make coverage-check       # atomic coverage and threshold check
make vet
make lint
make fuzz                 # 30-second parser fuzz smoke run
make benchmark
make proto-check          # regenerate and verify generated protobufs
make security             # govulncheck, module verification, npm audit
TEST_DATABASE_URL='postgres://...' make test-postgres # real PostgreSQL repository integration
make e2e                  # Compose PostgreSQL + TLS + all-RPC network E2E
```

The current CI coverage floor is 45% (total statements are around 49%); it is
intentionally a floor, not a claim that coverage alone proves correctness.
Critical network behavior is
covered by the server lifecycle/authentication integration test, while the
service and parser boundaries have table-driven, fuzz, concurrency, and
benchmark coverage.

## Frontend

The Next.js app runs independently on port `3001`:

![Frontend request-builder UI](docs/assets/frontend.png)

```sh
make frontend-install
make frontend-typecheck frontend-lint frontend-build
npm --prefix frontend run dev
```

It is a local UX/demo surface. The address, TLS toggle, metadata editor, and
request methods describe a future browser transport contract but currently do
not open a gRPC-Web connection. Do not use its simulated success responses as
an API health signal.

## Docker and Compose

Build the non-root image:

```sh
make generate-certs
docker build --tag grpc-service:local .
JWT_SECRET='replace-with-a-long-random-secret' \
POSTGRES_PASSWORD='replace-with-a-database-password' \
DATABASE_URL='postgres://grpc_service:replace-with-a-database-password@postgres:5432/grpc_service?sslmode=disable' \
docker compose up --build
```

Compose runs PostgreSQL with a named volume, waits for its health check, and
starts the server with the durable backend. It mounts development certificates
read-only and exposes gRPC `50051`, health `8080`, and metrics `9090`. For a
fully automated smoke test, run `make e2e`. To enable the optional observability
stack:

```sh
JWT_SECRET='replace-with-a-long-random-secret' \
POSTGRES_PASSWORD='replace-with-a-database-password' \
DATABASE_URL='postgres://grpc_service:replace-with-a-database-password@postgres:5432/grpc_service?sslmode=disable' \
docker compose --profile observability up --build
```

The Jaeger profile accepts OTLP/HTTP on `4318`; the UI is on `16686`.

## API reference

The source contract is [proto/service.proto](proto/service.proto). Generated Go
types are in [pkg/pb](pkg/pb). The RPC surface is:

- `GetUser`, `CreateUser`, `UpdateUser`, `DeleteUser`
- `StreamUserEvents` (server stream)
- `CollectUserMetrics` (client stream)
- `ChatStream` (bidirectional stream)
- `grpc.health.v1.Health/Check` and `Watch`

UserService methods return standard gRPC status codes. Invalid input is
`InvalidArgument`, missing records are `NotFound`, invalid credentials are
`Unauthenticated`, capacity/rate violations are `ResourceExhausted`, and
unexpected failures are client-safe `Internal` errors.

## Deployment

The Kubernetes example is under [test/deployment/k8s/deployment.yaml](test/deployment/k8s/deployment.yaml).
Before applying it, create `grpc-service-auth` with `jwt-secret`, create
`grpc-service-tls` with `server.crt`, `server.key`, and `ca.crt`, replace the
example image with an immutable registry digest, and validate the manifest:

```sh
kubectl apply --dry-run=client -f test/deployment/k8s/deployment.yaml
```

The deployment uses a non-root user, read-only root filesystem, dropped Linux
capabilities, startup/liveness/readiness probes, and a 30-second termination
drain window. Follow [docs/operations/runbook.md](docs/operations/runbook.md) for rollout and rollback
procedures.

## Contributing

Use branches named `feat/<short-name>`, `fix/<short-name>`, `docs/<short-name>`,
or `chore/<short-name>`. Keep commits in imperative conventional form such as
`fix(auth): reject expired JWTs`. Pull requests must explain the behavioral
change, include a regression test for each defect fixed, and pass the backend,
frontend, security, container, and generated-protobuf CI checks.

Do not commit certificates, secrets, `node_modules`, `.next`, coverage output,
or generated local configuration. Update an ADR when changing transport,
authentication, persistence, observability, or deployment policy.

## License

This repository is released under the MIT License; see [LICENSE](LICENSE).
