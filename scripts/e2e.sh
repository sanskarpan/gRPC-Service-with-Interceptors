#!/usr/bin/env bash

set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

cert_dir=$(mktemp -d "${TMPDIR:-/tmp}/grpc-service-e2e.XXXXXX")
compose_project="grpc-service-e2e-$$"
api_key="e2e-api-key-change-me-123456789"
db_password="e2e-db-password-change-me"

cleanup() {
	docker compose -p "$compose_project" down --volumes --remove-orphans >/dev/null 2>&1 || true
	rm -rf "$cert_dir"
}
trap cleanup EXIT

./scripts/generate-certs.sh "$cert_dir" >/dev/null

export CERT_DIR="$cert_dir"
export JWT_SECRET="e2e-jwt-secret-change-me-123456789"
export AUTH_API_KEYS="$api_key"
export E2E_API_KEY="$api_key"
export POSTGRES_PASSWORD="$db_password"
export DATABASE_URL="postgres://grpc_service:${db_password}@postgres:5432/grpc_service?sslmode=disable"
export SERVER_MAX_STREAM_MESSAGES=3
export SERVER_STREAM_INTERVAL=10ms
export E2E=1
export E2E_CA_FILE="$cert_dir/ca.crt"
export E2E_SERVER_NAME=localhost

docker compose -p "$compose_project" up -d --build

ready=0
for _ in $(seq 1 60); do
	if curl --fail --silent "http://127.0.0.1:${E2E_HEALTH_PORT:-8080}/readyz" >/dev/null; then
		ready=1
		break
	fi
	sleep 1
done
if [ "$ready" -ne 1 ]; then
	docker compose -p "$compose_project" logs grpc-server postgres
	exit 1
fi

export E2E_ADDRESS="127.0.0.1:${E2E_GRPC_PORT:-50051}"
export E2E_HEALTH_URL="http://127.0.0.1:${E2E_HEALTH_PORT:-8080}/readyz"
export E2E_LIVENESS_URL="http://127.0.0.1:${E2E_HEALTH_PORT:-8080}/healthz"
export E2E_METRICS_URL="http://127.0.0.1:${E2E_METRICS_PORT:-9090}/metrics"
go test ./cmd/server -run '^TestNetworkE2E$' -count=1
