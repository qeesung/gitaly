package limithandler

import (
	"sync"

	"golang.org/x/net/context"
	"golang.org/x/sync/semaphore"
)

// LimitedFunc represents a function that will be limited
type LimitedFunc func() (resp interface{}, err error)

type weightedWithSize struct {
	w *semaphore.Weighted
	n int64
}

// ConcurrencyLimiter contains rate limiter state
type ConcurrencyLimiter struct {
	v   map[string]*weightedWithSize
	mux sync.Mutex
}

// Lazy create a semaphore for the given key
func (c *ConcurrencyLimiter) getSemaphore(lockKey string, max int64) *semaphore.Weighted {
	c.mux.Lock()
	defer c.mux.Unlock()

	ws := c.v[lockKey]
	if ws != nil {
		return ws.w
	}

	w := semaphore.NewWeighted(max)
	c.v[lockKey] = &weightedWithSize{w: w, n: max}
	return w
}

func (c *ConcurrencyLimiter) attemptCollection(lockKey string) {
	c.mux.Lock()
	defer c.mux.Unlock()

	ws := c.v[lockKey]
	if ws == nil {
		return
	}

	max := ws.n
	w := ws.w

	if !w.TryAcquire(max) {
		return
	}

	// If we managed to acquire all the locks, we can remove the semaphore for this key
	delete(c.v, lockKey)
}

// Limit will limit the concurrency of f
func (c *ConcurrencyLimiter) Limit(ctx context.Context, lockKey string, maxConcurrency int64, f LimitedFunc) (interface{}, error) {
	w := c.getSemaphore(lockKey, maxConcurrency)

	err := w.Acquire(ctx, 1)

	if err != nil {
		return nil, err
	}

	defer w.Release(1)

	// Attempt to cleanup the semaphore it's no longer being used
	c.attemptCollection(lockKey)

	resp, err := f()

	return resp, err
}

// NewLimiter creates a new rate limiter
func NewLimiter() ConcurrencyLimiter {
	return ConcurrencyLimiter{v: make(map[string]*weightedWithSize)}
}
