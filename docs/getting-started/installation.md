# Installation & toolchain

The CI contract pins Go `1.26.5` and Node.js `22.14.0`. The verified local
toolchain is:

| Tool | Version |
| --- | --- |
| Go | 1.26.5 |
| Node.js | 22.14.0 in CI; newer also works locally |
| npm | 11.x |
| protoc | 33.4 |
| OpenSSL | 3.6.2 (development certificate script) |
| Docker Engine | 28.4.0 |
| Docker Compose | v2.39.4 |
| kubectl | v1.34.1 (manifest validation) |

## Build from source

```sh
git clone https://github.com/sanskarpan/gRPC-Service-with-Interceptors
cd gRPC-Service-with-Interceptors
make build          # compiles ./cmd/server and ./cmd/client
make test-race      # unit + integration tests under the race detector
```

## Common make targets

| Target | Purpose |
| --- | --- |
| `make build` | Build server and client binaries |
| `make test-race` | Race-enabled test suite |
| `make coverage-check` | Enforce the coverage floor |
| `make lint` / `make vet` | Static analysis |
| `make vulncheck` | `govulncheck` scan |
| `make generate-certs` | Development TLS certificates |
| `make proto` | Regenerate protobuf/gRPC code |
| `make verify` | vet + race tests + coverage + lint + vulncheck |
