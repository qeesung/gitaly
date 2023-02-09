package limithandler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/tick"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestRateLimiter_pruneUnusedLimiters(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		desc                                      string
		setup                                     func(r *RateLimiter)
		expectedLimiters, expectedRemovedLimiters []string
	}{
		{
			desc: "none are prunable",
			setup: func(r *RateLimiter) {
				r.limitersByKey.Store("a", struct{}{})
				r.limitersByKey.Store("b", struct{}{})
				r.limitersByKey.Store("c", struct{}{})
				r.lastAccessedByKey.Store("a", time.Now())
				r.lastAccessedByKey.Store("b", time.Now())
				r.lastAccessedByKey.Store("c", time.Now())
			},
			expectedLimiters:        []string{"a", "b", "c"},
			expectedRemovedLimiters: []string{},
		},
		{
			desc: "all are prunable",
			setup: func(r *RateLimiter) {
				r.limitersByKey.Store("a", struct{}{})
				r.limitersByKey.Store("b", struct{}{})
				r.limitersByKey.Store("c", struct{}{})
				r.lastAccessedByKey.Store("a", time.Now().Add(-1*time.Minute))
				r.lastAccessedByKey.Store("b", time.Now().Add(-1*time.Minute))
				r.lastAccessedByKey.Store("c", time.Now().Add(-1*time.Minute))
			},
			expectedLimiters:        []string{},
			expectedRemovedLimiters: []string{"a", "b", "c"},
		},
		{
			desc: "one is prunable",
			setup: func(r *RateLimiter) {
				r.limitersByKey.Store("a", struct{}{})
				r.limitersByKey.Store("b", struct{}{})
				r.limitersByKey.Store("c", struct{}{})
				r.lastAccessedByKey.Store("a", time.Now())
				r.lastAccessedByKey.Store("b", time.Now())
				r.lastAccessedByKey.Store("c", time.Now().Add(-1*time.Minute))
			},
			expectedLimiters:        []string{"a", "b"},
			expectedRemovedLimiters: []string{"c"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)
			ticker := tick.NewManualTicker()
			ch := make(chan struct{})
			ticker.ResetFunc = func() {
				ch <- struct{}{}
			}

			rateLimiter := &RateLimiter{
				refillInterval: time.Second,
				ticker:         ticker,
			}

			tc.setup(rateLimiter)

			go rateLimiter.PruneUnusedLimiters(ctx)
			<-ch

			ticker.Tick()
			<-ch

			for _, expectedLimiter := range tc.expectedLimiters {
				_, ok := rateLimiter.limitersByKey.Load(expectedLimiter)
				assert.True(t, ok)
			}

			for _, expectedRemovedLimiter := range tc.expectedRemovedLimiters {
				_, ok := rateLimiter.limitersByKey.Load(expectedRemovedLimiter)
				assert.False(t, ok)
			}
		})
	}
}
