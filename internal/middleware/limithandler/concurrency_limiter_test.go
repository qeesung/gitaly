package limithandler

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"golang.org/x/net/context"
)

type counter struct {
	sync.Mutex
	max      int
	current  int
	queued   int
	dequeued int
	enter    int
	exit     int
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

func TestLimiter(t *testing.T) {
	tests := []struct {
		name             string
		concurrency      int
		maxConcurrency   int
		iterations       int
		delay            time.Duration
		buckets          int
		wantMaxRange     []int
		wantMonitorCalls bool
	}{
		{
			name:             "single",
			concurrency:      1,
			maxConcurrency:   1,
			iterations:       1,
			delay:            1 * time.Millisecond,
			buckets:          1,
			wantMaxRange:     []int{1, 1},
			wantMonitorCalls: true,
		},
		{
			name:             "two-at-a-time",
			concurrency:      100,
			maxConcurrency:   2,
			iterations:       10,
			delay:            1 * time.Millisecond,
			buckets:          1,
			wantMaxRange:     []int{2, 3},
			wantMonitorCalls: true,
		},
		{
			name:             "two-by-two",
			concurrency:      100,
			maxConcurrency:   2,
			delay:            1000 * time.Nanosecond,
			iterations:       4,
			buckets:          2,
			wantMaxRange:     []int{4, 5},
			wantMonitorCalls: true,
		},
		{
			name:             "no-limit",
			concurrency:      10,
			maxConcurrency:   0,
			iterations:       200,
			delay:            1000 * time.Nanosecond,
			buckets:          1,
			wantMaxRange:     []int{10, 10},
			wantMonitorCalls: false,
		},
		{
			name:           "wide-spread",
			concurrency:    1000,
			maxConcurrency: 2,
			delay:          100 * time.Nanosecond,
			iterations:     40,
			buckets:        50,
			// Intentionally leaving the max low because CI runners
			// may struggle to do 80 things in parallel
			wantMaxRange:     []int{80, 102},
			wantMonitorCalls: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gauge := &counter{}

			limiter := NewLimiter(tt.maxConcurrency, gauge)
			wg := sync.WaitGroup{}
			wg.Add(tt.concurrency)

			// We know of an edge case that can lead to the rate limiter
			// occassionally letting one or two extra goroutines run
			// concurrently.
			for c := 0; c < tt.concurrency; c++ {
				go func(counter int) {
					for i := 0; i < tt.iterations; i++ {
						lockKey := strconv.Itoa((i ^ counter) % tt.buckets)

						limiter.Limit(context.Background(), lockKey, func() (interface{}, error) {
							gauge.up()

							assert.True(t, gauge.current <= tt.wantMaxRange[1], "Expected the number of concurrent operations (%v) to not exceed the maximum concurrency (%v)", gauge.current, tt.wantMaxRange[1])
							assert.True(t, limiter.countSemaphores() <= tt.buckets, "Expected the number of semaphores (%v) to be lte number of buckets (%v)", limiter.countSemaphores(), tt.buckets)
							time.Sleep(tt.delay)

							gauge.down()
							return nil, nil
						})

						time.Sleep(tt.delay)
					}

					wg.Done()
				}(c)
			}

			wg.Wait()
			assert.True(t, tt.wantMaxRange[0] <= gauge.max && gauge.max <= tt.wantMaxRange[1], "Expected maximum concurrency to be in the range [%v,%v] but got %v", tt.wantMaxRange[0], tt.wantMaxRange[1], gauge.max)
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
