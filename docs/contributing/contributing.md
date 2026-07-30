# Contributing

## Workflow

- Branch off `main`; keep changes focused.
- Use Conventional Commits (`feat:`, `fix:`, `docs:`, `chore:`, `test:`…).
- Every change ships with tests; the suite runs under `-race`.

## Before opening a PR

```sh
make verify   # vet + race tests + coverage gate + lint + vulncheck
```

CI additionally runs CodeQL, Trivy image scanning, manifest validation and
Prometheus rule tests. All gates must pass.

## Do not commit

Certificates and private keys, secrets, `node_modules`, `.next`, coverage
output, or build artifacts. `*.key` is git-ignored; use `make generate-certs`
for local certificates.

## Regenerating protobufs

```sh
make proto        # regenerate Go/gRPC/gateway stubs
make proto-check  # fail if generated code drifted
```
