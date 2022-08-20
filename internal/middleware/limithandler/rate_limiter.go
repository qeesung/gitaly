package limithandler

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/labkit/log"
	"golang.org/x/time/rate"
	"google.golang.org/protobuf/types/known/durationpb"
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

// ErrRateLimit is returned when RateLimiter determined a request has breached
// the rate request limit.
var ErrRateLimit = errors.New("rate limit reached")

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

		err := helper.ErrUnavailable(ErrRateLimit)

		detailedErr, errGeneratingDetailedErr := helper.ErrWithDetails(
			err,
			&gitalypb.LimitError{
				ErrorMessage: ErrRateLimit.Error(),
				RetryAfter:   durationpb.New(0),
			},
		)
		if errGeneratingDetailedErr != nil {
			log.WithField("rate_limit_error", err).
				WithError(errGeneratingDetailedErr).
				Error("failed to generate detailed error")

			return nil, err
		}

		return nil, detailedErr
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
					limitCfg.Interval.Duration(),
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
