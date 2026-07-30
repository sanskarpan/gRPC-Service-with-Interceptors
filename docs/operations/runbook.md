# Production Runbook

## Before deployment

1. Build and scan an immutable image tag or digest through CI.
2. Provision a secret-manager-backed JWT secret or API-key set.
3. Provision a server certificate whose SAN matches the service DNS name and,
   when mTLS is enabled, a client CA and client certificate policy.
4. Replace the example Kubernetes image with the approved registry digest.
5. Verify `go test -race`, lint, vulnerability scans, container scan, and
   manifest dry-run are green.

## Deploy

Create the external secrets in the target namespace, then apply the ConfigMap,
Deployment, Service, and HPA. Roll out one immutable image version at a time:

```sh
kubectl -n <namespace> apply -f test/deployment/k8s/deployment.yaml
kubectl -n <namespace> rollout status deployment/grpc-service --timeout=5m
kubectl -n <namespace> get pods -l app=grpc-service
```

Confirm `/healthz` is 200 and `/readyz` is 200 for each pod, then verify a
credentialed gRPC smoke call and Prometheus scrape before shifting traffic.

## Roll back

```sh
kubectl -n <namespace> rollout history deployment/grpc-service
kubectl -n <namespace> rollout undo deployment/grpc-service
kubectl -n <namespace> rollout status deployment/grpc-service --timeout=5m
```

If the deployment was changed outside the manifest, restore the last approved
image digest explicitly and record the incident. Do not roll back by applying
`:latest`; that tag is not an immutable release identifier.

## Top failure modes

### 1. Pods fail to start or remain unready

Inspect `kubectl describe pod`, container logs, and the ConfigMap/Secret names.
The usual causes are missing `JWT_SECRET`, missing TLS files, invalid YAML, or
port conflicts. `CONFIG_PATH` must point to the mounted file. Fix the external
resource and restart the rollout; do not disable auth or TLS to make readiness
pass.

### 2. TLS handshake failures

Check the certificate SAN against the client’s `CLIENT_SERVER_NAME`, verify the
CA chain, and confirm the key matches the certificate. For mTLS, verify the
client certificate is signed by the configured client CA and has clientAuth
usage. Rotate certificates by mounting the new material and restarting through
a controlled rollout.

### 3. Authentication failures

Confirm the caller sends exactly one `x-api-key` or `authorization: Bearer`
value and that the server has the matching secret/API key. Expired JWTs are
rejected. Never log or paste the secret into an incident channel; compare
secret versions and rotate through the secret manager.

### 4. Increased 5xx, latency, or `ResourceExhausted`

Check Prometheus request/error counters, latency histograms, active streams,
and process/container CPU and memory. `ResourceExhausted` can indicate rate,
message, stream, or user-capacity limits. Reduce load or scale replicas. Note
that with the `memory` storage backend replicas do not share state; horizontal
scaling requires the `postgres` backend (see storage configuration).

### 5. Missing telemetry or collector errors

Verify `/metrics` network policy and scrape target first. If tracing is enabled,
check `TRACING_ENDPOINT` is an absolute OTLP/HTTP URL and inspect collector
logs. Tracing export is best-effort during shutdown and must not be used as the
readiness signal.

## Shutdown and data warning

SIGTERM marks the instance unready, drains gRPC and HTTP listeners with the
configured timeout, and then exits. Durability depends on the configured
storage backend. With the `postgres` backend state is persisted and survives
restarts. With the `memory` backend (the default in `configs/config.yaml`,
intended for local development and tests) a restart or reschedule loses all
user state — in that configuration, escalate any report of lost user state as
a release-blocking persistence issue, not as a recoverable cache miss.

## Escalation

The on-call engineer owns first response, evidence capture, and rollback. Page
the service owner for repeated 5xx/latency or any authentication/TLS incident;
page the platform owner for node, ingress, secret-manager, certificate, or
Prometheus/collector failures; involve security immediately for credential
exposure or an authentication bypass. Record the incident ID, image digest,
config/secret versions, timestamps, and relevant metric/log queries.
