package main

import (
	"context"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/pb"
)

// TestE2E_IdempotentCreate exercises CreateUser idempotency through the real
// gRPC server and interceptor chain on the memory backend.
func TestE2E_IdempotentCreate(t *testing.T) {
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	req := &pb.CreateUserRequest{Name: "Idem", Email: "idem@test.com", Age: 33, IdempotencyKey: "e2e-key-1"}
	first, err := client.CreateUser(ctx, req)
	if err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	second, err := client.CreateUser(ctx, req)
	if err != nil {
		t.Fatalf("replay CreateUser: %v", err)
	}
	if first.GetId() != second.GetId() {
		t.Fatalf("idempotent replay produced a new id: %s vs %s", first.GetId(), second.GetId())
	}
}

// TestE2E_Postgres_IdempotentCreate runs the same check against the postgres
// backend so the durable idempotency path (migration + transaction) is covered.
func TestE2E_Postgres_IdempotentCreate(t *testing.T) {
	dbURL := envOr("TEST_DATABASE_URL", defaultPostgresURL)
	cfg := newBaseTestConfig(t)
	cfg.Storage.Backend = "postgres"
	cfg.Storage.URL = dbURL
	cfg.Server.Timeout = 10 * time.Second
	cfg.Storage.PingTimeout = 5 * time.Second
	cfg.Storage.RetryAttempts = 1
	cfg.Storage.RetryBackoff = time.Second

	server, err := startPostgresServer(t, cfg)
	if err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	req := &pb.CreateUserRequest{Name: "PgIdem", Email: "pgidem@test.com", Age: 44, IdempotencyKey: "e2e-pg-key-1"}
	first, err := client.CreateUser(ctx, req)
	if err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	second, err := client.CreateUser(ctx, req)
	if err != nil {
		t.Fatalf("replay CreateUser: %v", err)
	}
	if first.GetId() != second.GetId() {
		t.Fatalf("postgres idempotent replay produced a new id: %s vs %s", first.GetId(), second.GetId())
	}
}
