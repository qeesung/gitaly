package limithandler

import (
	"context"

	grpcmwtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
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

// Limiter limits incoming requests
type Limiter interface {
	Limit(ctx context.Context, lockKey string, f LimitedFunc) (interface{}, error)
}

// LimitedFunc represents a function that will be limited
type LimitedFunc func() (resp interface{}, err error)

// LimiterMiddleware contains rate limiter state
type LimiterMiddleware struct {
	methodLimiters        map[string]Limiter
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
