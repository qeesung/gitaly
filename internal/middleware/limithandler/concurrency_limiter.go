package limithandler

import (
	"sync"
	"time"

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

// ConcurrencyMonitor allows the concurrency monitor to be observed
type ConcurrencyMonitor interface {
	Queued(ctx context.Context)
	Dequeued(ctx context.Context)
	Enter(ctx context.Context, acquireTime time.Duration)
	Exit(ctx context.Context)
}

// ConcurrencyLimiter contains rate limiter state
type ConcurrencyLimiter struct {
	v       map[interface{}]*weightedWithSize
	mux     *sync.Mutex
	monitor ConcurrencyMonitor
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

	start := time.Now()
	c.monitor.Queued(ctx)

	w := c.getSemaphore(lockKey, int64(maxConcurrency))

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
func NewLimiter(monitor ConcurrencyMonitor) ConcurrencyLimiter {
	if monitor == nil {
		monitor = &nullConcurrencyMonitor{}
	}

	return ConcurrencyLimiter{
		v:       make(map[interface{}]*weightedWithSize),
		mux:     &sync.Mutex{},
		monitor: monitor,
	}
}

type nullConcurrencyMonitor struct{}

func (c *nullConcurrencyMonitor) Queued(ctx context.Context)                           {}
func (c *nullConcurrencyMonitor) Dequeued(ctx context.Context)                         {}
func (c *nullConcurrencyMonitor) Enter(ctx context.Context, acquireTime time.Duration) {}
func (c *nullConcurrencyMonitor) Exit(ctx context.Context)                             {}
