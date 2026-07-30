package service

import (
	"context"
	"testing"

	"github.com/example/grpc-service/internal/storage"
	"github.com/example/grpc-service/pkg/pb"
)

func TestCreateUserIdempotencyKey(t *testing.T) {
	svc := NewUserServiceWithRepository(storage.NewMemoryRepository(), ServiceLimits{MaxUsers: 100})
	ctx := context.Background()
	req := &pb.CreateUserRequest{Name: "Ada", Email: "ada@example.com", Age: 30, IdempotencyKey: "order-42"}

	first, err := svc.CreateUser(ctx, req)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	// Retweeted request with the same key returns the SAME user, not a duplicate.
	second, err := svc.CreateUser(ctx, req)
	if err != nil {
		t.Fatalf("replay create: %v", err)
	}
	if second.GetId() != first.GetId() {
		t.Fatalf("idempotent replay returned a new id %s (want %s)", second.GetId(), first.GetId())
	}
	// Without a key, a new user is created each time.
	nokey := &pb.CreateUserRequest{Name: "Bo", Email: "bo@example.com", Age: 25}
	a, _ := svc.CreateUser(ctx, nokey)
	b, _ := svc.CreateUser(ctx, nokey)
	if a.GetId() == b.GetId() {
		t.Fatal("keyless creates must produce distinct users")
	}
}
