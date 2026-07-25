package storage

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/example/grpc-service/pkg/pb"
	"google.golang.org/protobuf/proto"
)

type cacheEntry struct {
	user  *pb.User
	exp   time.Time
	stale bool
}

// CacheRepository wraps a Repository with an in-memory read-through cache
// that serves stale data when the underlying store is unavailable.
type CacheRepository struct {
	Repository
	ttl time.Duration

	mu sync.RWMutex
	m  map[string]*cacheEntry
}

// NewCacheRepository wraps repo with a TTL-based cache. Get operations
// populate the cache on success and serve stale entries when the underlying
// repo returns an error. Mutating operations invalidate the affected key.
func NewCacheRepository(repo Repository, ttl time.Duration) *CacheRepository {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &CacheRepository{
		Repository: repo,
		ttl:        ttl,
		m:          make(map[string]*cacheEntry),
	}
}

func (c *CacheRepository) get(key string) (*pb.User, bool) {
	c.mu.RLock()
	e, ok := c.m[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Now().After(e.exp) {
		return nil, false
	}
	return e.user, true
}

func (c *CacheRepository) getStale(key string) (*pb.User, bool) {
	c.mu.RLock()
	e, ok := c.m[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return e.user, true
}

func (c *CacheRepository) set(key string, user *pb.User) {
	c.mu.Lock()
	c.m[key] = &cacheEntry{
		user: proto.Clone(user).(*pb.User),
		exp:  time.Now().Add(c.ttl),
	}
	c.mu.Unlock()
}

func (c *CacheRepository) del(key string) {
	c.mu.Lock()
	delete(c.m, key)
	c.mu.Unlock()
}

// Get reads from cache first, falling through to the underlying repository
// on a miss. When the underlying repo fails with ErrUnavailable a stale
// entry is served if one exists.
func (c *CacheRepository) Get(ctx context.Context, id string) (*pb.User, error) {
	if u, ok := c.get(id); ok {
		return proto.Clone(u).(*pb.User), nil
	}
	u, err := c.Repository.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrUnavailable) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if stale, ok := c.getStale(id); ok {
				return proto.Clone(stale).(*pb.User), nil
			}
		}
		return nil, err
	}
	c.set(id, u)
	return proto.Clone(u).(*pb.User), nil
}

func (c *CacheRepository) Create(ctx context.Context, user *pb.User, maxUsers int) error {
	return c.Repository.Create(ctx, user, maxUsers)
}

func (c *CacheRepository) Update(ctx context.Context, user *pb.User) error {
	err := c.Repository.Update(ctx, user)
	if err == nil {
		c.del(user.GetId())
	}
	return err
}

func (c *CacheRepository) ReadModifyUpdate(ctx context.Context, id string, fn ReadModifyUpdateFn) (*pb.User, error) {
	u, err := c.Repository.ReadModifyUpdate(ctx, id, fn)
	if err == nil {
		c.del(id)
	} else if errors.Is(err, ErrUnavailable) {
		if stale, ok := c.getStale(id); ok {
			return proto.Clone(stale).(*pb.User), nil
		}
	}
	return u, err
}

func (c *CacheRepository) Delete(ctx context.Context, id string) error {
	err := c.Repository.Delete(ctx, id)
	if err == nil {
		c.del(id)
	}
	return err
}
