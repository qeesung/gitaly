package limithandler

import (
	"fmt"
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
		wantMax          int
		wantMonitorCalls int
	}{
		{
			name:             "single",
			concurrency:      1,
			maxConcurrency:   1,
			iterations:       1,
			delay:            1 * time.Millisecond,
			buckets:          1,
			wantMax:          1,
			wantMonitorCalls: 1,
		},
		{
			name:             "two-at-a-time",
			concurrency:      100,
			maxConcurrency:   2,
			iterations:       10,
			delay:            1 * time.Millisecond,
			buckets:          1,
			wantMax:          2,
			wantMonitorCalls: 100 * 10,
		},
		{
			name:             "two-by-two",
			concurrency:      100,
			maxConcurrency:   2,
			delay:            1000 * time.Nanosecond,
			iterations:       4,
			buckets:          2,
			wantMax:          4,
			wantMonitorCalls: 100 * 4,
		},
		{
			name:             "no-limit",
			concurrency:      10,
			maxConcurrency:   0,
			iterations:       200,
			delay:            1000 * time.Nanosecond,
			buckets:          1,
			wantMax:          10,
			wantMonitorCalls: 0,
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
			wantMaxUpperBound := tt.wantMax + 2

			for c := 0; c < tt.concurrency; c++ {
				go func() {

					for i := 0; i < tt.iterations; i++ {
						lockKey := fmt.Sprintf("key:%v", i%tt.buckets)

						limiter.Limit(context.Background(), lockKey, func() (interface{}, error) {
							gauge.up()

							assert.True(t, gauge.current <= wantMaxUpperBound)
							assert.True(t, len(limiter.semaphores) <= tt.buckets)
							time.Sleep(tt.delay)

							gauge.down()
							return nil, nil
						})

						// Add
						time.Sleep(tt.delay)
					}

					wg.Done()
				}()
			}

			wg.Wait()
			assert.True(t, tt.wantMax <= gauge.max && gauge.max <= wantMaxUpperBound)
			assert.Equal(t, 0, gauge.current)
			assert.Equal(t, 0, len(limiter.semaphores))

			assert.Equal(t, tt.wantMonitorCalls, gauge.enter)
			assert.Equal(t, tt.wantMonitorCalls, gauge.exit)
			assert.Equal(t, tt.wantMonitorCalls, gauge.queued)
			assert.Equal(t, tt.wantMonitorCalls, gauge.dequeued)
		})
	}
}
