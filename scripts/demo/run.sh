#!/usr/bin/env bash
# Drives a short end-to-end demo of the gRPC service for the README GIF.
set -e
export AUTH_API_KEYS='demo-api-key-1234567890' CLIENT_AUTH_TOKEN='demo-api-key-1234567890'
export TLS_ENABLED=false STORAGE_BACKEND=memory
export SERVER_STREAM_INTERVAL=250ms SERVER_MAX_STREAM_MESSAGES=4
export METRICS_ENABLED=true SERVER_HOST=127.0.0.1
./bin/server >/tmp/demo-srv.log 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null' EXIT
sleep 1.2
echo "▶ readiness:" && curl -sf http://127.0.0.1:8080/readyz && echo
echo "▶ running client (unary + streaming RPCs)…"
CLIENT_ADDRESS=127.0.0.1:50051 ./bin/client 2>&1 \
  | grep -oE '"message":"[^"]+"' | sed 's/"message":"/  • /; s/"$//' | head -16
echo "▶ sample metrics:" && curl -s http://127.0.0.1:9090/metrics \
  | grep -E '^grpc_server_(total_requests|active_streams)' | head -4
