package storage

import (
	"context"
	"strings"
	"sync"

	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/protobuf/proto"
)

// MemoryRepository is a bounded, concurrency-safe repository for development
// and tests. It intentionally does not survive process restarts.
type MemoryRepository struct {
	mu     sync.RWMutex
	users  map[string]*pb.User
	closed bool
}

// NewMemoryRepository constructs an empty process-local repository.
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{users: make(map[string]*pb.User)}
}

// Create stores a cloned user unless maxUsers has already been reached.
func (r *MemoryRepository) Create(ctx context.Context, user *pb.User, maxUsers int) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if user == nil || user.GetId() == "" {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrUnavailable
	}
	if len(r.users) >= maxUsers {
		return ErrCapacity
	}
	if _, exists := r.users[user.GetId()]; exists {
		return ErrAlreadyExists
	}
	r.users[user.GetId()] = proto.Clone(user).(*pb.User)
	return nil
}

// Get returns a clone of a stored user.
func (r *MemoryRepository) Get(ctx context.Context, id string) (*pb.User, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.closed {
		return nil, ErrUnavailable
	}
	user, ok := r.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	return proto.Clone(user).(*pb.User), nil
}

// Update replaces a stored user with a clone.
func (r *MemoryRepository) Update(ctx context.Context, user *pb.User) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if user == nil || user.GetId() == "" {
		return ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrUnavailable
	}
	if _, ok := r.users[user.GetId()]; !ok {
		return ErrNotFound
	}
	r.users[user.GetId()] = proto.Clone(user).(*pb.User)
	return nil
}

// ReadModifyUpdate atomically loads a user, lets the caller mutate a private
// copy under the repository's write lock, and stores the result. It exists
// so the service layer can avoid a lost-update race between Get and Update.
func (r *MemoryRepository) ReadModifyUpdate(ctx context.Context, id string, mutate ReadModifyUpdateFn) (*pb.User, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(id) == "" {
		return nil, ErrInvalid
	}
	if mutate == nil {
		return nil, ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, ErrUnavailable
	}
	existing, ok := r.users[id]
	if !ok {
		return nil, ErrNotFound
	}
	working := proto.Clone(existing).(*pb.User)
	if err := mutate(working); err != nil {
		return nil, err
	}
	r.users[id] = proto.Clone(working).(*pb.User)
	return proto.Clone(working).(*pb.User), nil
}

// Delete removes a user or returns ErrNotFound.
func (r *MemoryRepository) Delete(ctx context.Context, id string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return ErrUnavailable
	}
	if _, ok := r.users[id]; !ok {
		return ErrNotFound
	}
	delete(r.users, id)
	return nil
}

// Close releases no external resources for the in-memory implementation.
func (r *MemoryRepository) Ping(ctx context.Context) error {
	return contextError(ctx)
}

// Close makes subsequent operations fail closed rather than accepting writes
// into state that the owner has declared unavailable.
func (r *MemoryRepository) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = true
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	return ctx.Err()
}
