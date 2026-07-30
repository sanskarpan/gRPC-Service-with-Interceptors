# Docker & Compose

## Build the image

```sh
make docker-build
```

The image is a distroless-style, non-root build: it runs as uid/gid 10001 with
a read-only root filesystem, dropped capabilities and `no-new-privileges`.

## Run the stack

```sh
export JWT_SECRET='replace-with-a-32-byte-minimum-secret'
export POSTGRES_PASSWORD='replace-me'
make compose-up      # server + PostgreSQL (+ optional observability)
make compose-down
```

`docker-compose.yaml` requires secrets via environment with `:?` fail-fast, so
the stack refuses to start with unset credentials. See the
[Security model](../operations/security.md) for the full secret contract.
