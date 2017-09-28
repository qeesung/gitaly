package limithandler

import (
	"sync"

	"golang.org/x/net/context"
	"golang.org/x/sync/semaphore"
)

// LimitedFunc represents a function that will be limited
type LimitedFunc func() (resp interface{}, err error)

type weightedWithSize struct {
	// A weighted semaphore is like a mutex, but with a number of 'slots'.
	// When locking the locker requests 1 or more slots to be locked.
	// In this package, the number of slots is the number of concurrent requests the rate limiter lets through.
	// https://godoc.org/golang.org/x/sync/semaphore
	w *semaphore.Weighted
	n int64
}

// ConcurrencyLimiter contains rate limiter state
type ConcurrencyLimiter struct {
	v   map[interface{}]*weightedWithSize
	mux *sync.Mutex
}

// Lazy create a semaphore for the given key
func (c *ConcurrencyLimiter) getSemaphore(lockKey interface{}, max int64) *semaphore.Weighted {
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

func (c *ConcurrencyLimiter) attemptCollection(lockKey interface{}) {
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

	// By releasing, we prevent a lockup of goroutines that have already
	// acquired the semaphore, but have yet to acquire on it
	w.Release(max)

	// If we managed to acquire all the locks, we can remove the semaphore for this key
	delete(c.v, lockKey)

}

// Limit will limit the concurrency of f
func (c *ConcurrencyLimiter) Limit(ctx context.Context, lockKey interface{}, maxConcurrency int, f LimitedFunc) (interface{}, error) {
	if maxConcurrency <= 0 {
		return f()
	}

	w := c.getSemaphore(lockKey, int64(maxConcurrency))
	// Attempt to cleanup the semaphore it's no longer being used
	defer c.attemptCollection(lockKey)

	err := w.Acquire(ctx, 1)

	if err != nil {
		return nil, err
	}

	defer w.Release(1)

	resp, err := f()

	return resp, err
}

// NewLimiter creates a new rate limiter
func NewLimiter() ConcurrencyLimiter {
	return ConcurrencyLimiter{
		v:   make(map[interface{}]*weightedWithSize),
		mux: &sync.Mutex{},
	}
}
