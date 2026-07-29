package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/grpc-service/pkg/config"
	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/grpc/codes"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const defaultPostgresURL = "postgres://orpheus:orpheus@127.0.0.1:5432/grpc_service_test?sslmode=disable"

// ──────────────────────────────────────────────────────────────────────────────
// Part 1: CRUD Happy Paths
// ──────────────────────────────────────────────────────────────────────────────

func TestE2E_CRUD_GetUser(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "GetUser Test", Email: "getuser@test.com", Age: 25})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := client.GetUser(ctx, &pb.GetUserRequest{Id: created.GetId()})
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if got.GetId() != created.GetId() || got.GetName() != "GetUser Test" || got.GetEmail() != "getuser@test.com" || got.GetAge() != 25 {
		t.Fatalf("GetUser = %+v, want id=%s name=%s email=%s age=25", got, created.GetId(), "GetUser Test", "getuser@test.com")
	}
	if got.GetCreatedAt() == nil || got.GetUpdatedAt() == nil {
		t.Fatal("GetUser timestamps are nil")
	}
}

func TestE2E_CRUD_CreateUser(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Create Test", Email: "create@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if created.GetId() == "" {
		t.Fatal("CreateUser returned empty id")
	}
	if created.GetCreatedAt() == nil {
		t.Fatal("CreateUser created_at is nil")
	}
	if created.GetUpdatedAt() == nil {
		t.Fatal("CreateUser updated_at is nil")
	}
	if created.GetCreatedAt().AsTime().IsZero() || created.GetUpdatedAt().AsTime().IsZero() {
		t.Fatal("CreateUser timestamps are zero")
	}
}

func TestE2E_CRUD_UpdateUser(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Original", Email: "original@test.com", Age: 35})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	updated, err := client.UpdateUser(ctx, &pb.UpdateUserRequest{Id: created.GetId(), Name: "Updated Name", Age: 36})
	if err != nil {
		t.Fatalf("UpdateUser: %v", err)
	}
	if updated.GetName() != "Updated Name" || updated.GetAge() != 36 {
		t.Fatalf("UpdateUser = name=%s age=%d, want name=Updated Name age=36", updated.GetName(), updated.GetAge())
	}

	got, err := client.GetUser(ctx, &pb.GetUserRequest{Id: created.GetId()})
	if err != nil {
		t.Fatalf("GetUser after update: %v", err)
	}
	if got.GetName() != "Updated Name" || got.GetAge() != 36 {
		t.Fatalf("persisted user = name=%s age=%d, want name=Updated Name age=36", got.GetName(), got.GetAge())
	}
}

func TestE2E_CRUD_DeleteUser(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Delete Me", Email: "delete@test.com", Age: 40})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.DeleteUser(ctx, &pb.DeleteUserRequest{Id: created.GetId()}); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := client.GetUser(ctx, &pb.GetUserRequest{Id: created.GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetUser after delete code = %v, want NotFound", status.Code(err))
	}
}

func TestE2E_CRUD_DeleteIdempotent(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Idemp Delet", Email: "idem@test.com", Age: 22})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := client.DeleteUser(ctx, &pb.DeleteUserRequest{Id: created.GetId()}); err != nil {
		t.Fatalf("first DeleteUser: %v", err)
	}
	if _, err := client.DeleteUser(ctx, &pb.DeleteUserRequest{Id: created.GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("second DeleteUser code = %v, want NotFound", status.Code(err))
	}
}

func TestE2E_CRUD_FullCycle(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Full Cycle", Email: "cycle@test.com", Age: 27})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := client.GetUser(ctx, &pb.GetUserRequest{Id: created.GetId()})
	if err != nil || got.GetName() != "Full Cycle" {
		t.Fatalf("GetUser: %+v, %v", got, err)
	}

	updated, err := client.UpdateUser(ctx, &pb.UpdateUserRequest{Id: created.GetId(), Name: "Full Cycle Updated", Age: 28})
	if err != nil || updated.GetName() != "Full Cycle Updated" || updated.GetAge() != 28 {
		t.Fatalf("UpdateUser: %+v, %v", updated, err)
	}

	verified, err := client.GetUser(ctx, &pb.GetUserRequest{Id: created.GetId()})
	if err != nil || verified.GetName() != "Full Cycle Updated" {
		t.Fatalf("GetUser verify: %+v, %v", verified, err)
	}

	if _, err := client.DeleteUser(ctx, &pb.DeleteUserRequest{Id: created.GetId()}); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := client.GetUser(ctx, &pb.GetUserRequest{Id: created.GetId()}); status.Code(err) != codes.NotFound {
		t.Fatalf("GetUser after delete code = %v, want NotFound", status.Code(err))
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Part 2: Validation Error Paths
// ──────────────────────────────────────────────────────────────────────────────

func TestE2E_Validation_CreateEmptyName(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Email: "empty@test.com", Age: 30})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateUser empty name code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestE2E_Validation_CreateWhitespaceName(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "   ", Email: "ws@test.com", Age: 30})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateUser whitespace name code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestE2E_Validation_CreateInvalidEmail(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Bad Email", Email: "notanemail", Age: 30})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateUser invalid email code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestE2E_Validation_CreateNegativeAge(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Bad Age", Email: "badage@test.com", Age: -1})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateUser negative age code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestE2E_Validation_CreateOverAge(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Old Person", Email: "old@test.com", Age: 151})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateUser age 151 code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestE2E_Validation_CreateLongName(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	longName := strings.Repeat("x", cfg.Server.MaxStringBytes+1)
	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: longName, Email: "long@test.com", Age: 30})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("CreateUser long name code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestE2E_Validation_GetEmptyID(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.GetUser(ctx, &pb.GetUserRequest{Id: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetUser empty id code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestE2E_Validation_UpdateEmptyID(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.UpdateUser(ctx, &pb.UpdateUserRequest{Id: "", Name: "No ID", Age: 30})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("UpdateUser empty id code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestE2E_Validation_DeleteEmptyID(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.DeleteUser(ctx, &pb.DeleteUserRequest{Id: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("DeleteUser empty id code = %v, want InvalidArgument", status.Code(err))
	}
}

func TestE2E_Validation_NilRequest(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.GetUser(ctx, &pb.GetUserRequest{Id: ""})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("GetUser nil-like request code = %v, want InvalidArgument", status.Code(err))
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Part 3: Auth Paths
// ──────────────────────────────────────────────────────────────────────────────

func TestE2E_Auth_APITokenValid(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	user, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Auth Valid", Email: "authvalid@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser with valid API key: %v", err)
	}
	if user.GetId() == "" {
		t.Fatal("CreateUser returned empty id")
	}
}

func TestE2E_Auth_APITokenInvalid(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), "wrong-api-key")

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Bad Auth", Email: "badauth@test.com", Age: 30})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("invalid API key code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestE2E_Auth_APITokenMissing(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := context.Background()

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "No Auth", Email: "noauth@test.com", Age: 30})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("missing API key code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestE2E_Auth_APITokenEmpty(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := metadata.AppendToOutgoingContext(context.Background(), "x-api-key", "")

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Empty Key", Email: "emptykey@test.com", Age: 30})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("empty API key code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestE2E_Auth_JWTValid(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	token := generateTestJWT(testJWTSecret, time.Now().Add(time.Hour), nil)
	ctx := jwtAuthorizationContext(context.Background(), token)

	user, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "JWT User", Email: "jwt@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser with valid JWT: %v", err)
	}
	if user.GetId() == "" {
		t.Fatal("CreateUser returned empty id")
	}
}

func TestE2E_Auth_JWTExpired(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	token := generateTestJWT(testJWTSecret, time.Now().Add(-time.Hour), nil)
	ctx := jwtAuthorizationContext(context.Background(), token)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Exp JWT", Email: "expjwt@test.com", Age: 30})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("expired JWT code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestE2E_Auth_JWTWrongSecret(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	token := generateTestJWT("different-secret-key-that-doesnt-match", time.Now().Add(time.Hour), nil)
	ctx := jwtAuthorizationContext(context.Background(), token)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Fake JWT", Email: "fakejwt@test.com", Age: 30})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("wrong secret JWT code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestE2E_Auth_JWTMalformed(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := jwtAuthorizationContext(context.Background(), "not.a.jwt")

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Bad JWT", Email: "badjwt@test.com", Age: 30})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("malformed JWT code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestE2E_Auth_JWTNoExp(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	token := generateTestJWTWithoutExp(testJWTSecret)
	ctx := jwtAuthorizationContext(context.Background(), token)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "No Exp JWT", Email: "noexp@test.com", Age: 30})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("JWT without exp code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestE2E_Auth_JWTBadAlg(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	token := generateTestJWTWithAlg(testJWTSecret, "none", time.Now().Add(time.Hour), nil)
	ctx := jwtAuthorizationContext(context.Background(), token)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "None Alg", Email: "nonealg@test.com", Age: 30})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("alg=none JWT code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestE2E_Auth_NbfFuture(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	token := generateTestJWT(testJWTSecret, time.Now().Add(time.Hour), map[string]any{
		"nbf": time.Now().Add(time.Hour).Unix(),
	})
	ctx := jwtAuthorizationContext(context.Background(), token)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "NBF Future", Email: "nbf@test.com", Age: 30})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("future nbf JWT code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestE2E_Auth_IatFarFuture(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	token := generateTestJWT(testJWTSecret, time.Now().Add(time.Hour), map[string]any{
		"iat": time.Now().Add(10 * time.Minute).Unix(),
	})
	ctx := jwtAuthorizationContext(context.Background(), token)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "IAT Future", Email: "iat@test.com", Age: 30})
	if status.Code(err) != codes.Unauthenticated {
		t.Fatalf("far-future iat JWT code = %v, want Unauthenticated", status.Code(err))
	}
}

func TestE2E_Auth_IatWithinSkew(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	token := generateTestJWT(testJWTSecret, time.Now().Add(time.Hour), map[string]any{
		"iat": time.Now().Add(10 * time.Second).Unix(),
	})
	ctx := jwtAuthorizationContext(context.Background(), token)

	user, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "IAT Skew", Email: "iatskew@test.com", Age: 30})
	if err != nil {
		t.Fatalf("iat within skew JWT: %v", err)
	}
	if user.GetId() == "" {
		t.Fatal("CreateUser returned empty id")
	}
}

func TestE2E_Auth_AmbiguousPrefersBearer(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	token := generateTestJWT(testJWTSecret, time.Now().Add(time.Hour), nil)
	md := metadata.Pairs("authorization", "Bearer "+token, "x-api-key", "wrong-api-key")
	ctx := metadata.NewOutgoingContext(context.Background(), md)

	user, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Ambiguous", Email: "ambig@test.com", Age: 30})
	if err != nil {
		t.Fatalf("ambiguous auth (prefers Bearer): %v", err)
	}
	if user.GetId() == "" {
		t.Fatal("CreateUser returned empty id")
	}
}

func TestE2E_Auth_HealthExempt(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	healthClient := grpc_health_v1.NewHealthClient(conn)
	ctx := context.Background()

	resp, err := healthClient.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("unauthenticated health Check: %v", err)
	}
	if resp.GetStatus() != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("health status = %v, want SERVING", resp.GetStatus())
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Part 4: Streaming Paths
// ──────────────────────────────────────────────────────────────────────────────

func TestE2E_Stream_ServerStreamEvents(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "SSE User", Email: "sse@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	stream, err := client.StreamUserEvents(ctx, &pb.StreamUserEventsRequest{
		UserId:     created.GetId(),
		EventTypes: []string{"test_event"},
	})
	if err != nil {
		t.Fatalf("StreamUserEvents: %v", err)
	}
	count := 0
	for {
		event, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatalf("StreamUserEvents Recv: %v", recvErr)
		}
		if event.GetUserId() != created.GetId() {
			t.Fatalf("event user_id = %q, want %q", event.GetUserId(), created.GetId())
		}
		count++
	}
	if count != cfg.Server.MaxStreamMessages {
		t.Fatalf("StreamUserEvents received %d events, want %d", count, cfg.Server.MaxStreamMessages)
	}
}

func TestE2E_Stream_ClientCollectMetrics(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Metrics Src", Email: "msrc@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	stream, err := client.CollectUserMetrics(ctx)
	if err != nil {
		t.Fatalf("CollectUserMetrics: %v", err)
	}
	values := []float64{10, 20, 30, 40, 50}
	for _, v := range values {
		if err := stream.Send(&pb.UserMetric{
			UserId:     created.GetId(),
			MetricName: "test_metric",
			Value:      v,
		}); err != nil {
			t.Fatalf("CollectUserMetrics Send: %v", err)
		}
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CollectUserMetrics CloseAndRecv: %v", err)
	}
	if resp.GetCount() != 5 {
		t.Fatalf("count = %d, want 5", resp.GetCount())
	}
	if resp.GetSum() != 150 {
		t.Fatalf("sum = %v, want 150", resp.GetSum())
	}
	if resp.GetAvg() != 30 {
		t.Fatalf("avg = %v, want 30", resp.GetAvg())
	}
}

func TestE2E_Stream_BidiChat(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Chat User", Email: "chat@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	chat, err := client.ChatStream(ctx)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	messages := []string{"hello", "world", "test"}
	for _, m := range messages {
		if err := chat.Send(&pb.ChatMessage{UserId: created.GetId(), Message: m}); err != nil {
			t.Fatalf("ChatStream Send: %v", err)
		}
	}
	for i := 0; i < len(messages); i++ {
		reply, recvErr := chat.Recv()
		if recvErr != nil {
			t.Fatalf("ChatStream Recv: %v", recvErr)
		}
		if reply.GetId() == "" {
			t.Fatal("ChatStream reply has empty id")
		}
	}
	if err := chat.CloseSend(); err != nil {
		t.Fatalf("ChatStream CloseSend: %v", err)
	}
	if _, err := chat.Recv(); err != io.EOF {
		t.Fatalf("ChatStream final Recv = %v, want io.EOF", err)
	}
}

func TestE2E_Stream_ServerStreamCancelled(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	cfg.Server.MaxStreamMessages = 1000
	cfg.Server.StreamInterval = 50 * time.Millisecond
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	authCtx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(authCtx, &pb.CreateUserRequest{Name: "Cancel Stream", Email: "cancelstream@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	streamCtx, streamCancel := context.WithCancel(authCtx)
	stream, err := client.StreamUserEvents(streamCtx, &pb.StreamUserEventsRequest{
		UserId: created.GetId(), EventTypes: []string{"e"},
	})
	if err != nil {
		t.Fatalf("StreamUserEvents: %v", err)
	}
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("first Recv: %v", err)
	}
	streamCancel()

	for {
		_, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				continue
			}
			if !isCancellation(err) {
				t.Fatalf("Recv after cancel = %v, want cancellation", err)
			}
			break
		}
	}
}

func TestE2E_Stream_ServerStreamDeadline(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	cfg.Server.MaxStreamMessages = 1000
	cfg.Server.StreamInterval = 100 * time.Millisecond
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	authCtx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(authCtx, &pb.CreateUserRequest{Name: "Deadline Stream", Email: "deadline@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	streamCtx, cancel := context.WithTimeout(authCtx, 50*time.Millisecond)
	defer cancel()
	stream, err := client.StreamUserEvents(streamCtx, &pb.StreamUserEventsRequest{
		UserId: created.GetId(), EventTypes: []string{"e"},
	})
	if err != nil {
		t.Fatalf("StreamUserEvents: %v", err)
	}
	for {
		_, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				continue
			}
			if !isCancellation(err) {
				t.Fatalf("Recv after deadline = %v, want cancellation", err)
			}
			break
		}
	}
}

func TestE2E_Stream_ClientCollectMetricsEmptyStream(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	stream, err := client.CollectUserMetrics(ctx)
	if err != nil {
		t.Fatalf("CollectUserMetrics: %v", err)
	}
	resp, err := stream.CloseAndRecv()
	if err != nil {
		t.Fatalf("CollectUserMetrics empty CloseAndRecv: %v", err)
	}
	if resp.GetCount() != 0 || resp.GetSum() != 0 || resp.GetAvg() != 0 {
		t.Fatalf("empty metrics = count=%d sum=%v avg=%v, want all zero", resp.GetCount(), resp.GetSum(), resp.GetAvg())
	}
}

func TestE2E_Stream_BidiEarlyCloseSend(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Early Close", Email: "earlyclose@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	chat, err := client.ChatStream(ctx)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if err := chat.Send(&pb.ChatMessage{UserId: created.GetId(), Message: "only one"}); err != nil {
		t.Fatalf("ChatStream Send: %v", err)
	}
	reply, err := chat.Recv()
	if err != nil || reply.GetId() == "" {
		t.Fatalf("ChatStream first Recv = %+v, %v", reply, err)
	}
	if err := chat.CloseSend(); err != nil {
		t.Fatalf("ChatStream CloseSend: %v", err)
	}
	if _, err := chat.Recv(); err != io.EOF {
		t.Fatalf("ChatStream final Recv = %v, want io.EOF", err)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Part 5: Health & Metrics
// ──────────────────────────────────────────────────────────────────────────────

func TestE2E_Health_LivenessAlways(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	resp, err := http.Get(server.healthURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}
}

func TestE2E_Health_ReadinessBeforeServer(t *testing.T) {
	cfg := newBaseTestConfig(t)
	healthURL := "http://127.0.0.1:" + itoa(cfg.Health.Port)
	resp, err := http.Get(healthURL + "/readyz")
	if err == nil {
		resp.Body.Close()
		t.Fatal("/readyz succeeded before server started, expected connection refused")
	}
}

func TestE2E_Health_ReadinessAfterServer(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	resp, err := http.Get(server.healthURL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/readyz status = %d, want 200", resp.StatusCode)
	}
}

func TestE2E_Health_ReadinessAfterShutdown(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Server.Timeout = 5 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, cfg) }()
	healthURL := "http://127.0.0.1:" + itoa(cfg.Health.Port)
	waitForStatus(t, healthURL+"/readyz", http.StatusOK)

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("run(): %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run() did not drain")
	}

	// After run() has fully drained, the health listener is closed. /readyz must
	// therefore be either unreachable (connection refused) or, if somehow still
	// answering, report not-ready. It must never still return 200.
	resp, err := http.Get(healthURL + "/readyz")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Fatalf("/readyz returned 200 after full shutdown; readiness must not report ready")
		}
	}
}

func TestE2E_Metrics_CountersPresent(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Metrics.Enabled = true
	cfg.Metrics.Port = freePort(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)
	client.CreateUser(ctx, &pb.CreateUserRequest{Name: "M0", Email: "m0@test.com", Age: 30})

	metricsURL := "http://127.0.0.1:" + itoa(cfg.Metrics.Port) + "/metrics"
	body := getHTTP(t, metricsURL)
	if !strings.Contains(body, "grpc_server_total_requests") {
		t.Fatal("metrics body missing grpc_server_total_requests")
	}
}

func TestE2E_Metrics_RequestsIncrement(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Metrics.Enabled = true
	cfg.Metrics.Port = freePort(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	if _, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "M1", Email: "m1@test.com", Age: 30}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	metricsURL := "http://127.0.0.1:" + itoa(cfg.Metrics.Port) + "/metrics"
	body := getHTTP(t, metricsURL)
	if !strings.Contains(body, `example.v1.UserService/CreateUser`) {
		t.Fatal("metrics body missing CreateUser method entry")
	}
}

func TestE2E_Metrics_ErrorsIncrement(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Metrics.Enabled = true
	cfg.Metrics.Port = freePort(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)

	_, _ = client.CreateUser(context.Background(), &pb.CreateUserRequest{Name: "Err", Email: "err@test.com", Age: 30})

	metricsURL := "http://127.0.0.1:" + itoa(cfg.Metrics.Port) + "/metrics"
	body := getHTTP(t, metricsURL)
	if !strings.Contains(body, "grpc_server_total_errors") {
		t.Fatal("metrics body missing grpc_server_total_errors")
	}
}

func TestE2E_Reflection_Disabled(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	cfg.Reflection = false
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()

	err := conn.Invoke(context.Background(), "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo", nil, nil)
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("reflection disabled Invoke code = %v, want Unimplemented", status.Code(err))
	}
}

func TestE2E_Reflection_Enabled(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	cfg.Reflection = true
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()

	err := conn.Invoke(context.Background(), "/grpc.reflection.v1.ServerReflection/ServerReflectionInfo", nil, nil)
	if status.Code(err) == codes.Unimplemented {
		t.Fatal("reflection enabled but Invoke returned Unimplemented")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Part 6: Rate Limit & Timeout
// ──────────────────────────────────────────────────────────────────────────────

func TestE2E_RateLimit_Triggers(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Server.RateLimitPerSecond = 1
	cfg.Server.RateLimitBurst = 1
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	var exhausted bool
	for i := range 10 {
		_, err := client.CreateUser(ctx, &pb.CreateUserRequest{
			Name:  "RL " + strconv.Itoa(i),
			Email: fmt.Sprintf("rl%d@test.com", i),
			Age:   30,
		})
		if status.Code(err) == codes.ResourceExhausted {
			exhausted = true
			break
		}
		if err != nil && status.Code(err) != codes.ResourceExhausted {
			t.Logf("unexpected error at iteration %d: %v", i, err)
		}
	}
	if !exhausted {
		t.Fatal("rate limit never triggered after 10 requests with limit=1 burst=1")
	}
}

func TestE2E_Timeout_Triggers(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Server.Timeout = time.Nanosecond
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Timeout T", Email: "timeout@test.com", Age: 30})
	if err == nil {
		t.Fatal("CreateUser succeeded with 1ns timeout, expected DeadlineExceeded")
	}
	if status.Code(err) != codes.DeadlineExceeded && status.Code(err) != codes.Canceled {
		t.Fatalf("CreateUser with 1ns timeout code = %v, want DeadlineExceeded or Canceled", status.Code(err))
	}
}

func TestE2E_Capacity_Triggers(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Server.MaxUsers = 1
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Cap 1", Email: "cap1@test.com", Age: 30})
	if err != nil {
		t.Fatalf("first CreateUser: %v", err)
	}
	_, err = client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Cap 2", Email: "cap2@test.com", Age: 30})
	if status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("second CreateUser code = %v, want ResourceExhausted", status.Code(err))
	}
}

func TestE2E_LargeMessage_Rejected(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	cfg.Server.MaxStringBytes = 10 * 1024 * 1024
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	bigName := strings.Repeat("x", 2*1024*1024)
	_, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: bigName, Email: "big@test.com", Age: 30})
	if err == nil {
		t.Fatal("large message CreateUser succeeded, expected ResourceExhausted")
	}
	code := status.Code(err)
	if code != codes.ResourceExhausted && code != codes.InvalidArgument {
		t.Fatalf("large message code = %v, want ResourceExhausted or InvalidArgument", code)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Part 7: Concurrent & Resilience
// ──────────────────────────────────────────────────────────────────────────────

func TestE2E_Concurrent_CRUDNoLostWrites(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Server.MaxUsers = 1000
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	var wg sync.WaitGroup
	errs := make(chan error, 10)
	for i := range 10 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			created, err := client.CreateUser(ctx, &pb.CreateUserRequest{
				Name:  fmt.Sprintf("Concurrent %d", idx),
				Email: fmt.Sprintf("concurrent%d@test.com", idx),
				Age:   int32(20 + idx),
			})
			if err != nil {
				errs <- fmt.Errorf("goroutine %d CreateUser: %w", idx, err)
				return
			}
			if _, err := client.UpdateUser(ctx, &pb.UpdateUserRequest{
				Id: created.GetId(), Name: fmt.Sprintf("Updated %d", idx), Age: int32(21 + idx),
			}); err != nil {
				errs <- fmt.Errorf("goroutine %d UpdateUser: %w", idx, err)
				return
			}
			if _, err := client.DeleteUser(ctx, &pb.DeleteUserRequest{Id: created.GetId()}); err != nil {
				errs <- fmt.Errorf("goroutine %d DeleteUser: %w", idx, err)
				return
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestE2E_Concurrent_GetUsers(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Get Concurrent", Email: "getconcurrent@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			user, e := client.GetUser(ctx, &pb.GetUserRequest{Id: created.GetId()})
			if e != nil {
				errs <- e
				return
			}
			if user.GetId() != created.GetId() {
				errs <- fmt.Errorf("GetUser returned wrong id: %s", user.GetId())
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
}

func TestE2E_GracefulShutdown_DrainsInFlight(t *testing.T) {
	cfg := newBaseTestConfig(t)
	cfg.Server.MaxStreamMessages = 10
	cfg.Server.StreamInterval = 100 * time.Millisecond
	server := startTestServer(t, cfg)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Drain Test", Email: "drain@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	stream, err := client.StreamUserEvents(ctx, &pb.StreamUserEventsRequest{
		UserId: created.GetId(), EventTypes: []string{"drain"},
	})
	if err != nil {
		t.Fatalf("StreamUserEvents: %v", err)
	}

	// Drain at least one event, then start shutdown
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("first Recv: %v", err)
	}

	shutdownDone := make(chan struct{})
	go func() {
		server.Stop(t)
		close(shutdownDone)
	}()

	// Continue receiving events; with GracefulStop they should finish.
	count := 1
	for {
		_, recvErr := stream.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatalf("StreamUserEvents Recv during drain: %v", recvErr)
		}
		count++
	}
	if count != cfg.Server.MaxStreamMessages {
		t.Fatalf("received %d events, want %d (all events should drain)", count, cfg.Server.MaxStreamMessages)
	}

	<-shutdownDone
}

// TestE2E_ServiceRemainsOperationalAfterInvalidInput verifies that a request
// which the handler rejects (invalid argument) does not wedge the server: a
// subsequent valid request still succeeds. Panic-to-Internal recovery of the
// wired UnaryPanicRecoveryInterceptor is covered directly by the unit tests
// in pkg/interceptors (the real UserService has no panic path to exercise
// end-to-end).
func TestE2E_ServiceRemainsOperationalAfterInvalidInput(t *testing.T) {
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	// A rejected request must not affect server liveness.
	if _, err := client.GetUser(ctx, &pb.GetUserRequest{Id: ""}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for empty id, got %v", err)
	}

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Still Up", Email: "stillup@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser after rejected request: %v", err)
	}
	got, err := client.GetUser(ctx, &pb.GetUserRequest{Id: created.GetId()})
	if err != nil {
		t.Fatalf("GetUser after rejected request: %v", err)
	}
	if got.GetId() != created.GetId() {
		t.Fatalf("expected id %q, got %q", created.GetId(), got.GetId())
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Part 8: Postgres Backend
// ──────────────────────────────────────────────────────────────────────────────

func TestE2E_Postgres_CRUDCycle(t *testing.T) {
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

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "PG CRUD", Email: "pgcrud@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	got, err := client.GetUser(ctx, &pb.GetUserRequest{Id: created.GetId()})
	if err != nil || got.GetName() != "PG CRUD" {
		t.Fatalf("GetUser: %+v, %v", got, err)
	}

	updated, err := client.UpdateUser(ctx, &pb.UpdateUserRequest{Id: created.GetId(), Name: "PG Updated", Age: 31})
	if err != nil || updated.GetAge() != 31 {
		t.Fatalf("UpdateUser: %+v, %v", updated, err)
	}

	if _, err := client.DeleteUser(ctx, &pb.DeleteUserRequest{Id: created.GetId()}); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
}

func TestE2E_Postgres_RestartRecovers(t *testing.T) {
	dbURL := envOr("TEST_DATABASE_URL", defaultPostgresURL)
	cfg := newBaseTestConfig(t)
	cfg.Storage.Backend = "postgres"
	cfg.Storage.URL = dbURL
	cfg.Server.Timeout = 10 * time.Second
	cfg.Storage.PingTimeout = 5 * time.Second
	cfg.Storage.RetryAttempts = 2
	cfg.Storage.RetryBackoff = time.Second

	server1, err := startPostgresServer(t, cfg)
	if err != nil {
		t.Skipf("postgres not available: %v", err)
	}

	conn1 := dialTestClient(t, server1.grpcAddr)
	client1 := pb.NewUserServiceClient(conn1)
	ctx := authContext(context.Background(), testAPIKey)
	created, err := client1.CreateUser(ctx, &pb.CreateUserRequest{Name: "Restart Test", Email: "restart@test.com", Age: 25})
	if err != nil {
		conn1.Close()
		server1.Stop(t)
		t.Fatalf("CreateUser: %v", err)
	}
	userId := created.GetId()
	conn1.Close()
	server1.Stop(t)

	cfg2 := newBaseTestConfig(t)
	cfg2.Storage.Backend = "postgres"
	cfg2.Storage.URL = dbURL
	cfg2.Server.Timeout = 10 * time.Second
	cfg2.Storage.PingTimeout = 5 * time.Second
	cfg2.Storage.RetryAttempts = 2
	cfg2.Storage.RetryBackoff = time.Second

	server2, err2 := startPostgresServer(t, cfg2)
	if err2 != nil {
		t.Skipf("postgres not available on restart: %v", err2)
	}
	defer server2.Stop(t)

	conn2 := dialTestClient(t, server2.grpcAddr)
	defer func() { _ = conn2.Close() }()
	client2 := pb.NewUserServiceClient(conn2)
	ctx2 := authContext(context.Background(), testAPIKey)

	got, err := client2.GetUser(ctx2, &pb.GetUserRequest{Id: userId})
	if err != nil {
		t.Fatalf("GetUser after restart: %v", err)
	}
	if got.GetName() != "Restart Test" {
		t.Fatalf("GetUser name = %q, want %q", got.GetName(), "Restart Test")
	}
}

func TestE2E_Postgres_MigrationsIdempotent(t *testing.T) {
	dbURL := envOr("TEST_DATABASE_URL", defaultPostgresURL)
	cfg := newBaseTestConfig(t)
	cfg.Storage.Backend = "postgres"
	cfg.Storage.URL = dbURL
	cfg.Server.Timeout = 10 * time.Second
	cfg.Storage.PingTimeout = 5 * time.Second
	cfg.Storage.RetryAttempts = 2
	cfg.Storage.RetryBackoff = time.Second

	server1, err := startPostgresServer(t, cfg)
	if err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	server1.Stop(t)

	cfg2 := newBaseTestConfig(t)
	cfg2.Storage.Backend = "postgres"
	cfg2.Storage.URL = dbURL
	cfg2.Server.Timeout = 10 * time.Second
	cfg2.Storage.PingTimeout = 5 * time.Second
	cfg2.Storage.RetryAttempts = 2
	cfg2.Storage.RetryBackoff = time.Second

	server2, err2 := startPostgresServer(t, cfg2)
	if err2 != nil {
		t.Skipf("postgres not available on second start: %v", err2)
	}
	defer server2.Stop(t)

	conn := dialTestClient(t, server2.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	created, err := client.CreateUser(ctx, &pb.CreateUserRequest{Name: "Migrations OK", Email: "migrations@test.com", Age: 30})
	if err != nil {
		t.Fatalf("CreateUser after restart: %v", err)
	}
	if created.GetId() == "" {
		t.Fatal("CreateUser returned empty id")
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Part 9: Error Propagation
// ──────────────────────────────────────────────────────────────────────────────

func TestE2E_UpdateNonExistentUser(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.UpdateUser(ctx, &pb.UpdateUserRequest{Id: "user-nonexistent", Name: "Ghost", Age: 30})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("UpdateUser nonexistent code = %v, want NotFound", status.Code(err))
	}
}

func TestE2E_GetNonExistentUser(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.GetUser(ctx, &pb.GetUserRequest{Id: "user-nonexistent"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("GetUser nonexistent code = %v, want NotFound", status.Code(err))
	}
}

func TestE2E_DeleteNonExistentUser(t *testing.T) {
	t.Parallel()
	cfg := newBaseTestConfig(t)
	server := startTestServer(t, cfg)
	defer server.Stop(t)

	conn := dialTestClient(t, server.grpcAddr)
	defer func() { _ = conn.Close() }()
	client := pb.NewUserServiceClient(conn)
	ctx := authContext(context.Background(), testAPIKey)

	_, err := client.DeleteUser(ctx, &pb.DeleteUserRequest{Id: "user-nonexistent"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("DeleteUser nonexistent code = %v, want NotFound", status.Code(err))
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// Helpers needed only by this file
// ──────────────────────────────────────────────────────────────────────────────

// generateTestJWTWithoutExp creates a JWT without an exp claim.
func generateTestJWTWithoutExp(secret string) string {
	header := map[string]any{"alg": "HS256", "typ": "JWT"}
	payload := map[string]any{}
	return signJWTPayload(secret, header, payload)
}

// generateTestJWTWithAlg creates a JWT with a custom alg value.
func generateTestJWTWithAlg(secret, alg string, exp time.Time, extraClaims map[string]any) string {
	header := map[string]any{"alg": alg, "typ": "JWT"}
	payload := map[string]any{"exp": exp.Unix()}
	for k, v := range extraClaims {
		payload[k] = v
	}
	return signJWTPayload(secret, header, payload)
}

func signJWTPayload(secret string, header, payload map[string]any) string {
	hBytes, _ := json.Marshal(header)
	pBytes, _ := json.Marshal(payload)
	hB64 := base64.RawURLEncoding.EncodeToString(hBytes)
	pB64 := base64.RawURLEncoding.EncodeToString(pBytes)
	input := hB64 + "." + pB64
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(input))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return input + "." + sig
}

// startPostgresServer attempts to start a server with Postgres backend.
func startPostgresServer(t *testing.T, cfg *config.Config) (*runningServer, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- run(ctx, cfg) }()

	healthURL := "http://127.0.0.1:" + itoa(cfg.Health.Port)
	var lastErr error
	for range 100 {
		select {
		case err := <-errCh:
			cancel()
			return nil, err
		default:
		}
		resp, pollErr := http.Get(healthURL + "/readyz")
		if pollErr == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return &runningServer{
					cancel:    cancel,
					runErr:    errCh,
					healthURL: healthURL,
					grpcAddr:  "127.0.0.1:" + itoa(cfg.Server.Port),
				}, nil
			}
		}
		lastErr = pollErr
		time.Sleep(100 * time.Millisecond)
	}
	cancel()
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("postgres server never became ready")
}
