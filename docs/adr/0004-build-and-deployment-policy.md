# ADR 0004: Reproducible Build and Deployment Gates

## Context

The original repository had inconsistent Go versions, incomplete frontend
checks, vulnerable dependency versions, permissive container defaults, and
deployment manifests that did not match the binary’s actual listeners.

## Decision

Pin the supported toolchain in `go.mod`, Docker, CI, and documentation. CI
must run race-enabled tests, coverage, vet, lint, vulnerability scanning,
frontend typecheck/lint/build, container scanning, and generated-protobuf
checks. The image is multi-stage, static, non-root, read-only at runtime, and
has a liveness healthcheck. Kubernetes examples use immutable-image guidance,
external secrets, probe endpoints, termination draining, and restrictive
security contexts.

## Consequences

Pull requests take longer but fail before merge when build, security, or
deployment contracts regress. Release tagging on `main` requires all required
jobs to pass. The example Kubernetes image name still must be replaced with a
registry digest by the release owner.

## Alternatives considered

- Floating `latest` images and tool versions were rejected because they make
  rebuilds and incident reproduction unreliable.
- Backend-only CI was rejected because the frontend and container are shipped
  artifacts.
- Running as root was rejected because the service does not need elevated
  privileges.
