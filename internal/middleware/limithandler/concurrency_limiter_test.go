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
	m       *sync.Mutex
	max     int
	current int
}

func (c *counter) up() {
	c.m.Lock()
	defer c.m.Unlock()

	c.current = c.current + 1
	if c.current > c.max {
		c.max = c.current
	}
}

func (c *counter) down() {
	c.m.Lock()
	defer c.m.Unlock()

	c.current = c.current - 1
}

func TestLimiter(t *testing.T) {
	tests := []struct {
		name           string
		concurrency    int
		maxConcurrency int
		iterations     int
		buckets        int
		wantMax        int
	}{
		{
			name:           "single",
			concurrency:    1,
			maxConcurrency: 1,
			iterations:     1,
			buckets:        1,
			wantMax:        1,
		},
		{
			name:           "two-at-a-time",
			concurrency:    10,
			maxConcurrency: 2,
			iterations:     1,
			buckets:        1,
			wantMax:        2,
		},
		{
			name:           "two-by-two",
			concurrency:    10,
			maxConcurrency: 2,
			iterations:     4,
			buckets:        2,
			wantMax:        4,
		},
		{
			name:           "no-limit",
			concurrency:    2,
			maxConcurrency: 0,
			iterations:     2,
			buckets:        1,
			wantMax:        2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limiter := NewLimiter(nil)
			wg := sync.WaitGroup{}
			wg.Add(tt.concurrency)

			gauge := &counter{m: &sync.Mutex{}}
			for c := 0; c < tt.concurrency; c++ {
				go func() {

					for i := 0; i < tt.iterations; i++ {
						lockKey := fmt.Sprintf("key:%v", i%tt.buckets)

						limiter.Limit(context.Background(), lockKey, tt.maxConcurrency, func() (interface{}, error) {
							gauge.up()

							assert.True(t, len(limiter.v) <= tt.maxConcurrency)
							assert.True(t, len(limiter.v) <= tt.buckets)
							time.Sleep(1 * time.Millisecond)
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
