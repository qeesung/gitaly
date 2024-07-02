package limiter

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/durationpb"
)

type counter struct {
	sync.Mutex
	max         int
	current     int
	queued      int
	dequeued    int
	enter       int
	exit        int
	droppedSize int
	droppedTime int
}

func (c *counter) up() {
	c.Lock()
	defer c.Unlock()

	c.current = c.current + 1
	if c.current > c.max {
		c.max = c.current
	}
}

func (c *counter) down() {
	c.Lock()
	defer c.Unlock()

	c.current = c.current - 1
}

func (c *counter) currentVal() int {
	c.Lock()
	defer c.Unlock()
	return c.current
}

func (c *counter) Queued(context.Context, string, int) {
	c.Lock()
	defer c.Unlock()
	c.queued++
}

func (c *counter) Dequeued(context.Context) {
	c.Lock()
	defer c.Unlock()
	c.dequeued++
}

func (c *counter) Enter(context.Context, int, time.Duration) {
	c.Lock()
	defer c.Unlock()
	c.enter++
}

func (c *counter) Exit(context.Context) {
	c.Lock()
	defer c.Unlock()
	c.exit++
}

func (c *counter) Dropped(_ context.Context, _ string, _ int, _ int, _ time.Duration, reason string) {
	c.Lock()
	defer c.Unlock()
	switch reason {
	case "max_time":
		c.droppedTime++
	case "max_size":
		c.droppedSize++
	}
}

func TestLimiter_static(t *testing.T) {
	t.Parallel()

	// Limiter works with static limits regardless of semaphore implementation
	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, testLimiterStatic)
}

func testLimiterStatic(t *testing.T, ctx context.Context) {
	tests := []struct {
		name             string
		concurrency      int
		maxConcurrency   int
		iterations       int
		buckets          int
		wantMonitorCalls bool
	}{
		{
			name:             "single",
			concurrency:      1,
			maxConcurrency:   1,
			iterations:       1,
			buckets:          1,
			wantMonitorCalls: true,
		},
		{
			name:             "two-at-a-time",
			concurrency:      100,
			maxConcurrency:   2,
			iterations:       10,
			buckets:          1,
			wantMonitorCalls: true,
		},
		{
			name:             "two-by-two",
			concurrency:      100,
			maxConcurrency:   2,
			iterations:       4,
			buckets:          2,
			wantMonitorCalls: true,
		},
		{
			name:             "no-limit",
			concurrency:      10,
			maxConcurrency:   0,
			iterations:       200,
			buckets:          1,
			wantMonitorCalls: false,
		},
		{
			name:           "wide-spread",
			concurrency:    1000,
			maxConcurrency: 2,
			// We use a long delay here to prevent flakiness in CI. If the delay is
			// too short, the first goroutines to enter the critical section will be
			// gone before we hit the intended maximum concurrency.
			iterations:       40,
			buckets:          50,
			wantMonitorCalls: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expectedGaugeMax := tt.maxConcurrency * tt.buckets
			if tt.maxConcurrency <= 0 {
				expectedGaugeMax = tt.concurrency
			}

			gauge := &counter{}

			limiter := NewConcurrencyLimiter(
				NewAdaptiveLimit("staticLimit", AdaptiveSetting{Initial: tt.maxConcurrency}),
				0,
				0,
				gauge,
			)
			wg := sync.WaitGroup{}
			wg.Add(tt.concurrency)

			full := sync.NewCond(&sync.Mutex{})

			// primePump waits for the gauge to reach the minimum
			// expected max concurrency so that the limiter is
			// "warmed" up before proceeding with the test
			primePump := func() {
				full.L.Lock()
				defer full.L.Unlock()

				gauge.up()

				if gauge.max >= expectedGaugeMax {
					full.Broadcast()
					return
				}

				full.Wait() // wait until full is broadcast
			}

			// We know of an edge case that can lead to the rate limiter
			// occasionally letting one or two extra goroutines run
			// concurrently.
			for c := 0; c < tt.concurrency; c++ {
				go func(counter int) {
					for i := 0; i < tt.iterations; i++ {
						lockKey := strconv.Itoa((i ^ counter) % tt.buckets)

						_, err := limiter.Limit(ctx, lockKey, func() (interface{}, error) {
							primePump()

							current := gauge.currentVal()
							require.True(t, current <= expectedGaugeMax, "Expected the number of concurrent operations (%v) to not exceed the maximum concurrency (%v)", current, expectedGaugeMax)

							require.True(t, limiter.countSemaphores() <= tt.buckets, "Expected the number of semaphores (%v) to be lte number of buckets (%v)", limiter.countSemaphores(), tt.buckets)

							gauge.down()
							return nil, nil
						})
						require.NoError(t, err)
					}

					wg.Done()
				}(c)
			}

			wg.Wait()

			assert.Equal(t, expectedGaugeMax, gauge.max, "Expected maximum concurrency")
			assert.Equal(t, 0, gauge.current)
			assert.Equal(t, 0, limiter.countSemaphores())

			var wantMonitorCallCount int
			if tt.wantMonitorCalls {
				wantMonitorCallCount = tt.concurrency * tt.iterations
			} else {
				wantMonitorCallCount = 0
			}

			assert.Equal(t, wantMonitorCallCount, gauge.enter)
			assert.Equal(t, wantMonitorCallCount, gauge.exit)
			assert.Equal(t, wantMonitorCallCount, gauge.queued)
			assert.Equal(t, wantMonitorCallCount, gauge.dequeued)
		})
	}
}

func TestLimiter_dynamic(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, func(t *testing.T, ctx context.Context) {
		if featureflag.UseResizableSemaphoreInConcurrencyLimiter.IsEnabled(ctx) {
			testLimiterDynamic(t, ctx)
		} else {
			t.Skip("limiter.staticSemaphore does not support dynamic limiting")
		}
	})
}

func testLimiterDynamic(t *testing.T, ctx context.Context) {
	t.Run("increase dynamic limit when there is no queuing request", func(t *testing.T) {
		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 5, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}
		limiter := NewConcurrencyLimiter(limit, 10, 0, gauge)

		// 5 requests acquired the tokens, the limiter is full now
		release1, waitAfterRelease1 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 5, gauge.enter)

		// Update the limit to 7
		limit.Update(7)

		// 2 more requests acquired the token. This proves the limit is expanded
		release2, waitAfterRelease2 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 2)
		require.Equal(t, 7, gauge.enter)

		close(release1)
		close(release2)
		waitAfterRelease1()
		waitAfterRelease2()
		require.Equal(t, 7, gauge.exit)
	})

	t.Run("decrease dynamic limit when there is no queuing request", func(t *testing.T) {
		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 5, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}
		limiter := NewConcurrencyLimiter(limit, 10, 0, gauge)

		// 3 requests acquired the tokens, 2 slots left
		release1, waitAfterRelease1 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 3)
		require.Equal(t, 3, gauge.enter)
		require.Equal(t, 3, gauge.queued)

		// Update the limit to 3
		limit.Update(3)

		// 2 requests are put in queue, meaning the limit shrinks down
		waitAcquired2, release2, waitAfterRelease2 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 2)
		require.Equal(t, 3, gauge.enter)
		require.Equal(t, 5, gauge.queued)

		// Release first 3 requests
		close(release1)
		waitAfterRelease1()

		// Now the last 2 requests can acquire token
		waitAcquired2()
		require.Equal(t, 5, gauge.enter)
		require.Equal(t, 5, gauge.queued)
		require.Equal(t, 3, gauge.exit)

		// Release the last patch
		close(release2)
		waitAfterRelease2()
		require.Equal(t, 5, gauge.exit)
	})

	t.Run("increase dynamic limit more than the number of queuing requests", func(t *testing.T) {
		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 5, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}
		limiter := NewConcurrencyLimiter(limit, 10, 0, gauge)

		// 5 requests acquired the tokens, the limiter is full now
		release1, waitAfterRelease1 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 5, gauge.enter)

		// 2 requests waiting in the queue
		waitAcquired2, release2, waitAfterRelease2 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 2)
		require.Equal(t, 5, gauge.enter)
		require.Equal(t, 7, gauge.queued)

		// Update the limit to 7
		limit.Update(7)

		// Wait for the other 2 requests acquired the token. This proves the limiter is expanded
		waitAcquired2()
		require.Equal(t, 7, gauge.enter)

		close(release1)
		close(release2)
		waitAfterRelease1()
		waitAfterRelease2()
		require.Equal(t, 7, gauge.exit)
	})

	t.Run("increase dynamic limit less than the number of queuing requests", func(t *testing.T) {
		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 5, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}
		limiter := NewConcurrencyLimiter(limit, 10, 0, gauge)

		// 5 requests acquired the tokens, the limiter is full now
		release1, waitAfterRelease1 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 5, gauge.enter)

		// 2 requests waiting in the queue
		waitAcquired2, release2, waitAfterRelease2 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 2)
		require.Equal(t, 5, gauge.enter)
		require.Equal(t, 7, gauge.queued)

		// 5 more requests waiting in the queue
		waitAcquired3, release3, waitAfterRelease3 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 5, gauge.enter)
		require.Equal(t, 12, gauge.queued)

		// Update the limit to 7.
		limit.Update(7)

		//  Release first 5 requests, all requests should fit in the queue now.
		close(release1)
		waitAfterRelease1()
		require.Equal(t, 5, gauge.exit)

		waitAcquired2()
		waitAcquired3()
		require.Equal(t, 12, gauge.enter)

		// Now release all requests
		close(release2)
		close(release3)
		waitAfterRelease2()
		waitAfterRelease3()
		require.Equal(t, 12, gauge.exit)
	})

	t.Run("decrease dynamic limit less than the number of concurrent requests", func(t *testing.T) {
		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 5, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}
		limiter := NewConcurrencyLimiter(limit, 10, 0, gauge)

		// 5 requests acquired the tokens, the limiter is full now
		release1, waitAfterRelease1 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 5, gauge.enter)

		// Update the limit to 3
		limit.Update(3)

		// 3 requests are put in queue
		waitAcquired2, release2, waitAfterRelease2 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 3)
		require.Equal(t, 5, gauge.enter)
		require.Equal(t, 8, gauge.queued)

		// Release the first 5 requests
		close(release1)
		waitAfterRelease1()
		require.Equal(t, 5, gauge.exit)

		// Now the last 3 requests acquire the tokens
		waitAcquired2()
		require.Equal(t, 8, gauge.enter)

		// 1 more request is put in queue, meaning the limit has correctly shrunk down to 3.
		waitAcquired3, release3, waitAfterRelease3 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 1)
		require.Equal(t, 8, gauge.enter)
		require.Equal(t, 9, gauge.queued)

		// Release the second 3 requests
		close(release2)
		waitAfterRelease2()
		require.Equal(t, 8, gauge.exit)

		// The last request acquires the token
		waitAcquired3()
		require.Equal(t, 9, gauge.enter)

		// Release the last request
		close(release3)
		waitAfterRelease3()
		require.Equal(t, 9, gauge.exit)
	})

	t.Run("increase and decrease dynamic limit multiple times", func(t *testing.T) {
		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 5, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}
		limiter := NewConcurrencyLimiter(limit, 10, 0, gauge)

		// Update the limit to 7
		limit.Update(7)

		// 5 requests acquired the tokens
		release1, waitAfterRelease1 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 5, gauge.enter)

		// Update the limit to 3
		limit.Update(3)

		// 3 requests are put in queue
		waitAcquired2, release2, waitAfterRelease2 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 3)
		require.Equal(t, 5, gauge.enter)
		require.Equal(t, 8, gauge.queued)

		// Update the limit to 10
		limit.Update(10)

		// All existing requests acquire the tokens
		waitAcquired2()
		require.Equal(t, 8, gauge.enter)

		// 2 more requests
		release3, waitAfterRelease3 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 2)
		require.Equal(t, 10, gauge.enter)

		// Update the limit to 1
		limit.Update(1)

		// Now release all of them
		close(release1)
		waitAfterRelease1()
		require.Equal(t, 5, gauge.exit)

		close(release2)
		waitAfterRelease2()
		require.Equal(t, 8, gauge.exit)

		close(release3)
		waitAfterRelease3()
		require.Equal(t, 10, gauge.exit)
	})

	t.Run("increase the limit when the queue is full", func(t *testing.T) {
		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 1, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}
		// Mind the queue length here
		limiter := NewConcurrencyLimiter(limit, 5, 0, gauge)

		// 1 requests acquired the tokens, the limiter is full now
		release1, waitAfterRelease1 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 1)
		require.Equal(t, 1, gauge.enter)
		require.Equal(t, 1, gauge.queued)

		// 5 requests queuing for the tokens, the queue is full now
		waitAcquired2, release2, waitAfterRelease2 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 1, gauge.enter)
		require.Equal(t, 6, gauge.queued)

		// Limiter rejects new request
		ensureOperationsRejectedWhenQueueFull(t, ctx, "1", limiter)

		// Update the limit
		limit.Update(6)
		waitAcquired2()
		require.Equal(t, 6, gauge.enter)

		// 5 requests queuing for the tokens, the queue is full now
		waitAcquired3, release3, waitAfterRelease3 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 6, gauge.enter)
		require.Equal(t, 11, gauge.queued)

		// Limiter rejects new request
		ensureOperationsRejectedWhenQueueFull(t, ctx, "1", limiter)

		// Clean up
		close(release1)
		close(release2)
		waitAfterRelease1()
		waitAfterRelease2()
		require.Equal(t, 6, gauge.exit)

		waitAcquired3()
		require.Equal(t, 11, gauge.enter)
		close(release3)
		waitAfterRelease3()
	})

	t.Run("decrease the limit when the queue is full", func(t *testing.T) {
		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 5, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}
		// Mind the queue length here
		limiter := NewConcurrencyLimiter(limit, 3, 0, gauge)

		// 5 requests acquired the tokens, the limiter is full now
		release1, waitAfterRelease1 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 5, gauge.enter)
		require.Equal(t, 5, gauge.queued)

		// 3 requests queuing for the tokens, the queue is full now
		waitAcquired2, release2, waitAfterRelease2 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 3)
		require.Equal(t, 5, gauge.enter)
		require.Equal(t, 8, gauge.queued)

		// Limiter rejects new request
		ensureOperationsRejectedWhenQueueFull(t, ctx, "1", limiter)

		// Update the limit.
		limit.Update(3)

		// The queue is still full
		ensureOperationsRejectedWhenQueueFull(t, ctx, "1", limiter)

		// Release first 5 requests and let the last 3 requests in
		close(release1)
		waitAfterRelease1()
		require.Equal(t, 5, gauge.exit)
		waitAcquired2()
		require.Equal(t, 8, gauge.enter)

		// Another 3 requests in queue. The queue is still full, meaning the concurrency is 3 and the queue is still 3.
		waitAcquired3, release3, waitAfterRelease3 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 3)
		require.Equal(t, 8, gauge.enter)
		require.Equal(t, 11, gauge.queued)
		ensureOperationsRejectedWhenQueueFull(t, ctx, "1", limiter)

		// Clean up
		close(release2)
		waitAfterRelease2()
		require.Equal(t, 8, gauge.exit)
		waitAcquired3()
		require.Equal(t, 11, gauge.enter)
		close(release3)
		waitAfterRelease3()
		require.Equal(t, 11, gauge.exit)
	})

	t.Run("dynamic limit works without queuing", func(t *testing.T) {
		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 5, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}
		// No maxQueueLength, it means the limiter accepts unlimited requests to be queued
		limiter := NewConcurrencyLimiter(limit, 0, 0, gauge)

		// 5 requests acquired the tokens, the limiter is full now
		release1, waitAfterRelease1 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 5, gauge.enter)
		require.Equal(t, 5, gauge.queued)

		// 5 more requests
		waitAcquired2, release2, waitAfterRelease2 := spawnAndWaitQueued(t, ctx, "1", limiter, gauge, 5)

		// Update the limit.
		limit.Update(10)

		// All of them acquired the tokens
		waitAcquired2()
		require.Equal(t, 10, gauge.enter)

		// Clean up
		close(release1)
		close(release2)
		waitAfterRelease1()
		waitAfterRelease2()
		require.Equal(t, 10, gauge.exit)
	})

	t.Run("dynamic limit works with context timeout", func(t *testing.T) {
		ctxWithTimeout, cancel, simulateTimeout := testhelper.ContextWithSimulatedTimeout(ctx)
		defer cancel()

		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 5, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}

		limiter := NewConcurrencyLimiter(limit, 0, 1*time.Millisecond, gauge)
		limiter.SetWaitTimeoutContext = func() context.Context { return ctxWithTimeout }

		// 5 requests acquired the tokens, the limiter is full now
		release1, waitAfterRelease1 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 5, gauge.enter)
		require.Equal(t, 5, gauge.queued)

		errors := make(chan error, 10)
		// 5 requests in queue
		spawnQueuedAndCollectErrors(ctxWithTimeout, "1", limiter, gauge, 5, errors)

		// Decrease the limit
		limit.Update(3)

		// 5 more requests in queue
		spawnQueuedAndCollectErrors(ctxWithTimeout, "1", limiter, gauge, 5, errors)

		// Trigger timeout event
		simulateTimeout()
		for i := 0; i < 10; i++ {
			require.EqualError(t, <-errors, "maximum time in concurrency queue reached")
		}

		// Other goroutines exit as normal
		close(release1)
		waitAfterRelease1()
		require.Equal(t, 5, gauge.exit)
	})

	t.Run("dynamic limit works with context cancellation", func(t *testing.T) {
		ctx2, cancel := context.WithCancel(ctx)

		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 5, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}

		limiter := NewConcurrencyLimiter(limit, 0, 0, gauge)

		// 5 requests acquired the tokens, the limiter is full now
		release1, waitAfterRelease1 := spawnAndWaitAcquired(t, ctx, "1", limiter, gauge, 5)
		require.Equal(t, 5, gauge.enter)
		require.Equal(t, 5, gauge.queued)

		errors := make(chan error, 10)
		// 5 requests in queue
		spawnQueuedAndCollectErrors(ctx2, "1", limiter, gauge, 5, errors)

		// Decrease the limit
		limit.Update(3)

		// 5 more requests in queue
		spawnQueuedAndCollectErrors(ctx2, "1", limiter, gauge, 5, errors)

		// Trigger context cancellation
		cancel()
		for i := 0; i < 10; i++ {
			require.EqualError(t, <-errors, "unexpected error when dequeueing request: context canceled")
		}

		// Other goroutines exit as normal
		close(release1)
		waitAfterRelease1()
		require.Equal(t, 5, gauge.exit)
	})

	t.Run("dynamic limit works with multiple buckets", func(t *testing.T) {
		limit := NewAdaptiveLimit("dynamicLimit", AdaptiveSetting{Initial: 5, Max: 10, Min: 1})
		gauge := &blockingQueueCounter{queuedCh: make(chan struct{})}

		limiter := NewConcurrencyLimiter(limit, 5, 0, gauge)

		var releaseChans []chan struct{}
		var waitAcquireFuncs, waitReleaseFuncs []func()

		// 5 * 5 requests acquired tokens
		for i := 1; i <= 5; i++ {
			release, waitAfterRelease := spawnAndWaitAcquired(t, ctx, fmt.Sprintf("%d", i), limiter, gauge, 5)
			releaseChans = append(releaseChans, release)
			waitReleaseFuncs = append(waitReleaseFuncs, waitAfterRelease)
		}
		require.Equal(t, 25, gauge.enter)

		// 1 + 2 + 3 + 4 + 5 requests are in queue
		for i := 1; i <= 5; i++ {
			waitAcquired, release, waitAfterRelease := spawnAndWaitQueued(t, ctx, fmt.Sprintf("%d", i), limiter, gauge, i)
			waitAcquireFuncs = append(waitAcquireFuncs, waitAcquired)
			releaseChans = append(releaseChans, release)
			waitReleaseFuncs = append(waitReleaseFuncs, waitAfterRelease)
		}
		require.Equal(t, 25, gauge.enter)
		require.Equal(t, 40, gauge.queued)

		// Update limit, enough for all requests
		limit.Update(10)

		// All requests acquired tokens now
		for _, wait := range waitAcquireFuncs {
			wait()
		}
		require.Equal(t, 40, gauge.enter)

		// Release all
		for _, release := range releaseChans {
			close(release)
		}
		for _, wait := range waitReleaseFuncs {
			wait()
		}
		require.Equal(t, 40, gauge.exit)
	})
}

// spawnAndWaitAcquired spawns N goroutines that wait for the limiter. They wait until all of them acquire the limiter
// token before exiting. This function returns a channel to control token release and a function to wait until all
// goroutines finish.
func spawnAndWaitAcquired(t *testing.T, ctx context.Context, bucket string, limiter *ConcurrencyLimiter, gauge *blockingQueueCounter, n int) (chan struct{}, func()) {
	var acquireWg, releaseWg sync.WaitGroup
	release := make(chan struct{})

	for i := 0; i < n; i++ {
		acquireWg.Add(1)
		releaseWg.Add(1)
		go func() {
			defer releaseWg.Done()
			_, err := limiter.Limit(ctx, bucket, func() (resp interface{}, err error) {
				acquireWg.Done()
				<-release
				return nil, nil
			})
			require.NoError(t, err)
		}()
	}
	for i := 0; i < n; i++ {
		<-gauge.queuedCh
		gauge.queued++
	}
	acquireWg.Wait()

	return release, releaseWg.Wait
}

// spawnAndWaitQueued spawns N goroutines that wait for the limiter. They wait until all of them are queued. This
// function returns a function to wait for channel to acquire the token, a channel to control token release, and a
// function to wait until all goroutines finish.
func spawnAndWaitQueued(t *testing.T, ctx context.Context, bucket string, limiter *ConcurrencyLimiter, gauge *blockingQueueCounter, n int) (func(), chan struct{}, func()) {
	var acquireWg, releaseWg sync.WaitGroup
	release := make(chan struct{})

	for i := 0; i < n; i++ {
		acquireWg.Add(1)
		releaseWg.Add(1)
		go func() {
			defer releaseWg.Done()
			_, err := limiter.Limit(ctx, bucket, func() (resp interface{}, err error) {
				acquireWg.Done()
				<-release
				return nil, nil
			})
			require.NoError(t, err)
		}()
	}
	for i := 0; i < n; i++ {
		<-gauge.queuedCh
		gauge.queued++
	}

	return acquireWg.Wait, release, releaseWg.Wait
}

// spawnQueueAndCollectErrors spawns N goroutines that should error when attempting to wait for the limiter. Errors are
// collected into the input errors channel. The function returned after N goroutines are already waiting the queue.
func spawnQueuedAndCollectErrors(ctx context.Context, bucket string, limiter *ConcurrencyLimiter, gauge *blockingQueueCounter, n int, errors chan error) {
	for i := 0; i < n; i++ {
		go func() {
			_, err := limiter.Limit(ctx, bucket, func() (interface{}, error) {
				return nil, fmt.Errorf("should not call")
			})
			errors <- err
		}()
	}
	for i := 0; i < n; i++ {
		<-gauge.queuedCh
		gauge.queued++
	}
}

func ensureOperationsRejectedWhenQueueFull(t *testing.T, ctx context.Context, bucket string, limiter *ConcurrencyLimiter) {
	_, err := limiter.Limit(ctx, bucket, func() (interface{}, error) {
		return nil, fmt.Errorf("should not call")
	})
	require.EqualError(t, err, "maximum queue size reached")
}

type blockingQueueCounter struct {
	counter

	queuedCh chan struct{}
}

// Queued will block on a channel. We need a way to synchronize on when a Limiter has attempted to acquire
// a semaphore but has not yet. The caller can use the channel to wait for many requests to be queued
func (b *blockingQueueCounter) Queued(context.Context, string, int) {
	b.queuedCh <- struct{}{}
}

func TestConcurrencyLimiter_queueLimit(t *testing.T) {
	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, func(t *testing.T, ctx context.Context) {
		queueLimit := 10

		monitorCh := make(chan struct{})
		monitor := &blockingQueueCounter{queuedCh: monitorCh}
		ch := make(chan struct{})
		limiter := NewConcurrencyLimiter(NewAdaptiveLimit("staticLimit", AdaptiveSetting{Initial: 1}), queueLimit, 0, monitor)

		// occupied with one live request that takes a long time to complete
		go func() {
			_, err := limiter.Limit(ctx, "key", func() (interface{}, error) {
				ch <- struct{}{}
				<-ch
				return nil, nil
			})
			require.NoError(t, err)
		}()

		<-monitorCh
		<-ch

		var wg sync.WaitGroup
		// fill up the queue
		for i := 0; i < queueLimit; i++ {
			wg.Add(1)
			go func() {
				_, err := limiter.Limit(ctx, "key", func() (interface{}, error) {
					return nil, nil
				})
				require.NoError(t, err)
				wg.Done()
			}()
		}

		var queued int
		for range monitorCh {
			queued++
			if queued == queueLimit {
				break
			}
		}

		errChan := make(chan error, 1)
		go func() {
			_, err := limiter.Limit(ctx, "key", func() (interface{}, error) {
				return nil, nil
			})
			errChan <- err
		}()

		err := <-errChan
		assert.Error(t, err)

		var structErr structerr.Error
		require.True(t, errors.As(err, &structErr))
		details := structErr.Details()
		require.Len(t, details, 1)

		limitErr, ok := details[0].(*gitalypb.LimitError)
		require.True(t, ok)

		assert.Equal(t, ErrMaxQueueSize.Error(), limitErr.ErrorMessage)
		assert.Equal(t, durationpb.New(0), limitErr.RetryAfter)
		assert.Equal(t, monitor.droppedSize, 1)

		close(ch)
		wg.Wait()
	})
}

type blockingDequeueCounter struct {
	counter

	dequeuedCh chan struct{}
}

// Dequeued will block on a channel. We need a way to synchronize on when a Limiter has successfully
// acquired a semaphore but has not yet. The caller can use the channel to wait for many requests to
// be queued
func (b *blockingDequeueCounter) Dequeued(context.Context) {
	b.dequeuedCh <- struct{}{}
}

func TestLimitConcurrency_queueWaitTime(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, func(t *testing.T, ctx context.Context) {
		limiterCtx, cancel, simulateTimeout := testhelper.ContextWithSimulatedTimeout(ctx)
		defer cancel()

		dequeuedCh := make(chan struct{})
		monitor := &blockingDequeueCounter{dequeuedCh: dequeuedCh}

		limiter := NewConcurrencyLimiter(
			NewAdaptiveLimit("staticLimit", AdaptiveSetting{Initial: 1}),
			0,
			1*time.Millisecond,
			monitor,
		)
		limiter.SetWaitTimeoutContext = func() context.Context { return limiterCtx }

		ch := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			_, err := limiter.Limit(ctx, "key", func() (interface{}, error) {
				<-ch
				return nil, nil
			})
			require.NoError(t, err)
			wg.Done()
		}()

		<-dequeuedCh

		simulateTimeout()

		errChan := make(chan error)
		go func() {
			_, err := limiter.Limit(ctx, "key", func() (interface{}, error) {
				return nil, nil
			})
			errChan <- err
		}()

		<-dequeuedCh
		err := <-errChan

		var structErr structerr.Error
		require.True(t, errors.As(err, &structErr))
		details := structErr.Details()
		require.Len(t, details, 1)

		limitErr, ok := details[0].(*gitalypb.LimitError)
		require.True(t, ok)

		testhelper.RequireGrpcCode(t, err, codes.ResourceExhausted)
		assert.Equal(t, ErrMaxQueueTime.Error(), limitErr.ErrorMessage)
		assert.Equal(t, durationpb.New(0).AsDuration(), limitErr.RetryAfter.AsDuration())

		assert.Equal(t, monitor.droppedTime, 1)
		close(ch)
		wg.Wait()
	})
}

func TestLimitConcurrency_queueWaitTimeRealTimeout(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, func(t *testing.T, ctx context.Context) {
		limiter := NewConcurrencyLimiter(
			NewAdaptiveLimit("staticLimit", AdaptiveSetting{Initial: 1}),
			0,
			// This test setups two requests:
			// - The first one is eligible. It enters the handler and blocks the queue.
			// - The second request is blocked until timeout.
			// Both of them share this timeout. Internally, the limiter creates a context
			// deadline to reject timed-out requests. If it's set too low, there's a tiny
			// possibility that the context reaches the deadline when the limiter checks the
			// request. Thus, setting a reasonable timeout here and adding some retry
			// attempts below make the test stable.
			100*time.Millisecond,
			&counter{},
		)

		waitAcquire := make(chan struct{})
		release := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()

			// Retry a couple of time, just in case the test runs on a very slow machine that invalidates
			// the test. If the limiter still cannot permit the request after 3 times, something must go
			// wrong horribly.
			for i := 0; i < 3; i++ {
				if _, err := limiter.Limit(ctx, "key", func() (interface{}, error) {
					close(waitAcquire)
					<-release
					return nil, nil
				}); err == nil {
					return
				}
			}
			require.FailNow(t, "the first request is supposed to enter the test server's handler")
		}()

		<-waitAcquire
		_, err := limiter.Limit(ctx, "key", func() (interface{}, error) {
			return nil, nil
		})

		var structErr structerr.Error
		require.True(t, errors.As(err, &structErr))
		details := structErr.Details()
		require.Len(t, details, 1)

		limitErr, ok := details[0].(*gitalypb.LimitError)
		require.True(t, ok)

		testhelper.RequireGrpcCode(t, err, codes.ResourceExhausted)
		assert.Equal(t, ErrMaxQueueTime.Error(), limitErr.ErrorMessage)
		assert.Equal(t, durationpb.New(0).AsDuration(), limitErr.RetryAfter.AsDuration())

		close(release)
	})
}
