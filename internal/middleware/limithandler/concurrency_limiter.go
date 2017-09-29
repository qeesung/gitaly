package limithandler

import (
	"sync"
	"time"

	"golang.org/x/net/context"
	"golang.org/x/sync/semaphore"
)

// LimitedFunc represents a function that will be limited
type LimitedFunc func() (resp interface{}, err error)

// ConcurrencyMonitor allows the concurrency monitor to be observed
type ConcurrencyMonitor interface {
	Queued(ctx context.Context)
	Dequeued(ctx context.Context)
	Enter(ctx context.Context, acquireTime time.Duration)
	Exit(ctx context.Context)
}

// ConcurrencyLimiter contains rate limiter state
type ConcurrencyLimiter struct {
	// A weighted semaphore is like a mutex, but with a number of 'slots'.
	// When locking the locker requests 1 or more slots to be locked.
	// In this package, the number of slots is the number of concurrent requests the rate limiter lets through.
	// https://godoc.org/golang.org/x/sync/semaphore
	semaphores map[string]*semaphore.Weighted
	max        int64
	mux        *sync.Mutex
	monitor    ConcurrencyMonitor
}

// Lazy create a semaphore for the given key
func (c *ConcurrencyLimiter) getSemaphore(lockKey string) *semaphore.Weighted {
	c.mux.Lock()
	defer c.mux.Unlock()

	ws := c.semaphores[lockKey]
	if ws != nil {
		return ws
	}

	w := semaphore.NewWeighted(c.max)
	c.semaphores[lockKey] = w
	return w
}

func (c *ConcurrencyLimiter) attemptCollection(lockKey string) {
	c.mux.Lock()
	defer c.mux.Unlock()

	ws := c.semaphores[lockKey]
	if ws == nil {
		return
	}

	if !ws.TryAcquire(c.max) {
		return
	}

	// By releasing, we prevent a lockup of goroutines that have already
	// acquired the semaphore, but have yet to acquire on it
	ws.Release(c.max)

	// If we managed to acquire all the locks, we can remove the semaphore for this key
	delete(c.semaphores, lockKey)
}

func (c *ConcurrencyLimiter) countSemaphores() int {
	c.mux.Lock()
	defer c.mux.Unlock()

	return len(c.semaphores)
}

// Limit will limit the concurrency of f
func (c *ConcurrencyLimiter) Limit(ctx context.Context, lockKey string, f LimitedFunc) (interface{}, error) {
	if c.max <= 0 {
		return f()
	}

	start := time.Now()
	c.monitor.Queued(ctx)

	w := c.getSemaphore(lockKey)

	// Attempt to cleanup the semaphore it's no longer being used
	defer c.attemptCollection(lockKey)

	err := w.Acquire(ctx, 1)
	c.monitor.Dequeued(ctx)

	if err != nil {
		return nil, err
	}

	c.monitor.Enter(ctx, time.Since(start))
	defer c.monitor.Exit(ctx)

	defer w.Release(1)

	resp, err := f()

	return resp, err
}

// NewLimiter creates a new rate limiter
func NewLimiter(max int, monitor ConcurrencyMonitor) *ConcurrencyLimiter {
	if monitor == nil {
		monitor = &nullConcurrencyMonitor{}
	}

	return &ConcurrencyLimiter{
		semaphores: make(map[string]*semaphore.Weighted),
		max:        int64(max),
		mux:        &sync.Mutex{},
		monitor:    monitor,
	}
}

type nullConcurrencyMonitor struct{}

func (c *nullConcurrencyMonitor) Queued(ctx context.Context)                           {}
func (c *nullConcurrencyMonitor) Dequeued(ctx context.Context)                         {}
func (c *nullConcurrencyMonitor) Enter(ctx context.Context, acquireTime time.Duration) {}
func (c *nullConcurrencyMonitor) Exit(ctx context.Context)                             {}
