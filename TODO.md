# Production Readiness Worklist

This is the living backlog for the production hardening loops. Items are
checked only after implementation and a reproducible validation command or
test prove the behavior.

## P0 — production blockers

- [ ] Replace the process-local user map with a durable repository.
- [ ] Define the persistence contract: schema, migrations, indexes, pool
      limits, transaction semantics, retry behavior, and idempotency policy.
- [ ] Add real-database integration tests for every CRUD path and restart/data
      recovery behavior.
- [ ] Verify multi-replica consistency and failure behavior before claiming
      production readiness for real user data.

## P1 — network and protocol E2E

- [ ] Start the built server with real TLS certificates and exercise the native
      gRPC client over TLS.
- [ ] Exercise API-key success/failure, JWT success/expiry/signature failure,
      ambiguous metadata, and reflection-disabled behavior over the network.
- [ ] Exercise every unary RPC over the network, including invalid input,
      not-found, duplicate/retry, capacity, deadline, and panic paths.
- [ ] Exercise server-streaming, client-streaming, and bidi-streaming RPCs with
      cancellation, message caps, stream deadlines, backpressure, EOF, and
      transport-failure cases.
- [ ] Verify `/healthz`, `/readyz`, and `/metrics` with real listeners during
      startup, serving, dependency failure, and graceful shutdown.
- [ ] Verify the example client against the containerized server, including
      certificate hostname validation and configured credentials.

## P1 — deployment and dependency E2E

- [ ] Run the full Docker Compose stack with Prometheus and an OTLP collector;
      verify scrape and trace delivery.
- [ ] Run Compose with missing secrets, missing certificates, invalid config,
      and unavailable observability dependencies; verify fail-closed or
      graceful-degradation behavior.
- [ ] Validate Kubernetes manifests structurally and, where a cluster is
      available, perform rollout, readiness, termination-drain, and rollback
      smoke tests.
- [ ] Verify container runs as non-root, read-only filesystem, bounded ports,
      and no secret/artifact leakage in the image.

## P1 — load, resilience, and failure injection

- [ ] Add repeatable load tests for unary and all streaming RPCs.
- [ ] Measure p50/p95/p99 latency, request/error rate, active streams, memory,
      goroutines, file descriptors, and rate-limit behavior under load.
- [ ] Run soak testing long enough to detect goroutine, memory, stream, and
      connection leaks.
- [ ] Inject slow clients, cancelled clients, malformed messages, abrupt
      disconnects, listener failures, certificate failures, and collector
      failures.
- [ ] Verify graceful shutdown drains in-flight work and leaves no owned
      goroutines or open listeners.
- [ ] Verify retry/idempotency behavior for every write operation and document
      non-idempotent operations at the API boundary.

## P2 — frontend and tooling

- [ ] Run browser-level UI tests for malformed JSON, concurrent tabs, streaming
      updates, cancellation, saved requests, and error display.
- [ ] Keep the frontend visibly in demo mode until a reviewed gRPC-Web or HTTP
      gateway contract exists.
- [ ] Run dependency freshness and license review on a scheduled cadence.
- [ ] Add secret/artifact scanning and enforce generated-protobuf cleanliness
      in CI.

## Completed in the second hardening loop

- [x] Removed the invalid `Method` lucide-react import and pruned unused
      imports (`Play`, `FolderOpen`, `Gauge`, `Wifi`, `WifiOff`, `LayoutGrid`,
      `List`, `MoreHorizontal`, `RefreshCw`) in `frontend/app/page.tsx`.
- [x] Added an app-level error boundary (`frontend/app/error.tsx`) and a
      global fallback (`frontend/app/global-error.tsx`), plus a streaming
      `frontend/app/loading.tsx`.
- [x] Hardened the demo request flow: typed `mockResponse`, AbortController-
      backed cancellation, guard against state updates after unmount, and
      clipboard-write error handling.
- [x] Pinned image tags in `Dockerfile` (digest-pinned base + runtime),
      added OCI metadata labels, dropped to a non-root `app` user, and
      created `/tmp/grpc` for write paths so the runtime works under
      `readOnly: true`.
- [x] Hardened `docker-compose.yaml`: non-root users, `read_only: true`,
      `init: true`, `no-new-privileges`, `cap_drop ALL`, resource limits,
      and tmpfs for every writable path on the server, postgres, jaeger,
      and prometheus services.
- [x] Expanded `test/deployment/k8s/deployment.yaml`: `topologySpread
      Constraints`, pod anti-affinity, `fsGroup`, `serviceAccountName`,
      in-memory `emptyDir` mounts for `/tmp/grpc` and `/run` to support a
      read-only root filesystem, and an explicit `imagePullPolicy`.
- [x] Added `test/deployment/k8s/poddisruptionbudget.yaml` (min 2 pods
      available), `test/deployment/k8s/networkpolicy.yaml` (ingress on
      50051/8080/9090, egress to PostgreSQL/OTLP/DNS), and a locked-down
      `serviceaccount.yaml`.
- [x] Hardened `.github/workflows/ci-cd.yaml`: explicit Go module cache,
      added a `Build` step, separated `Vet`, added a manifests job, added a
      container smoke run with `--read-only` before the Trivy scan, and kept
      golangci-lint, govulncheck, race + coverage enforcement, and frontend
      `npm ci && npm run build`.
- [x] Expanded `Makefile` with `make help`, `make lint` (with golangci-lint →
      `go vet` fallback), `make vulncheck`, `make test-load`, `make bench`,
      `make verify` (vet + race + coverage + lint + vulncheck), and made
      `docker-run` use `read_only`, `tmpfs`, `no-new-privileges`, and the
      minimum capability set.
- [x] Tightened `.dockerignore` and `.gitignore` to cover `*.test`, `*.out`,
      `coverage.out`, `*.crt`, `*.key`, `*.pem`, `.env`, `frontend/node_modules`,
      `frontend/.next`, `frontend/coverage`, and editor/IDE noise.

## Completed in the first hardening loop

- [x] Full repository audit and prioritized defect list in `AUDIT.md`.
- [x] Fail-closed configuration, TLS/mTLS, API-key/JWT auth, reflection gate.
- [x] Bounds, validation, cloning, timeouts, rate admission, panic recovery,
      structured logs, Prometheus metrics, OTLP tracing, health probes, and
      graceful shutdown.
- [x] Unit, race, fuzz, benchmark, authenticated in-process gRPC integration,
      frontend typecheck/lint/build, dependency scans, container build/health
      smoke, protobuf reproducibility, and manifest/Compose parsing.
- [x] README, ADRs, runbook, CI/CD, Dependabot, CodeQL, Docker hardening, and
      deployment documentation.

