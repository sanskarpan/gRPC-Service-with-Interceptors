# Deployment

Reference Kubernetes manifests live under `test/deployment/k8s/`. They are
hardened by default.

## Security posture

- Runs as non-root (uid/gid 10001) with `readOnlyRootFilesystem`,
  `allowPrivilegeEscalation: false`, all Linux capabilities dropped, and the
  `RuntimeDefault` seccomp profile.
- `automountServiceAccountToken: false`.
- A `NetworkPolicy` restricts ingress/egress.
- Images are pinned by digest.

## Availability

- A `PodDisruptionBudget` keeps a minimum number of replicas during voluntary
  disruptions.
- A `HorizontalPodAutoscaler` scales on CPU.
- Topology spread constraints distribute replicas across zones (best-effort).

## Probes

- **Liveness / startup** → `GET /healthz` (shallow; process is up).
- **Readiness** → `GET /readyz` (gated on a real database ping).

The readiness gate flips to not-ready before the drain begins on shutdown, so a
load balancer removes the pod before in-flight requests are drained.

## Secrets

`JWT_SECRET`, `AUTH_API_KEYS` and `DATABASE_URL` are provided via Kubernetes
`secretKeyRef` (and TLS material via a mounted Secret). Nothing sensitive is
baked into the image or manifests. See the [Security model](security.md).

## Validate manifests

```sh
make manifests   # or: kubectl apply --dry-run=server -f test/deployment/k8s/
```

## External secret management

Secrets reach the service only via environment variables sourced from Kubernetes
Secrets (`secretKeyRef`) — the application never talks to a secret backend
directly. To source those Secrets from Vault, AWS Secrets Manager, or GCP Secret
Manager, use the [External Secrets Operator](https://external-secrets.io) (or the
Secrets Store CSI driver). A ready-to-adapt `ExternalSecret` manifest is provided
at `test/deployment/k8s/externalsecret.yaml`; the container image and Deployment
are unchanged. This keeps the binary backend-agnostic and avoids coupling it to a
single secret-manager SDK.
