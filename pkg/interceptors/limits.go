package interceptors

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NewRateLimitInterceptors creates process-wide admission controls for unary
// and streaming RPCs. Rejected work fails immediately, keeping overload from
// becoming an unbounded queue of goroutines and messages.
func NewRateLimitInterceptors(perSecond, burst int) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	if perSecond < 1 {
		perSecond = 1
	}
	if burst < 1 {
		burst = 1
	}
	limiter := rate.NewLimiter(rate.Limit(perSecond), burst)
	allow := func() bool { return limiter.Allow() }
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			if !allow() {
				return nil, status.Error(codes.ResourceExhausted, "request rate limit exceeded")
			}
			return handler(ctx, req)
		}, func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if !allow() {
				return status.Error(codes.ResourceExhausted, "request rate limit exceeded")
			}
			return handler(srv, stream)
		}
}

// clientLimiter pairs a token bucket with the last access time so idle
// limiters can be evicted from the map.
type clientLimiter struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// PerClientRateLimiter maintains a separate token bucket for each
// authenticated client identified by the client ID stored in the context.
// Limiters for clients that have been idle longer than the cleanup interval
// are periodically evicted.
type PerClientRateLimiter struct {
	mu       sync.RWMutex
	limiters map[string]*clientLimiter
	rate     rate.Limit
	burst    int

	// global is a single shared bucket used when no client ID is available
	// (unauthenticated or unattributed traffic). It enforces the same
	// rate/burst rather than degrading to unlimited.
	global *rate.Limiter

	quit chan struct{}
}

// NewPerClientRateLimiter creates a limiter that gives each client a token
// bucket of rate/burst. A background goroutine evicts limiters for clients
// idle longer than cleanupInterval (set to 0 to disable periodic cleanup).
func NewPerClientRateLimiter(r rate.Limit, burst int, cleanupInterval time.Duration) *PerClientRateLimiter {
	p := &PerClientRateLimiter{
		limiters: make(map[string]*clientLimiter),
		rate:     r,
		burst:    burst,
		global:   rate.NewLimiter(r, burst),
		quit:     make(chan struct{}),
	}
	if cleanupInterval > 0 {
		go p.cleanupLoop(cleanupInterval)
	}
	return p
}

// Stop halts the background cleanup goroutine. It is safe to call multiple times.
func (p *PerClientRateLimiter) Stop() {
	select {
	case <-p.quit:
	default:
		close(p.quit)
	}
}

// Allow reports whether the identified client may proceed. An empty clientID
// falls back to a single shared global bucket that enforces the same
// rate/burst, so unattributed traffic is still bounded.
func (p *PerClientRateLimiter) Allow(clientID string) bool {
	if clientID == "" {
		return p.global.Allow()
	}
	p.mu.Lock()
	cl, exists := p.limiters[clientID]
	if !exists {
		cl = &clientLimiter{limiter: rate.NewLimiter(p.rate, p.burst)}
		p.limiters[clientID] = cl
	}
	cl.lastAccess = time.Now()
	p.mu.Unlock()
	return cl.limiter.Allow()
}

func (p *PerClientRateLimiter) cleanupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.evictIdle(interval * 2)
		case <-p.quit:
			return
		}
	}
}

func (p *PerClientRateLimiter) evictIdle(age time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for id, cl := range p.limiters {
		if now.Sub(cl.lastAccess) > age {
			delete(p.limiters, id)
		}
	}
}

// NewPerClientRateLimitInterceptors creates unary and stream interceptors that
// apply per-client rate limits by reading the client ID from the context.
// Client IDs are set by the auth interceptor running earlier in the chain.
func NewPerClientRateLimitInterceptors(perSecond, burst int, cleanupInterval time.Duration) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor, *PerClientRateLimiter) {
	if perSecond < 1 {
		perSecond = 1
	}
	if burst < 1 {
		burst = 1
	}
	limiter := NewPerClientRateLimiter(rate.Limit(perSecond), burst, cleanupInterval)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			if !limiter.Allow(extractClientID(ctx)) {
				return nil, status.Error(codes.ResourceExhausted, "per-client rate limit exceeded")
			}
			return handler(ctx, req)
		}, func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			if !limiter.Allow(extractClientID(stream.Context())) {
				return status.Error(codes.ResourceExhausted, "per-client rate limit exceeded")
			}
			return handler(srv, stream)
		}, limiter
}

// UnaryTimeoutInterceptor puts a server-owned deadline around every unary
// handler, including handlers called by clients that forgot to set one.
func UnaryTimeoutInterceptor(timeout time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if timeout <= 0 {
			return handler(ctx, req)
		}
		deadlineCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return handler(deadlineCtx, req)
	}
}

// StreamTimeoutInterceptor applies a server-owned deadline to the stream
// context while preserving all other grpc.ServerStream behavior.
func StreamTimeoutInterceptor(timeout time.Duration) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if timeout <= 0 {
			return handler(srv, stream)
		}
		ctx, cancel := context.WithTimeout(stream.Context(), timeout)
		defer cancel()
		return handler(srv, &contextServerStream{ServerStream: stream, ctx: ctx})
	}
}

type contextServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *contextServerStream) Context() context.Context {
	return s.ctx
}
