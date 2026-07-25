# syntax=docker/dockerfile:1.7
FROM golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/grpc-server ./cmd/server

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b

LABEL org.opencontainers.image.title="grpc-service-with-interceptors" \
      org.opencontainers.image.description="Hardened gRPC service demonstrating interceptors, mTLS, observability, and graceful shutdown." \
      org.opencontainers.image.source="https://github.com/anomalyco/gRPC-Service-with-Interceptors" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="anomalyco" \
      org.opencontainers.image.version="1.0.0"

RUN apk --no-cache add ca-certificates wget \
    && addgroup -S -g 10001 app \
    && adduser -S -D -H -u 10001 -G app app \
    && mkdir -p /app/certs /app/configs /tmp/grpc \
    && chown -R app:app /app /tmp/grpc

WORKDIR /app
COPY --from=builder --chown=app:app /out/grpc-server /app/grpc-server
COPY --chown=app:app configs/config.yaml /app/configs/config.yaml
RUN chmod 0555 /app/grpc-server \
    && chmod 0444 /app/configs/config.yaml

USER app
ENV CONFIG_PATH=/app/configs/config.yaml \
    TMPDIR=/tmp/grpc

EXPOSE 50051 8080 9090

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O - http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/app/grpc-server"]
