package limithandler

import (
	"context"
	"strings"
	"time"

	grpcmwtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/limiter"
	"google.golang.org/grpc"
)

// GetLockKey function defines the lock key of an RPC invocation based on its context
type GetLockKey func(context.Context) string

// LimitConcurrencyByRepo implements GetLockKey by using the repository path as lock.
func LimitConcurrencyByRepo(ctx context.Context) string {
	tags := grpcmwtags.Extract(ctx)
	ctxValue := tags.Values()["grpc.request.repoPath"]
	if ctxValue == nil {
		return ""
	}

	s, ok := ctxValue.(string)
	if ok {
		return s
	}

	return ""
}

// LimiterMiddleware contains rate limiter state
type LimiterMiddleware struct {
	methodLimiters        map[string]limiter.Limiter
	getLockKey            GetLockKey
	requestsDroppedMetric *prometheus.CounterVec
	collect               func(metrics chan<- prometheus.Metric)
}

// New creates a new middleware that limits requests. SetupFunc sets up the
// middlware with a specific kind of limiter.
func New(cfg config.Cfg, getLockKey GetLockKey, setupMiddleware SetupFunc) *LimiterMiddleware {
	middleware := &LimiterMiddleware{
		getLockKey: getLockKey,
		requestsDroppedMetric: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_requests_dropped_total",
				Help: "Number of requests dropped from the queue",
			},
			[]string{
				"system",
				"grpc_service",
				"grpc_method",
				"reason",
			},
		),
	}

	setupMiddleware(cfg, middleware)

	return middleware
}

// Describe is used to describe Prometheus metrics.
func (c *LimiterMiddleware) Describe(descs chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, descs)
}

// Collect is used to collect Prometheus metrics.
func (c *LimiterMiddleware) Collect(metrics chan<- prometheus.Metric) {
	c.requestsDroppedMetric.Collect(metrics)
	if c.collect != nil {
		c.collect(metrics)
	}
}

// UnaryInterceptor returns a Unary Interceptor
func (c *LimiterMiddleware) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		lockKey := c.getLockKey(ctx)
		if lockKey == "" {
			return handler(ctx, req)
		}

		limiter := c.methodLimiters[info.FullMethod]
		if limiter == nil {
			// No concurrency limiting
			return handler(ctx, req)
		}

		return limiter.Limit(ctx, lockKey, func() (interface{}, error) {
			return handler(ctx, req)
		})
	}
}

// StreamInterceptor returns a Stream Interceptor
func (c *LimiterMiddleware) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		wrapper := &wrappedStream{stream, info, c, true}
		return handler(srv, wrapper)
	}
}

// SetupFunc set up a middleware to limiting requests
type SetupFunc func(cfg config.Cfg, middleware *LimiterMiddleware)

type wrappedStream struct {
	grpc.ServerStream
	info              *grpc.StreamServerInfo
	limiterMiddleware *LimiterMiddleware
	initial           bool
}

func (w *wrappedStream) RecvMsg(m interface{}) error {
	if err := w.ServerStream.RecvMsg(m); err != nil {
		return err
	}

	// Only perform limiting on the first request of a stream
	if !w.initial {
		return nil
	}

	w.initial = false

	ctx := w.Context()

	lockKey := w.limiterMiddleware.getLockKey(ctx)
	if lockKey == "" {
		return nil
	}

	limiter := w.limiterMiddleware.methodLimiters[w.info.FullMethod]
	if limiter == nil {
		// No concurrency limiting
		return nil
	}

	ready := make(chan struct{})
	errs := make(chan error)
	go func() {
		if _, err := limiter.Limit(ctx, lockKey, func() (interface{}, error) {
			close(ready)
			<-ctx.Done()
			return nil, nil
		}); err != nil {
			errs <- err
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-ready:
		// It's our turn!
		return nil
	case err := <-errs:
		return err
	}
}

// WithConcurrencyLimiters sets up middleware to limit the concurrency of
// requests based on RPC and repository
func WithConcurrencyLimiters(cfg config.Cfg, middleware *LimiterMiddleware) {
	acquiringSecondsMetric := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "gitaly",
			Subsystem: "concurrency_limiting",
			Name:      "acquiring_seconds",
			Help:      "Histogram of time calls are rate limited (in seconds)",
			Buckets:   cfg.Prometheus.GRPCLatencyBuckets,
		},
		[]string{"system", "grpc_service", "grpc_method"},
	)
	inProgressMetric := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "gitaly",
			Subsystem: "concurrency_limiting",
			Name:      "in_progress",
			Help:      "Gauge of number of concurrent in-progress calls",
		},
		[]string{"system", "grpc_service", "grpc_method"},
	)
	queuedMetric := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "gitaly",
			Subsystem: "concurrency_limiting",
			Name:      "queued",
			Help:      "Gauge of number of queued calls",
		},
		[]string{"system", "grpc_service", "grpc_method"},
	)

	middleware.collect = func(metrics chan<- prometheus.Metric) {
		acquiringSecondsMetric.Collect(metrics)
		inProgressMetric.Collect(metrics)
		queuedMetric.Collect(metrics)
	}

	result := make(map[string]limiter.Limiter)
	for _, limit := range cfg.Concurrency {
		limit := limit

		newTickerFunc := func() helper.Ticker {
			return helper.NewManualTicker()
		}

		if limit.MaxQueueWait > 0 {
			newTickerFunc = func() helper.Ticker {
				return helper.NewTimerTicker(limit.MaxQueueWait.Duration())
			}
		}

		result[limit.RPC] = limiter.NewConcurrencyLimiter(
			limiter.NewAdaptiveLimit("staticLimit", limiter.AdaptiveSetting{Initial: limit.MaxPerRepo}),
			limit.MaxQueueSize,
			newTickerFunc,
			limiter.NewPerRPCPromMonitor(
				"gitaly", limit.RPC,
				queuedMetric, inProgressMetric, acquiringSecondsMetric, middleware.requestsDroppedMetric,
			),
		)
	}

	// Set default for ReplicateRepository.
	replicateRepositoryFullMethod := "/gitaly.RepositoryService/ReplicateRepository"
	if _, ok := result[replicateRepositoryFullMethod]; !ok {
		result[replicateRepositoryFullMethod] = limiter.NewConcurrencyLimiter(
			limiter.NewAdaptiveLimit("staticLimit", limiter.AdaptiveSetting{Initial: 1}),
			0,
			func() helper.Ticker {
				return helper.NewManualTicker()
			},
			limiter.NewPerRPCPromMonitor(
				"gitaly", replicateRepositoryFullMethod,
				queuedMetric, inProgressMetric, acquiringSecondsMetric, middleware.requestsDroppedMetric,
			),
		)
	}

	middleware.methodLimiters = result
}

// WithRateLimiters sets up a middleware with limiters that limit requests
// based on its rate per second per RPC
func WithRateLimiters(ctx context.Context) SetupFunc {
	return func(cfg config.Cfg, middleware *LimiterMiddleware) {
		result := make(map[string]limiter.Limiter)

		for _, limitCfg := range cfg.RateLimiting {
			if limitCfg.Burst > 0 && limitCfg.Interval > 0 {
				serviceName, methodName := splitMethodName(limitCfg.RPC)
				rateLimiter := limiter.NewRateLimiter(
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

func splitMethodName(fullMethodName string) (string, string) {
	fullMethodName = strings.TrimPrefix(fullMethodName, "/") // remove leading slash
	service, method, ok := strings.Cut(fullMethodName, "/")
	if !ok {
		return "unknown", "unknown"
	}
	return service, method
}
