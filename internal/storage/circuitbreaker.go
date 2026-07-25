package storage

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/example/grpc-service/pkg/pb"
)

// CircuitBreakerState represents the state of a circuit breaker.
type CircuitBreakerState int32

const (
	CircuitBreakerClosed   CircuitBreakerState = 0
	CircuitBreakerOpen     CircuitBreakerState = 1
	CircuitBreakerHalfOpen CircuitBreakerState = 2
)

func (s CircuitBreakerState) String() string {
	switch s {
	case CircuitBreakerClosed:
		return "closed"
	case CircuitBreakerOpen:
		return "open"
	case CircuitBreakerHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreakerConfig configures the repository circuit breaker.
type CircuitBreakerConfig struct {
	Enabled             bool          `yaml:"enabled"`
	FailureThreshold    int           `yaml:"failure_threshold"`
	SuccessThreshold    int           `yaml:"success_threshold"`
	OpenTimeout         time.Duration `yaml:"open_timeout"`
	MaxHalfOpenRequests int           `yaml:"max_half_open_requests"`
}

// DefaultCircuitBreakerConfig returns sensible defaults.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		Enabled:             false,
		FailureThreshold:    5,
		SuccessThreshold:    2,
		OpenTimeout:         30 * time.Second,
		MaxHalfOpenRequests: 1,
	}
}

// CircuitBreakerRepository wraps a Repository with circuit breaker logic.
type CircuitBreakerRepository struct {
	Repository
	cfg CircuitBreakerConfig

	state          atomic.Int32
	failureCount   atomic.Int64
	successCount   atomic.Int64
	halfOpenCount  atomic.Int64
	lastFailureAt  atomic.Int64
	openSince      atomic.Int64

	mu sync.Mutex
}

// NewCircuitBreakerRepository wraps repo with a circuit breaker. Zero-valued
// config fields fall back to DefaultCircuitBreakerConfig.
func NewCircuitBreakerRepository(repo Repository, cfg CircuitBreakerConfig) *CircuitBreakerRepository {
	defaults := DefaultCircuitBreakerConfig()
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = defaults.FailureThreshold
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = defaults.SuccessThreshold
	}
	if cfg.OpenTimeout <= 0 {
		cfg.OpenTimeout = defaults.OpenTimeout
	}
	if cfg.MaxHalfOpenRequests <= 0 {
		cfg.MaxHalfOpenRequests = defaults.MaxHalfOpenRequests
	}
	cb := &CircuitBreakerRepository{
		Repository: repo,
		cfg:        cfg,
	}
	cb.state.Store(int32(CircuitBreakerClosed))
	return cb
}

func (cb *CircuitBreakerRepository) State() CircuitBreakerState {
	return CircuitBreakerState(cb.state.Load())
}

// ResetForce resets the circuit breaker to the closed state and clears all
// internal counters. Use for manual intervention or testing.
func (cb *CircuitBreakerRepository) ResetForce() {
	cb.mu.Lock()
	cb.state.Store(int32(CircuitBreakerClosed))
	cb.ResetCounts()
	cb.mu.Unlock()
}

// ResetCounts resets the failure and success counters.
func (cb *CircuitBreakerRepository) ResetCounts() {
	cb.failureCount.Store(0)
	cb.successCount.Store(0)
	cb.halfOpenCount.Store(0)
}

// Counts returns the current failure, success, and half-open counts.
func (cb *CircuitBreakerRepository) Counts() (int64, int64, int64) {
	return cb.failureCount.Load(), cb.successCount.Load(), cb.halfOpenCount.Load()
}

func (cb *CircuitBreakerRepository) beforeCall() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch CircuitBreakerState(cb.state.Load()) {
	case CircuitBreakerOpen:
		openSince := time.Unix(0, cb.openSince.Load())
		if time.Since(openSince) > cb.cfg.OpenTimeout {
			cb.state.Store(int32(CircuitBreakerHalfOpen))
			cb.successCount.Store(0)
			cb.halfOpenCount.Store(0)
			cb.halfOpenCount.Add(1)
			return nil
		}
		return ErrCircuitOpen
	case CircuitBreakerHalfOpen:
		if cb.halfOpenCount.Load() >= int64(cb.cfg.MaxHalfOpenRequests) {
			return ErrCircuitOpen
		}
		cb.halfOpenCount.Add(1)
		return nil
	default:
		return nil
	}
}

func (cb *CircuitBreakerRepository) afterCall(err error) {
	if err == nil {
		switch CircuitBreakerState(cb.state.Load()) {
		case CircuitBreakerHalfOpen:
			cb.successCount.Add(1)
			if cb.successCount.Load() >= int64(cb.cfg.SuccessThreshold) {
				cb.mu.Lock()
				if CircuitBreakerState(cb.state.Load()) == CircuitBreakerHalfOpen {
					cb.state.Store(int32(CircuitBreakerClosed))
					cb.ResetCounts()
				}
				cb.mu.Unlock()
			}
		case CircuitBreakerClosed:
			cb.failureCount.Store(0)
		}
		return
	}

	if err == ErrUnavailable {
		return
	}

	if CircuitBreakerState(cb.state.Load()) == CircuitBreakerHalfOpen {
		cb.mu.Lock()
		cb.failureCount.Store(0)
		cb.state.Store(int32(CircuitBreakerOpen))
		cb.openSince.Store(time.Now().UnixNano())
		cb.mu.Unlock()
		return
	}

	cb.failureCount.Add(1)
	cb.lastFailureAt.Store(time.Now().UnixNano())

	if cb.failureCount.Load() >= int64(cb.cfg.FailureThreshold) {
		cb.mu.Lock()
		if cb.failureCount.Load() >= int64(cb.cfg.FailureThreshold) &&
			CircuitBreakerState(cb.state.Load()) == CircuitBreakerClosed {
			cb.state.Store(int32(CircuitBreakerOpen))
			cb.openSince.Store(time.Now().UnixNano())
		}
		cb.mu.Unlock()
	}
}

func (cb *CircuitBreakerRepository) Create(ctx context.Context, user *pb.User, maxUsers int) error {
	if err := cb.beforeCall(); err != nil {
		return err
	}
	err := cb.Repository.Create(ctx, user, maxUsers)
	cb.afterCall(err)
	return err
}

func (cb *CircuitBreakerRepository) Get(ctx context.Context, id string) (*pb.User, error) {
	if err := cb.beforeCall(); err != nil {
		return nil, err
	}
	u, err := cb.Repository.Get(ctx, id)
	cb.afterCall(err)
	return u, err
}

func (cb *CircuitBreakerRepository) Update(ctx context.Context, user *pb.User) error {
	if err := cb.beforeCall(); err != nil {
		return err
	}
	err := cb.Repository.Update(ctx, user)
	cb.afterCall(err)
	return err
}

func (cb *CircuitBreakerRepository) ReadModifyUpdate(ctx context.Context, id string, fn ReadModifyUpdateFn) (*pb.User, error) {
	if err := cb.beforeCall(); err != nil {
		return nil, err
	}
	u, err := cb.Repository.ReadModifyUpdate(ctx, id, fn)
	cb.afterCall(err)
	return u, err
}

func (cb *CircuitBreakerRepository) Delete(ctx context.Context, id string) error {
	if err := cb.beforeCall(); err != nil {
		return err
	}
	err := cb.Repository.Delete(ctx, id)
	cb.afterCall(err)
	return err
}

func (cb *CircuitBreakerRepository) Ping(ctx context.Context) error {
	if err := cb.beforeCall(); err != nil {
		return err
	}
	err := cb.Repository.Ping(ctx)
	cb.afterCall(err)
	return err
}

// Close resets the circuit breaker to closed and delegates Close to the inner
// repository.
func (cb *CircuitBreakerRepository) Close() error {
	cb.ResetForce()
	return cb.Repository.Close()
}
