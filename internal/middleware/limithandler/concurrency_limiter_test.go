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
	max     int
	current int
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

func TestLimiter(t *testing.T) {
	tests := []struct {
		name           string
		concurrency    int
		maxConcurrency int
		iterations     int
		delay          time.Duration
		buckets        int
		wantMax        int
	}{
		{
			name:           "single",
			concurrency:    1,
			maxConcurrency: 1,
			iterations:     1,
			delay:          1 * time.Millisecond,
			buckets:        1,
			wantMax:        1,
		},
		{
			name:           "two-at-a-time",
			concurrency:    100,
			maxConcurrency: 2,
			iterations:     10,
			delay:          1 * time.Millisecond,
			buckets:        1,
			wantMax:        2,
		},
		{
			name:           "two-by-two",
			concurrency:    100,
			maxConcurrency: 2,
			delay:          1 * time.Nanosecond,
			iterations:     4,
			buckets:        2,
			wantMax:        4,
		},
		{
			name:           "no-limit",
			concurrency:    10,
			maxConcurrency: 0,
			iterations:     200,
			delay:          1 * time.Nanosecond,
			buckets:        1,
			wantMax:        10,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewLimiter(nil)
			wg := sync.WaitGroup{}
			wg.Add(tt.concurrency)

			gauge := &counter{}
			for c := 0; c < tt.concurrency; c++ {
				go func() {

					for i := 0; i < tt.iterations; i++ {
						lockKey := fmt.Sprintf("key:%v", i%tt.buckets)

						limiter.Limit(context.Background(), lockKey, tt.maxConcurrency, func() (interface{}, error) {
							gauge.up()

							assert.True(t, len(limiter.v) <= tt.maxConcurrency)
							assert.True(t, len(limiter.v) <= tt.buckets)
							time.Sleep(tt.delay)

							gauge.down()
							return nil, nil
						})
					}

					wg.Done()
				}()
			}

			wg.Wait()
			assert.Equal(t, tt.wantMax, gauge.max)
			assert.Equal(t, 0, gauge.current)
			assert.Equal(t, 0, len(limiter.v))
		})
	}
}
