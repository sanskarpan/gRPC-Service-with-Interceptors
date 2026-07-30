package storage

import (
	"context"
	"fmt"
	"testing"

	"github.com/example/grpc-service/pkg/pb"
)

func TestMemoryListPaginates(t *testing.T) {
	t.Parallel()
	r := NewMemoryRepository()
	ctx := context.Background()
	// Insert 5 users with sortable ids user-0..user-4.
	for i := 0; i < 5; i++ {
		if err := r.Create(ctx, &pb.User{Id: fmt.Sprintf("user-%d", i), Name: "n"}, 100); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// First page of 2.
	page1, next1, err := r.List(ctx, 2, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page1) != 2 || page1[0].GetId() != "user-0" || page1[1].GetId() != "user-1" {
		t.Fatalf("unexpected page1: %+v", page1)
	}
	if next1 != "user-1" {
		t.Fatalf("expected next cursor user-1, got %q", next1)
	}

	// Walk to the end and ensure every id is seen exactly once.
	seen := map[string]bool{}
	cursor := ""
	for {
		users, next, err := r.List(ctx, 2, cursor)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, u := range users {
			if seen[u.GetId()] {
				t.Fatalf("duplicate id %s", u.GetId())
			}
			seen[u.GetId()] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if len(seen) != 5 {
		t.Fatalf("expected 5 distinct users, saw %d", len(seen))
	}
}

func TestMemoryListRejectsNonPositiveLimit(t *testing.T) {
	t.Parallel()
	r := NewMemoryRepository()
	if _, _, err := r.List(context.Background(), 0, ""); err == nil {
		t.Fatal("expected error for limit 0")
	}
}
