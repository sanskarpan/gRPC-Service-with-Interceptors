.PHONY: all build test test-race test-coverage test-load test-soak coverage-check lint vet vulncheck fuzz bench benchmark benchmark-server loadtest proto proto-check frontend-install frontend-typecheck frontend-lint frontend-build clean generate-certs run-server run-client docker-build docker-run compose-up compose-down test-postgres test-postgres-e2e test-retry e2e security verify help

GO ?= go
GOFLAGS ?=
COVERAGE_MIN ?= 10
FUZZ_TIME ?= 10s
GOLANGCI_LINT_VERSION ?= v2.1.6
GOVULNCHECK_VERSION ?= v1.1.4
GO_PACKAGES := ./cmd/... ./internal/... ./pkg/...

all: build test-race frontend-build

help:
	@echo "Common targets:"
	@echo "  make build            - compile server and client binaries"
	@echo "  make test             - run unit tests"
	@echo "  make test-race        - run tests with the race detector"
	@echo "  make test-coverage    - run tests with coverage report"
	@echo "  make test-postgres    - run Postgres integration tests (requires TEST_DATABASE_URL)"
	@echo "  make test-postgres-e2e - run E2E tests against Postgres (requires TEST_DATABASE_URL)"
	@echo "  make test-retry       - run retry unit tests"
	@echo "  make test-soak        - run soak/stability tests (requires TEST_DATABASE_URL)"
	@echo "  make test-load        - run load tests (build tag loadtest)"
	@echo "  make coverage-check   - fail if coverage drops below COVERAGE_MIN"
	@echo "  make lint             - run golangci-lint (falls back to go vet)"
	@echo "  make vet              - run go vet"
	@echo "  make vulncheck        - run govulncheck"
	@echo "  make fuzz             - run a short fuzz pass"
	@echo "  make bench            - run all package benchmarks"
	@echo "  make verify           - run vet, test-race, coverage-check, lint, vulncheck"
	@echo "  make e2e              - run end-to-end scripts"
	@echo "  make frontend-*       - frontend install/typecheck/lint/build"

build:
	$(GO) build -trimpath ./cmd/server ./cmd/client

test:
	$(GO) test $(GO_PACKAGES)

test-postgres:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required"; exit 1)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" $(GO) test -race -count=1 -timeout=120s -run 'TestPostgres' ./internal/storage/...

test-postgres-e2e:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required"; exit 1)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" $(GO) test -race -count=1 -timeout=300s -run 'TestE2E' ./cmd/server/...

test-retry:
	$(GO) test -race -count=1 -timeout=60s -run 'TestRetry|TestBackoff|TestIsRetryable' ./internal/storage/...

test-soak:
	@test -n "$(TEST_DATABASE_URL)" || (echo "TEST_DATABASE_URL is required"; exit 1)
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" $(GO) test -tags=soak -race -count=1 -timeout=30m -run 'TestSoak' ./cmd/server/...

test-race:
	$(GO) test -race $(GO_PACKAGES)

test-coverage:
	$(GO) test -race -covermode=atomic -coverprofile=coverage.out $(GO_PACKAGES)
	$(GO) tool cover -func=coverage.out

test-load:
	$(GO) test -tags=loadtest -count=1 -timeout=120s ./cmd/server/... -run TestLoad -v

coverage-check: test-coverage
	@coverage=$$($(GO) tool cover -func=coverage.out | awk '/^total:/ {gsub("%", "", $$3); print $$3}'); \
	awk -v actual="$${coverage:-0}" -v minimum="$(COVERAGE_MIN)" 'BEGIN { if ((actual + 0) < (minimum + 0)) { printf "coverage %.1f%% is below %.1f%%\n", actual, minimum; exit 1 } }'

lint:
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run --timeout=5m $(GO_PACKAGES); \
	else \
		echo "golangci-lint not installed; falling back to go vet"; \
		$(GO) vet $(GO_PACKAGES); \
	fi

vet:
	$(GO) vet $(GO_PACKAGES)

vulncheck:
	@command -v govulncheck >/dev/null 2>&1 || $(GO) install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	govulncheck $(GO_PACKAGES)

fuzz:
	$(GO) test ./internal/service -run=^$$ -fuzz=Fuzz -fuzztime=$(FUZZ_TIME)

bench:
	$(GO) test -run=^$$ -bench=. -benchmem -count=1 $(GO_PACKAGES)

benchmark:
	$(GO) test ./internal/service -run=^$$ -bench=. -benchmem -count=3

benchmark-server:
	$(GO) test -bench=. -benchmem -count=1 ./cmd/server/...

loadtest:
	$(GO) test -tags=loadtest -count=1 -timeout=120s ./cmd/server/... -run TestLoad -v

proto:
	@command -v protoc >/dev/null || (echo "protoc is required"; exit 1)
	@$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
	@$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1
	@$(GO) install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.26.3
	@PATH="$(shell $(GO) env GOPATH)/bin:$$PATH" protoc --proto_path=proto \
		--go_out=module=github.com/example/grpc-service:. \
		--go-grpc_out=module=github.com/example/grpc-service:. \
		--grpc-gateway_out=module=github.com/example/grpc-service:. \
		service.proto health.proto

proto-check: proto
	@tmp=$$(mktemp -d); \
	trap 'rm -rf "$$tmp"' EXIT; \
	PATH="$(shell $(GO) env GOPATH)/bin:$$PATH" protoc --proto_path=proto \
		--go_out=module=github.com/example/grpc-service:"$$tmp" \
		--go-grpc_out=module=github.com/example/grpc-service:"$$tmp" \
		--grpc-gateway_out=module=github.com/example/grpc-service:"$$tmp" \
		service.proto health.proto; \
	cmp "$$tmp/pkg/pb/service.pb.go" pkg/pb/service.pb.go; \
	cmp "$$tmp/pkg/pb/service_grpc.pb.go" pkg/pb/service_grpc.pb.go; \
	cmp "$$tmp/pkg/pb/service.pb.gw.go" pkg/pb/service.pb.gw.go; \
	cmp "$$tmp/pkg/pb/grpc/health/v1/health.pb.go" pkg/pb/grpc/health/v1/health.pb.go; \
	cmp "$$tmp/pkg/pb/grpc/health/v1/health_grpc.pb.go" pkg/pb/grpc/health/v1/health_grpc.pb.go

frontend-install:
	npm --prefix frontend ci

frontend-typecheck:
	npm --prefix frontend run typecheck

frontend-lint:
	npm --prefix frontend run lint

frontend-build:
	npm --prefix frontend run build

clean:
	rm -rf bin/ coverage.out coverage.html

generate-certs:
	./scripts/generate-certs.sh

run-server:
	$(GO) run ./cmd/server

run-client:
	$(GO) run ./cmd/client

docker-build:
	docker build --tag grpc-service:local .

docker-run:
	docker run --rm --read-only --tmpfs /tmp/grpc:rw,nosuid,nodev,size=64m \
		--security-opt no-new-privileges:true \
		--cap-drop ALL --cap-add NET_BIND_SERVICE \
		-p 50051:50051 -p 8080:8080 -p 9090:9090 \
		-v $$(pwd)/certs:/app/certs:ro \
		grpc-service:local

compose-up:
	docker compose up -d

compose-down:
	docker compose down

e2e:
	./scripts/e2e.sh

security: vulncheck
	$(GO) mod verify
	npm --prefix frontend audit --omit=dev --audit-level=high

verify: vet test-race coverage-check lint vulncheck
	@echo "all checks passed"
