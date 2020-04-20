package limithandler

import (
	"context"
	"fmt"
	"sync"
	"time"
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
	semaphores map[string]*semaphoreReference
	max        int64
	mux        *sync.Mutex
	monitor    ConcurrencyMonitor
}

type semaphoreReference struct {
	tokens chan struct{}
	count  int
}

// Lazy create a semaphore for the given key
func (c *ConcurrencyLimiter) getSemaphore(lockKey string) *semaphoreReference {
	c.mux.Lock()
	defer c.mux.Unlock()

	if ref := c.semaphores[lockKey]; ref != nil {
		ref.count++
		return ref
	}

	ref := &semaphoreReference{
		tokens: make(chan struct{}, c.max),
		count:  1, // The caller gets this reference so the initial value is 1
	}
	c.semaphores[lockKey] = ref
	return ref
}

func (c *ConcurrencyLimiter) putSemaphore(lockKey string) {
	c.mux.Lock()
	defer c.mux.Unlock()

	ref := c.semaphores[lockKey]
	if ref == nil {
		panic("semaphore should be in the map")
	}

	if ref.count <= 0 {
		panic(fmt.Sprintf("bad semaphore ref count %d", ref.count))
	}

	ref.count--
	if ref.count == 0 {
		delete(c.semaphores, lockKey)
	}
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

	sem := c.getSemaphore(lockKey)
	defer c.putSemaphore(lockKey)

	var err error
	select {
	case sem.tokens <- struct{}{}:
	case <-ctx.Done():
		err = ctx.Err()
	}

	c.monitor.Dequeued(ctx)
	if err != nil {
		return nil, err
	}

	defer func() { <-sem.tokens }()

	c.monitor.Enter(ctx, time.Since(start))
	defer c.monitor.Exit(ctx)

	return f()
}

// NewLimiter creates a new rate limiter
func NewLimiter(max int, monitor ConcurrencyMonitor) *ConcurrencyLimiter {
	if monitor == nil {
		monitor = &nullConcurrencyMonitor{}
	}

	return &ConcurrencyLimiter{
		semaphores: make(map[string]*semaphoreReference),
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
