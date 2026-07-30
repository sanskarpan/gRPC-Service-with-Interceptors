# Quickstart

Run the server and the example client locally in a couple of minutes.

## 1. Generate development certificates

```sh
make generate-certs
```

These self-signed certificates are for local development only. Keep private
keys out of source control and use your organization's PKI in production.

## 2. Start the server

```sh
export AUTH_API_KEYS='local-api-key-change-me-12345'
export CLIENT_AUTH_TOKEN='local-api-key-change-me-12345'
go run ./cmd/server
```

The server listens on gRPC `:50051`, health `:8080`, and metrics `:9090`.

## 3. Run the example client

In another terminal, with the same environment variables:

```sh
go run ./cmd/client
```

The client exercises unary, server-streaming, client-streaming and
bidirectional streaming RPCs. Every call uses a bounded context and verified
TLS.

## 4. Check health and metrics

```sh
curl -sf http://127.0.0.1:8080/healthz   # liveness
curl -sf http://127.0.0.1:8080/readyz    # readiness
curl -s  http://127.0.0.1:9090/metrics | grep grpc_server_
```

Next: read the [Configuration reference](../reference/configuration.md) to tune
the service, or the [Architecture overview](../architecture/overview.md) to
understand the interceptor chain.
