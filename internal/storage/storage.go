// Package storage contains repository implementations for the UserService.
// The service layer depends on this narrow contract so persistence can be
// tested independently and replaced without leaking database types outward.
package storage

import (
	"context"
	"errors"

	"github.com/example/grpc-service/pkg/pb"
)

// Repository is the persistence contract used by UserService. Implementations
// must clone protobuf values at their boundary and return ErrNotFound for
// missing records. Create enforces maxUsers transactionally where supported.
// ReadModifyUpdate atomically loads, mutates, and persists a user so callers
// do not introduce a lost-write race by performing separate Get + Update
// operations.
type Repository interface {
	Create(context.Context, *pb.User, int) error
	// CreateWithIdempotency behaves like Create, but when idempotencyKey is
	// non-empty a retry with the same key returns the previously created user
	// (replayed=true) instead of creating a duplicate. An empty key behaves
	// exactly like Create.
	CreateWithIdempotency(ctx context.Context, user *pb.User, maxUsers int, idempotencyKey string) (stored *pb.User, replayed bool, err error)
	Get(context.Context, string) (*pb.User, error)
	Update(context.Context, *pb.User) error
	ReadModifyUpdate(context.Context, string, ReadModifyUpdateFn) (*pb.User, error)
	Delete(context.Context, string) error
	// List returns up to limit users ordered by id, starting strictly after
	// afterID (keyset pagination; empty afterID starts from the beginning), and
	// the id to use as the next afterID ("" when the page is the last one).
	List(ctx context.Context, limit int, afterID string) (users []*pb.User, nextAfterID string, err error)
	Ping(context.Context) error
	Close() error
}

// ReadModifyUpdateFn is invoked with the freshly loaded user and may mutate
// it. The returned error aborts the operation and is propagated to the
// caller. The pointer is private to the repository; callers must not retain
// it past the call.
type ReadModifyUpdateFn func(current *pb.User) error

// ErrNotFound indicates that a requested user does not exist.
var ErrNotFound = errors.New("user not found")

// ErrCapacity indicates that the configured user limit has been reached.
var ErrCapacity = errors.New("user capacity reached")

// ErrAlreadyExists indicates that a resource identifier is already stored.
var ErrAlreadyExists = errors.New("user already exists")

// ErrInvalid indicates a value rejected by the persistence contract.
var ErrInvalid = errors.New("invalid persisted user")

// ErrUnavailable indicates a persistence dependency failure.
var ErrUnavailable = errors.New("storage unavailable")

// ErrCircuitOpen indicates the circuit breaker is open and requests are
// being rejected without attempting the underlying operation.
var ErrCircuitOpen = errors.New("circuit breaker open")
