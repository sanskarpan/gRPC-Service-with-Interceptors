# Production Readiness Audit

Date: 2026-07-22

Scope: Go gRPC server and client, protobuf contracts and generated code, configuration, deployment manifests, Docker/Compose, CI, certificate tooling, tests, and the Next.js frontend. Generated protobuf files were checked against the source schemas. `frontend/node_modules` and `frontend/.next` are generated artifacts; their dependency metadata and security advisories were evaluated through the package lock and `npm audit` rather than treated as application source.

## System model at audit time

The repository contains an in-memory Go `UserService` exposed through generated gRPC unary and streaming APIs. At audit time, `cmd/server` loaded YAML, constructed incomplete interceptors, registered the user and gRPC health services, and started only a gRPC listener plus a nominal Prometheus configuration. The health listener and OpenTelemetry implementation were not connected. `cmd/client` contained demo callers. The Next.js application was a browser-only mock UI; it did not connect to the gRPC service. Docker, Compose, and Kubernetes manifests disagreed on configuration paths, health behavior, TLS mode, and image delivery.

The principal trust boundaries are: YAML and environment configuration into the process; unauthenticated network clients into gRPC interceptors; protobuf messages into service methods; TLS certificate material into the listener; browser-entered endpoint, metadata, and JSON into the frontend; and build/dependency artifacts into CI and containers.

## Baseline evidence

- `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...` pass on the audit host with Go 1.26.5.
- Baseline Go coverage is 8.3% overall; the service is 52.1% and interceptors are 19.7%.
- `golangci-lint run` exits non-zero with 11 findings: seven unchecked errors, three deprecated `grpc.Dial` uses, and one unused function.
- `npx tsc --noEmit` fails because `lucide-react` does not export `Method` (`frontend/app/page.tsx:21`).
- `npm run lint` is not a deterministic check: Next.js starts an interactive ESLint setup prompt and exits without a configured lint policy.
- `npm audit --omit=dev` reports 2 critical, 1 high, 3 moderate, and 1 low vulnerable dependency groups, including Next.js, protobufjs, and `@grpc/grpc-js`.
- `govulncheck ./...` reports GO-2026-4762 in `google.golang.org/grpc@v1.79.1`, fixed in 1.79.3.
- The directory has no local `.git` repository or `.gitignore`; Git resolves to `/Users/sanskar`, so the initial status included unrelated home-directory files. No parent-repository files were modified or staged.

## Prioritized defect list

### ID: 1
Severity: Critical
Category: Security
Location: `pkg/interceptors/server.go:181-183`
Root Cause: `validateToken` accepts every non-empty token except the literal string `invalid`; it does not use the configured JWT secret or API key list and does not authenticate an identity.
Blast Radius: Every protected unary and streaming RPC is forgeable by any network caller who can send an arbitrary non-empty authorization value.
Fix: Inject a verifier built from validated configuration, verify JWT signature/claims or constant-time API-key matches, enforce expiry/audience/issuer, and return only a generic unauthenticated status on failure. Add integration tests for forged, expired, malformed, wrong-audience, and valid credentials.

### ID: 2
Severity: Critical
Category: Security
Location: `cmd/server/main.go:86-93`
Root Cause: TLS loading errors are logged and the server deliberately falls back to plaintext even when `tls.enabled` is true.
Blast Radius: A missing, invalid, or rotated certificate silently removes transport confidentiality and server authentication for all clients.
Fix: Fail startup closed when TLS is enabled and credentials cannot be loaded; make plaintext an explicit, separately validated development mode and expose the active transport mode in startup diagnostics.

### ID: 3
Severity: Critical
Category: Correctness
Location: `go.mod:3`, `Dockerfile:1`, `.github/workflows/ci-cd.yaml:16-20`
Root Cause: The module requires Go 1.24, while the Docker builder and both CI jobs install Go 1.21.
Blast Radius: Clean Docker builds and CI runs fail before compiling the service; the repository has no reproducible production build path.
Fix: Pin one supported Go toolchain version in `go.mod`, Docker, CI, and documentation, or use the `toolchain` directive consistently. Add a CI job that asserts the declared version and build the container in CI.

### ID: 4
Severity: Critical
Category: Correctness
Location: `cmd/server/main.go:28`, `Dockerfile:18-23`
Root Cause: The server unconditionally reads `configs/config.yaml`, but the image copies that file to `/app/config.yaml`; the Compose `CONFIG_PATH` variable is ignored.
Blast Radius: The published container exits during startup because its configured file path does not exist; Compose cannot override the path.
Fix: Implement a validated `CONFIG_PATH` override with a documented default, copy the file to the matching default path, and add a container startup test/health smoke test.

### ID: 5
Severity: Critical
Category: Reliability
Location: `internal/service/user.go:15-24`, `internal/service/user.go:51-67`
Root Cause: All state is stored in an unbounded process-local map with no persistence, quota, eviction, or storage abstraction.
Blast Radius: A restart loses every user; sustained traffic can exhaust memory; multiple replicas have divergent data and writes are not durable or retry-safe.
Fix: Put storage behind an interface and use a durable database with bounded pools, migrations, indexes, transaction semantics, and an explicit idempotency strategy. If the in-memory backend remains a demo, reject it in production configuration and enforce a size limit.

### ID: 6
Severity: High
Category: Security
Location: `pkg/config/config.go:65-76`, `configs/config.yaml:15-18`, `test/deployment/k8s/deployment.yaml:16-18`
Root Cause: Configuration accepts zero values and unknown keys, contains example JWT/API credentials in deployment-readable files, and has no environment/secrets-manager integration or production validation.
Blast Radius: Deployments can start with blank or placeholder credentials, operators may mistake sample secrets for real protection, and typos silently select unsafe zero values.
Fix: Decode with `KnownFields`, validate all required fields and ranges, remove secrets from checked-in YAML, load secret material from environment or an external secret reference, and reject placeholders/non-production defaults at startup.

### ID: 7
Severity: High
Category: Security
Location: `cmd/server/main.go:118-128`
Root Cause: mTLS config only populates `ClientCAs`; it never sets `tls.Config.ClientAuth` to require and verify a client certificate, and ignores whether the CA PEM parsed successfully.
Blast Radius: Enabling mTLS does not enforce client identity; unauthenticated TLS clients can still connect.
Fix: Require `tls.RequireAndVerifyClientCert`, validate the CA pool append result, set a minimum TLS version, and test accepted/rejected client certificates against a real TLS listener.

### ID: 8
Severity: High
Category: Security
Location: `cmd/server/main.go:46`, `SPEC.md:79-84`
Root Cause: Reflection is always registered even though the specification says it is disabled in production and no configuration gate exists.
Blast Radius: Public clients can enumerate service and message schemas, increasing reconnaissance and exposing APIs intended to be private.
Fix: Gate reflection behind an explicit development setting, default it off, and test that production configuration does not register the reflection service.

### ID: 9
Severity: High
Category: Security
Location: `cmd/client/main.go:53,88,124,163,213-224`, `pkg/interceptors/client.go:86-89`
Root Cause: Client examples hardcode localhost, use insecure transport, inject the literal `Bearer test-token`, and provide a TLS helper with `InsecureSkipVerify: true` that is unused and ignores certificate errors.
Blast Radius: Copy-pasted client behavior is vulnerable to MITM and cannot operate correctly outside a developer laptop; demo credentials can be sent to arbitrary endpoints.
Fix: Build client dial options from validated config, require TLS verification outside explicit development mode, use a configured CA/server name and credential provider, and remove hardcoded credentials.

### ID: 10
Severity: High
Category: Security
Location: `scripts/generate-certs.sh:7-17`
Root Cause: Generated certificates have no Subject Alternative Name, shell strict mode omits `-u` and `pipefail`, and the script deletes all private keys including the CA key immediately after generation.
Blast Radius: Modern clients reject the server certificate for hostname verification; the CA cannot sign rotations or reproduce the trust chain; script errors can be masked.
Fix: Generate SANs from explicit arguments, use `set -euo pipefail`, set restrictive permissions, keep CA/private material out of the image and repository, and document that the script is development-only.

### ID: 11
Severity: High
Category: Reliability
Location: `cmd/server/main.go:48-67`, `cmd/server/main.go:133-151`
Root Cause: Servers are launched as unmanaged goroutines, the process blocks forever in `select {}`, only the gRPC server receives graceful stop, and `Fatal` in a goroutine exits without coordinated cleanup.
Blast Radius: SIGTERM can leave a container alive after gRPC stops; in-flight work is not drained across all listeners; bind/serve failures are not propagated to a supervisor; shutdown behavior is non-deterministic.
Fix: Own listeners in a `run` function, use `signal.NotifyContext`, coordinate errors with `errgroup`, shut down gRPC and HTTP servers with a bounded drain deadline, and return from `main` with an appropriate exit code.

### ID: 12
Severity: High
Category: Reliability
Location: `cmd/server/main.go:145-151`, `test/deployment/k8s/deployment.yaml:89-94`
Root Cause: The configured health port is never started and `/healthz`/`/readyz` handlers do not exist; the Kubernetes readiness probe therefore targets a dead endpoint.
Blast Radius: Kubernetes marks every pod unready and removes it from service; operators have no meaningful liveness/readiness signal.
Fix: Add a dedicated HTTP server with `/healthz` liveness and `/readyz` readiness, include dependency checks and a readiness state transition, apply HTTP timeouts, and cover both healthy and draining states.

### ID: 13
Severity: High
Category: Reliability
Location: `cmd/server/main.go:70-84`, `internal/service/user.go:27-179`
Root Cause: `max_conns` and request timeout configuration are unused; there are no gRPC message size limits, keepalive policy, stream quota, rate limiter, or per-operation deadline enforcement.
Blast Radius: Large messages and unlimited concurrent/long-lived streams can consume memory, goroutines, file descriptors, and CPU until the process is unavailable.
Fix: Configure maximum receive/send sizes, keepalive enforcement, bounded concurrency and per-client quotas, server-side deadlines, and a rate-limiting policy; reject excess work with `ResourceExhausted`.

### ID: 14
Severity: High
Category: Correctness
Location: `internal/service/user.go:35-40`, `internal/service/user.go:78-94`
Root Cause: Methods return pointers stored in the shared map. The lock protects map lookup only; callers can mutate the returned protobuf after the lock is released, and update mutates the same object in place.
Blast Radius: Concurrent requests can race, observe partially updated users, or corrupt stored state; `go test -race` does not cover this because no concurrent test exercises it.
Fix: Clone protobuf messages at storage boundaries, update copies under the lock/transaction, and add concurrent read/update tests under the race detector.

### ID: 15
Severity: High
Category: Correctness
Location: `internal/service/user.go:43-50`, `internal/service/user.go:70-92`, `internal/service/user.go:97-110`
Root Cause: Input validation only checks a few empty strings. It allows whitespace-only/overlong names, malformed or overlong email addresses, negative or impossible ages, nil requests, and ambiguous update semantics where age zero or empty fields cannot be intentionally set.
Blast Radius: Invalid data enters storage and logs; nil direct calls panic; clients cannot reliably express valid partial updates.
Fix: Validate nils, trim and bound strings, validate email syntax and age range, use a field mask/optional fields for patch semantics, and add table-driven boundary tests for each field.

### ID: 16
Severity: High
Category: Correctness
Location: `internal/service/user.go:54`, `internal/service/user.go:172-173`
Root Cause: IDs are generated from wall-clock nanoseconds without collision handling or an injectable generator.
Blast Radius: Concurrent creates can overwrite a user on a collision; tests and retries are non-deterministic; message IDs are not globally safe across replicas.
Fix: Use a collision-resistant UUID/ULID generator behind an interface, verify uniqueness transactionally, and add concurrent collision and deterministic test coverage.

### ID: 17
Severity: High
Category: Correctness
Location: `internal/service/user.go:141-162`
Root Cause: Every `Recv` error is treated as normal end-of-stream, including cancellation and transport errors; a zero-message stream computes `0/0`, and metric values are neither bounded nor checked for finite values.
Blast Radius: Aborted or malformed uploads are reported as successful summaries, zero-input responses contain NaN, and clients can cause unbounded accumulation or invalid arithmetic.
Fix: Distinguish `io.EOF` from other errors, validate each message, enforce count/size limits, reject non-finite values, define zero-input behavior, and test cancellation, malformed input, and limits.

### ID: 18
Severity: High
Category: Reliability
Location: `internal/service/user.go:113-138`, `internal/service/user.go:165-178`
Root Cause: Streaming RPCs accept arbitrary user IDs/messages, ignore event filters, have hardcoded duration/count, and have no global stream budget or message-length bound.
Blast Radius: Clients receive misleading results and can hold resources with many long-lived streams; chat can echo oversized or invalid messages.
Fix: Validate stream requests, implement event filtering, make limits configurable and bounded, account active streams, honor cancellation on every operation, and enforce per-message limits.

### ID: 19
Severity: High
Category: Reliability
Location: `internal/service/user.go:165-178`, `cmd/client/main.go:171-210`
Root Cause: Bidi streaming has no server/client deadline or ownership protocol; the client starts a sender goroutine but never consumes `errCh` or cancels it when receiving ends.
Blast Radius: Stalled peers and failed senders can leave goroutines and connections alive indefinitely; client shutdown can race with stream use.
Fix: Use context deadlines, a single owner for each stream direction, cancellation on either-side failure, bounded send queues, and explicit close/error propagation tests.

### ID: 20
Severity: High
Category: Observability
Location: `pkg/tracing/tracing.go:9-23`, `cmd/server/main.go:27-52`
Root Cause: Tracing is a no-op stub and `tracing.Init` is never called; no spans or propagation are installed at gRPC entry points or external calls.
Blast Radius: Cross-service latency and failure diagnosis is impossible despite the configuration claiming Jaeger/OpenTelemetry support.
Fix: Initialize OpenTelemetry from config with a shutdown function, install gRPC server/client instrumentation, propagate context, and add exporter failure/degraded-mode tests.

### ID: 21
Severity: High
Category: Observability
Location: `cmd/server/main.go:71-83`, `pkg/interceptors/server.go:16-69`
Root Cause: Auth is the outermost interceptor, so rejected requests bypass logging and metrics; panic recovery is innermost and does not protect auth/logging/metrics code. `ActiveStreams` is never updated.
Blast Radius: Attack traffic and middleware failures disappear from request/error metrics, and dashboards under-report load and failures.
Fix: Make recovery the outer boundary, ensure every completed request is classified and measured including auth failures, add request IDs/trace IDs safely, and increment/decrement active-stream gauges with `defer`.

### ID: 22
Severity: Medium
Category: Security
Location: `pkg/interceptors/server.go:16-34`, `pkg/interceptors/client.go:54-66`
Root Cause: Full protobuf requests and unbounded error strings are logged at info level with no redaction, size cap, sampling, or secret-field policy.
Blast Radius: PII, credentials in request fields, and internal errors can enter centralized logs, increasing privacy and incident impact; high-volume payloads can inflate logging cost.
Fix: Log method, request ID, status, and bounded metadata only; redact configured sensitive fields and use structured error classifications rather than raw messages at the boundary.

### ID: 23
Severity: Medium
Category: Correctness
Location: `pkg/errors/errors.go:79-84`
Root Cause: `FromError` only recognizes a direct `*AppError`, panics on nil, and returns arbitrary internal error text for unknown errors.
Blast Radius: Wrapped application errors lose their intended status; nil error handling can crash diagnostics; boundary code may leak implementation details.
Fix: Use `errors.As`, handle nil explicitly, centralize safe status conversion, and separate operator-facing wrapped errors from client-facing messages.

### ID: 24
Severity: Medium
Category: Correctness
Location: `frontend/app/page.tsx:213-351`
Root Cause: The browser UI is a mock simulator, not a gRPC client. It never uses `serverAddress`, TLS, or metadata, and `JSON.parse` runs before the `try` block.
Blast Radius: Users see successful fake responses instead of real server behavior; malformed JSON can throw out of the event handler and leave the tab loading state stuck.
Fix: Implement a real, separately hosted gRPC-Web/HTTP gateway contract or label the UI as demo-only; validate JSON inside the error boundary, abort in-flight requests, and test success, transport failure, auth failure, and malformed input.

### ID: 25
Severity: Medium
Category: Correctness
Location: `frontend/app/page.tsx:270-281`
Root Cause: The UpdateUser switch case uses `/example.UserService.UpdateUser` with a dot instead of the actual `/example.UserService/UpdateUser` method path.
Blast Radius: Update requests always fall through to a misleading successful "not fully implemented" response.
Fix: Use generated method constants or a typed method registry and assert every supported method has a handler in frontend tests.

### ID: 26
Severity: Medium
Category: Reliability
Location: `frontend/app/page.tsx:169-181`, `frontend/app/page.tsx:297-309`
Root Cause: State updates close over the current `tabs` array and append stream events from a stale snapshot; timers and async work are not cancelled when tabs change or the component unmounts.
Blast Radius: Concurrent requests can overwrite each other, lose stream events, or update removed UI state; behavior varies with timing.
Fix: Use functional state updates/reducers, per-request abort controllers, stable callbacks, and component lifecycle cleanup; add deterministic tests for concurrent tab and stream updates.

### ID: 27
Severity: Medium
Category: Correctness
Location: `frontend/app/page.tsx:21`, `.github/workflows/ci-cd.yaml:24-31`
Root Cause: The frontend does not type-check or build in CI, allowing the invalid `Method` icon import and other client regressions to ship unnoticed.
Blast Radius: The frontend cannot pass a clean TypeScript build on the current checkout, while backend-only CI reports green.
Fix: Remove/replace the invalid import, add deterministic ESLint configuration, and require `npm ci`, typecheck, lint, and production build in CI.

### ID: 28
Severity: Medium
Category: Reliability
Location: `internal/service/user_test.go:115`, `internal/service/user_test.go:144`, `pkg/config/config_test.go:44-49,99-104`
Root Cause: Tests discard errors, assert only presence/non-nil values, omit nil/boundary/concurrency/stream paths, and do not exercise the actual network server or storage behavior.
Blast Radius: Tests pass while important correctness, lifecycle, security, and race defects remain undetected; the reported 8.3% coverage is not a production safety net.
Fix: Replace ignored errors with fatal assertions, assert status codes and complete contents, add table-driven/fuzz/concurrency tests, add hermetic in-process gRPC integration tests, and establish a justified coverage floor.

### ID: 29
Severity: Medium
Category: Performance
Location: `.github/workflows/ci-cd.yaml:9-80`, `Makefile:24-31`
Root Cause: CI does not run race, coverage, frontend, dependency, container, or proto reproducibility checks; the proto target references an old grpc-gateway module path and no pinned tool binaries.
Blast Radius: Broken generated API changes, races, vulnerable dependencies, and container regressions can merge; local and CI generation can differ.
Fix: Pin tool versions, verify generated protobuf output is clean, run the full unit/race/integration/fuzz/benchmark policy, and fail on lint/security/coverage regressions.

### ID: 30
Severity: Medium
Category: Security
Location: `frontend/package-lock.json` dependency graph; `go.mod:5-17`
Root Cause: The current install contains vulnerable versions: Next 14.1.0, `@grpc/grpc-js` 1.14.3, and protobufjs at or below vulnerable ranges. Go vulnerability scanning also finds gRPC-Go 1.79.1 below the fixed 1.79.3 release, and OpenTelemetry Jaeger exporter is deprecated.
Blast Radius: A deployed frontend can be exposed to published SSRF, authorization-bypass, DoS, and code-injection advisories; the backend has a reachable gRPC authorization-bypass vulnerability.
Fix: Upgrade and lock patched versions, run `npm audit --omit=dev` and `govulncheck ./...` in CI, review transitive fixes, replace deprecated exporters, and document an update cadence. Evidence: [GO-2026-4762](https://pkg.go.dev/vuln/GO-2026-4762) and the advisory URLs emitted by the baseline `npm audit` report.

### ID: 31
Severity: Medium
Category: Reliability
Location: `docker-compose.yaml:14-45`, `test/deployment/prometheus/prometheus.yml:4-7`
Root Cause: Compose expects a `grpc_health_probe` binary absent from the image, configures `CONFIG_PATH` that the server ignores, mounts `./test/deployment/prometheus.yml` while the file is in `test/deployment/prometheus/prometheus.yml`, and uses unpinned `latest` images.
Blast Radius: Compose health status is permanently unhealthy, Prometheus configuration mounting fails, and deployments are non-reproducible.
Fix: Add the health probe or use the HTTP health endpoint, correct the mount, pin image digests, pass only supported config variables, and add a Compose smoke test.

### ID: 32
Severity: Medium
Category: Reliability
Location: `test/deployment/k8s/deployment.yaml:13-18,41-45,67-105`
Root Cause: Kubernetes disables TLS while the sample config contains placeholder auth, creates an empty TLS Secret, uses `grpc-service:latest` without a registry/digest, mounts a ConfigMap as if it were a production configuration, and has no security context or rollout termination policy.
Blast Radius: The deployment is insecure or unable to pull/start consistently, and readiness is already broken by finding 12; pod hardening and supply-chain controls are absent.
Fix: Separate development manifests from production, use external Secrets, pin an immutable image from a registry, require TLS/auth in production, add non-root/read-only security context, termination/drain settings, and validate manifests in CI.

### ID: 33
Severity: Low
Category: Correctness
Location: `pkg/metrics/metrics.go:34-39`, `pkg/interceptors/client.go:15-40`
Root Cause: The active-stream gauge is defined but unused, client metrics are globally registered but client interceptors are not installed, and Prometheus registration is hard-wired through `promauto`.
Blast Radius: Metrics give a false picture of concurrency and client behavior and are difficult to isolate in tests or multi-server processes.
Fix: Inject a registry/metrics provider, install the intended interceptors, instrument stream lifecycle, and test collected metric samples and labels.

### ID: 34
Severity: Low
Category: Architecture
Location: `pkg/tracing/tracing.go:9-23`, `SPEC.md:60-112`
Root Cause: The specification advertises middleware, gateway, mTLS, retry, and OpenTelemetry capabilities that are absent or stubs; there are no documented ADRs, runbook, contributing guide, or production README.
Blast Radius: Operators and integrators form an incorrect mental model, deploy unsupported features, and cannot recover or extend the service safely.
Fix: Make the implementation and contract agree, document explicitly supported behavior, add ADRs for transport/auth/storage/observability/deployment decisions, and provide a verified README/runbook.

### ID: 35
Severity: Low
Category: Security
Location: Repository root (no `.gitignore`), `Dockerfile:8`
Root Cause: Build context is unconstrained and there is no project ignore file, so `frontend/node_modules`, `.next`, local certificates, coverage, editor files, and potentially secrets can enter source control or Docker build context.
Blast Radius: Images become unnecessarily large and may contain credentials or development artifacts; accidental commits are likely.
Fix: Add a strict `.gitignore` and `.dockerignore`, keep generated dependencies/build output out of the context, and add secret/artifact checks to CI.

## Delivery gates derived from the audit

Critical and High findings must be fixed with regression tests before production claims are made. The final gate must include backend unit/integration tests, `-race`, fuzz smoke runs, coverage enforcement, frontend typecheck/lint/build, `go vet`, `golangci-lint`, `govulncheck`, `npm audit --omit=dev`, reproducible protobuf generation, container build/scanning, and manifest validation. A clean local unit-test result alone is not sufficient evidence of readiness.

## Remediation status after the audit

The following fixes have been implemented and regression-tested: fail-closed
authentication and TLS/mTLS (1, 2, 6, 7, 8); aligned toolchains and container
configuration (3, 4); bounded validation, cloning, IDs, streams, timeouts, and
rate admission (13-19); coordinated health and graceful shutdown (11, 12);
structured logs, metrics, OTLP tracing, and panic recovery (20-23); frontend
type/lint/build hardening and explicit demo labeling (24-27); dependency,
protobuf, container, and manifest CI controls (29-35). The original baseline
dependency findings are addressed by the current lockfiles and gRPC 1.82.1
(30).

Two release gates remain explicit rather than hidden:

- Finding 5 remains open as an architecture limitation: the backend is bounded
  but process-local and non-durable. A real production data workload requires a
  repository/database decision, migrations, and real-infrastructure tests.
- Findings 24, 26, 28, and 33 are partially addressed: the UI remains a local
  simulator, the in-memory backend is not a durable integration target, and
  Prometheus instruments remain process-global. The repository documents these
  boundaries and CI validates the behavior that is actually implemented.
