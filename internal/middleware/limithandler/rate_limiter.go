package limithandler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/metadata/featureflag"
	"golang.org/x/time/rate"
)

// RateLimiter is an implementation of Limiter that puts a hard limit on the
// number of requests per second
type RateLimiter struct {
	limitersByKey, lastAccessedByKey sync.Map
	refillInterval                   time.Duration
	burst                            int
	requestsDroppedMetric            prometheus.Counter
	ticker                           helper.Ticker
}

// Limit rejects an incoming reequest if the maximum number of requests per
// second has been reached
func (r *RateLimiter) Limit(ctx context.Context, lockKey string, f LimitedFunc) (interface{}, error) {
	limiter, _ := r.limitersByKey.LoadOrStore(
		lockKey,
		rate.NewLimiter(rate.Every(r.refillInterval), r.burst),
	)
	r.lastAccessedByKey.Store(lockKey, time.Now())

	if !limiter.(*rate.Limiter).Allow() {
		// For now, we are only emitting this metric to get an idea of the shape
		// of traffic.
		r.requestsDroppedMetric.Inc()
		if featureflag.RateLimit.IsEnabled(ctx) {
			return nil, helper.ErrUnavailable(errors.New("too many requests"))
		}
	}

	return f()
}

// PruneUnusedLimiters enters an infinite loop to periodically check if any
// limiters can be cleaned up. This is meant to be called in a separate
// goroutine.
func (r *RateLimiter) PruneUnusedLimiters(ctx context.Context) {
	defer r.ticker.Stop()
	for {
		r.ticker.Reset()
		select {
		case <-r.ticker.C():
			r.pruneUnusedLimiters()
		case <-ctx.Done():
			return
		}
	}
}

func (r *RateLimiter) pruneUnusedLimiters() {
	r.lastAccessedByKey.Range(func(key, value interface{}) bool {
		if value.(time.Time).Before(time.Now().Add(-10 * r.refillInterval)) {
			r.limitersByKey.Delete(key)
		}

		return true
	})
}

// NewRateLimiter creates a new instance of RateLimiter
func NewRateLimiter(
	refillInterval time.Duration,
	burst int,
	ticker helper.Ticker,
	requestsDroppedMetric prometheus.Counter,
) *RateLimiter {
	r := &RateLimiter{
		refillInterval:        refillInterval,
		burst:                 burst,
		requestsDroppedMetric: requestsDroppedMetric,
		ticker:                ticker,
	}

	return r
}

// WithRateLimiters sets up a middleware with limiters that limit requests
// based on its rate per second per RPC
func WithRateLimiters(ctx context.Context) SetupFunc {
	return func(cfg config.Cfg, middleware *LimiterMiddleware) {
		result := make(map[string]Limiter)

		for _, limitCfg := range cfg.RateLimiting {
			if limitCfg.Burst > 0 && limitCfg.Interval > 0 {
				serviceName, methodName := splitMethodName(limitCfg.RPC)
				rateLimiter := NewRateLimiter(
					limitCfg.Interval,
					limitCfg.Burst,
					helper.NewTimerTicker(5*time.Minute),
					middleware.requestsDroppedMetric.With(prometheus.Labels{
						"system":       "gitaly",
						"grpc_service": serviceName,
						"grpc_method":  methodName,
						"reason":       "rate",
					}),
				)
				result[limitCfg.RPC] = rateLimiter
				go rateLimiter.PruneUnusedLimiters(ctx)
			}
		}

		middleware.methodLimiters = result
	}
}
