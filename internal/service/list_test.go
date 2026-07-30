package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/example/grpc-service/internal/storage"
	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestListUsersPaginatesAndClamps(t *testing.T) {
	repo := storage.NewMemoryRepository()
	svc := NewUserServiceWithRepository(repo, ServiceLimits{MaxUsers: 100})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := repo.Create(ctx, &pb.User{Id: fmt.Sprintf("u-%d", i), Name: "n", Email: "e@x.com", Age: 20}, 100); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// page_size 0 -> default (returns all 3 here), token round-trips.
	resp, err := svc.ListUsers(ctx, &pb.ListUsersRequest{PageSize: 0})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(resp.GetUsers()) != 3 {
		t.Fatalf("expected 3 users, got %d", len(resp.GetUsers()))
	}
	if resp.GetNextPageToken() != "" {
		t.Fatalf("expected empty next token at end, got %q", resp.GetNextPageToken())
	}

	// page_size 2 -> first page + opaque token; second page returns the rest.
	p1, err := svc.ListUsers(ctx, &pb.ListUsersRequest{PageSize: 2})
	if err != nil {
		t.Fatalf("page1: %v", err)
	}
	if len(p1.GetUsers()) != 2 || p1.GetNextPageToken() == "" {
		t.Fatalf("unexpected page1: n=%d token=%q", len(p1.GetUsers()), p1.GetNextPageToken())
	}
	p2, err := svc.ListUsers(ctx, &pb.ListUsersRequest{PageSize: 2, PageToken: p1.GetNextPageToken()})
	if err != nil {
		t.Fatalf("page2: %v", err)
	}
	if len(p2.GetUsers()) != 1 || p2.GetNextPageToken() != "" {
		t.Fatalf("unexpected page2: n=%d token=%q", len(p2.GetUsers()), p2.GetNextPageToken())
	}
	if p2.GetUsers()[0].GetId() != "u-2" {
		t.Fatalf("expected u-2 on page2, got %s", p2.GetUsers()[0].GetId())
	}
}

func TestListUsersRejectsBadToken(t *testing.T) {
	svc := NewUserServiceWithRepository(storage.NewMemoryRepository(), ServiceLimits{MaxUsers: 10})
	_, err := svc.ListUsers(context.Background(), &pb.ListUsersRequest{PageToken: "!!!not-base64!!!"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("expected InvalidArgument for bad token, got %v", err)
	}
}
