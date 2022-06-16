package limithandler

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/status"
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

func (c *counter) Queued(ctx context.Context) {
	c.Lock()
	defer c.Unlock()
	c.queued++
}

func (c *counter) Dequeued(ctx context.Context) {
	c.Lock()
	defer c.Unlock()
	c.dequeued++
}

func (c *counter) Enter(ctx context.Context, acquireTime time.Duration) {
	c.Lock()
	defer c.Unlock()
	c.enter++
}

func (c *counter) Exit(ctx context.Context) {
	c.Lock()
	defer c.Unlock()
	c.exit++
}

func (c *counter) Dropped(ctx context.Context, reason string) {
	switch reason {
	case "max_time":
		c.droppedTime++
	case "max_size":
		c.droppedSize++
	}
}

func TestLimiter(t *testing.T) {
	t.Parallel()

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
			ctx := testhelper.Context(t)

			expectedGaugeMax := tt.maxConcurrency * tt.buckets
			if tt.maxConcurrency <= 0 {
				expectedGaugeMax = tt.concurrency
			}

			gauge := &counter{}

			limiter := NewConcurrencyLimiter(
				tt.maxConcurrency,
				0,
				nil,
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

type blockingQueueCounter struct {
	counter

	queuedCh chan struct{}
}

// Queued will block on a channel. We need a way to synchronize on when a Limiter has attempted to acquire
// a semaphore but has not yet. The caller can use the channel to wait for many requests to be queued
func (b *blockingQueueCounter) Queued(_ context.Context) {
	b.queuedCh <- struct{}{}
}

func TestConcurrencyLimiter_queueLimit(t *testing.T) {
	queueLimit := 10
	ctx := testhelper.Context(t)

	monitorCh := make(chan struct{})
	monitor := &blockingQueueCounter{queuedCh: monitorCh}
	ch := make(chan struct{})
	limiter := NewConcurrencyLimiter(1, queueLimit, nil, monitor)

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

	s, ok := status.FromError(err)
	require.True(t, ok)
	details := s.Details()
	require.Len(t, details, 1)

	limitErr, ok := details[0].(*gitalypb.LimitError)
	require.True(t, ok)

	assert.Equal(t, ErrMaxQueueSize.Error(), limitErr.ErrorMessage)
	assert.Equal(t, durationpb.New(0), limitErr.RetryAfter)
	assert.Equal(t, monitor.droppedSize, 1)

	close(ch)
	wg.Wait()
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
	ctx := testhelper.Context(t)

	ticker := helper.NewManualTicker()

	dequeuedCh := make(chan struct{})
	monitor := &blockingDequeueCounter{dequeuedCh: dequeuedCh}

	limiter := NewConcurrencyLimiter(
		1,
		0,
		func() helper.Ticker {
			return ticker
		},
		monitor,
	)

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

	ticker.Tick()

	errChan := make(chan error)
	go func() {
		_, err := limiter.Limit(ctx, "key", func() (interface{}, error) {
			return nil, nil
		})
		errChan <- err
	}()

	<-dequeuedCh
	err := <-errChan

	s, ok := status.FromError(err)
	require.True(t, ok)
	details := s.Details()
	require.Len(t, details, 1)

	limitErr, ok := details[0].(*gitalypb.LimitError)
	require.True(t, ok)

	assert.Equal(t, ErrMaxQueueTime.Error(), limitErr.ErrorMessage)
	assert.Equal(t, durationpb.New(0), limitErr.RetryAfter)

	assert.Equal(t, monitor.droppedTime, 1)
	close(ch)
	wg.Wait()
}
