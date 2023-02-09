package limithandler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/tick"
	"gitlab.com/gitlab-org/gitaly/v15/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

// ErrMaxQueueTime indicates a request has reached the maximum time allowed to wait in the
// concurrency queue.
var ErrMaxQueueTime = errors.New("maximum time in concurrency queue reached")

// ErrMaxQueueSize indicates the concurrency queue has reached its maximum size
var ErrMaxQueueSize = errors.New("maximum queue size reached")

// QueueTickerCreator is a function that provides a ticker
type QueueTickerCreator func() tick.Ticker

// keyedConcurrencyLimiter is a concurrency limiter that applies to a specific keyed resource.
type keyedConcurrencyLimiter struct {
	refcount               int
	monitor                ConcurrencyMonitor
	maxQueuedTickerCreator QueueTickerCreator

	// concurrencyTokens is the channel of available concurrency tokens, where every token
	// allows one concurrent call to the concurrency-limited function.
	concurrencyTokens chan struct{}
	// queueTokens is the channel of available queue tokens, where every token allows one
	// concurrent call to be admitted to the queue.
	queueTokens chan struct{}
}

// acquire tries to acquire the semaphore. It may fail if the admission queue is full or if the max
// queue-time ticker ticks before acquiring a concurrency token.
func (sem *keyedConcurrencyLimiter) acquire(ctx context.Context) (returnedErr error) {
	if sem.queueTokens != nil {
		// Try to acquire the queueing token. The queueing token is used to control how many
		// callers may wait for the concurrency token at the same time. If there are no more
		// queueing tokens then this indicates that the queue is full and we thus return an
		// error immediately.
		select {
		case sem.queueTokens <- struct{}{}:
			// We have acquired a queueing token, so we need to release it if acquiring
			// the concurrency token fails. If we succeed to acquire the concurrency
			// token though then we retain the queueing token until the caller signals
			// that the concurrency-limited function has finished. As a consequence the
			// queue token is returned together with the concurrency token.
			//
			// A simpler model would be to just have `maxQueueLength` many queueing
			// tokens. But this would add concurrency-limiting when acquiring the queue
			// token itself, which is not what we want to do. Instead, we want to admit
			// as many callers into the queue as the queue length permits plus the
			// number of available concurrency tokens allows.
			defer func() {
				if returnedErr != nil {
					<-sem.queueTokens
				}
			}()
		default:
			sem.monitor.Dropped(ctx, "max_size")
			return ErrMaxQueueSize
		}
	}

	// We are queued now, so let's tell the monitor. Furthermore, even though we're still
	// holding the queueing token when this function exits successfully we also tell the monitor
	// that we have exited the queue. It is only an implemenation detail anyway that we hold on
	// to the token, so the monitor shouldn't care about that.
	sem.monitor.Queued(ctx)
	defer sem.monitor.Dequeued(ctx)

	// Set up the ticker that keeps us from waiting indefinitely on the concurrency token.
	var ticker tick.Ticker
	if sem.maxQueuedTickerCreator != nil {
		ticker = sem.maxQueuedTickerCreator()
	} else {
		ticker = tick.Ticker(tick.NewManualTicker())
	}

	defer ticker.Stop()
	ticker.Reset()

	// Try to acquire the concurrency token now that we're in the queue.
	select {
	case sem.concurrencyTokens <- struct{}{}:
		return nil
	case <-ticker.C():
		sem.monitor.Dropped(ctx, "max_time")
		return ErrMaxQueueTime
	case <-ctx.Done():
		return ctx.Err()
	}
}

// release releases the acquired tokens.
func (sem *keyedConcurrencyLimiter) release() {
	if sem.queueTokens != nil {
		<-sem.queueTokens
	}
	<-sem.concurrencyTokens
}

// ConcurrencyLimiter contains rate limiter state.
type ConcurrencyLimiter struct {
	// maxConcurrencyLimit is the maximum number of concurrent calls to the limited function.
	// This limit is per key.
	maxConcurrencyLimit int64
	// maxQueueLength is the maximum number of operations allowed to wait in a queued state.
	// This limit is global and applies before the concurrency limit. Subsequent incoming
	// operations will be rejected with an error immediately.
	maxQueueLength int64
	// maxQueuedTickerCreator is a function that creates a ticker used to determine how long a
	// call may be queued.
	maxQueuedTickerCreator QueueTickerCreator

	// monitor is a monitor that will get notified of the state of concurrency-limited RPC
	// calls.
	monitor ConcurrencyMonitor

	m sync.RWMutex
	// limitsByKey tracks all concurrency limits per key. Its per-key entries are lazily created
	// and will get evicted once there are no concurrency-limited calls for any such key
	// anymore.
	limitsByKey map[string]*keyedConcurrencyLimiter
}

// NewConcurrencyLimiter creates a new concurrency rate limiter.
func NewConcurrencyLimiter(maxConcurrencyLimit, maxQueueLength int, maxQueuedTickerCreator QueueTickerCreator, monitor ConcurrencyMonitor) *ConcurrencyLimiter {
	if monitor == nil {
		monitor = NewNoopConcurrencyMonitor()
	}

	return &ConcurrencyLimiter{
		maxConcurrencyLimit:    int64(maxConcurrencyLimit),
		maxQueueLength:         int64(maxQueueLength),
		maxQueuedTickerCreator: maxQueuedTickerCreator,
		monitor:                monitor,
		limitsByKey:            make(map[string]*keyedConcurrencyLimiter),
	}
}

// Limit will limit the concurrency of the limited function f. There are two distinct mechanisms
// that limit execution of the function:
//
//  1. First, every call will enter the per-key queue. This queue limits how many callers may try to
//     acquire their per-key semaphore at the same time. If the queue is full the caller will be
//     rejected.
//  2. Second, when the caller has successfully entered the queue, they try to acquire their per-key
//     semaphore. If this takes longer than the maximum queueing limit then the caller will be
//     dequeued and gets an error.
func (c *ConcurrencyLimiter) Limit(ctx context.Context, limitingKey string, f LimitedFunc) (interface{}, error) {
	if c.maxConcurrencyLimit <= 0 {
		return f()
	}

	sem := c.getConcurrencyLimit(limitingKey)
	defer c.putConcurrencyLimit(limitingKey)

	start := time.Now()

	if err := sem.acquire(ctx); err != nil {
		switch err {
		case ErrMaxQueueSize:
			return nil, structerr.NewResourceExhausted("%w", ErrMaxQueueSize).WithDetail(&gitalypb.LimitError{
				ErrorMessage: err.Error(),
				RetryAfter:   durationpb.New(0),
			})
		case ErrMaxQueueTime:
			return nil, structerr.NewResourceExhausted("%w", ErrMaxQueueTime).WithDetail(&gitalypb.LimitError{
				ErrorMessage: err.Error(),
				RetryAfter:   durationpb.New(0),
			})
		default:
			ctxlogrus.Extract(ctx).WithField("limiting_key", limitingKey).WithError(err).Error("unexpected error when dequeueing request")
			return nil, err
		}
	}
	defer sem.release()

	c.monitor.Enter(ctx, time.Since(start))
	defer c.monitor.Exit(ctx)
	return f()
}

// getConcurrencyLimit retrieves the concurrency limit for the given key. If no such limiter exists
// it will be lazily constructed.
func (c *ConcurrencyLimiter) getConcurrencyLimit(limitingKey string) *keyedConcurrencyLimiter {
	c.m.Lock()
	defer c.m.Unlock()

	if c.limitsByKey[limitingKey] == nil {
		// Set up the queue tokens in case a maximum queue length was requested. As the
		// queue tokens are kept during the whole lifetime of the concurrency-limited
		// function we add the concurrency tokens to the number of available token.
		var queueTokens chan struct{}
		if c.maxQueueLength > 0 {
			queueTokens = make(chan struct{}, c.maxConcurrencyLimit+c.maxQueueLength)
		}

		c.limitsByKey[limitingKey] = &keyedConcurrencyLimiter{
			monitor:                c.monitor,
			maxQueuedTickerCreator: c.maxQueuedTickerCreator,
			concurrencyTokens:      make(chan struct{}, c.maxConcurrencyLimit),
			queueTokens:            queueTokens,
		}
	}

	c.limitsByKey[limitingKey].refcount++

	return c.limitsByKey[limitingKey]
}

// putConcurrencyLimit drops the reference to the concurrency limit identified by the given key.
// This must only ever be called after `getConcurrencyLimit()` for the same key. If the reference
// count of the concurrency limit drops to zero then it will be destroyed.
func (c *ConcurrencyLimiter) putConcurrencyLimit(limitingKey string) {
	c.m.Lock()
	defer c.m.Unlock()

	ref := c.limitsByKey[limitingKey]
	if ref == nil {
		panic("semaphore should be in the map")
	}

	if ref.refcount <= 0 {
		panic(fmt.Sprintf("bad semaphore ref refcount %d", ref.refcount))
	}

	ref.refcount--
	if ref.refcount == 0 {
		delete(c.limitsByKey, limitingKey)
	}
}

func (c *ConcurrencyLimiter) countSemaphores() int {
	c.m.RLock()
	defer c.m.RUnlock()

	return len(c.limitsByKey)
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

	result := make(map[string]Limiter)
	for _, limit := range cfg.Concurrency {
		limit := limit

		newTickerFunc := func() tick.Ticker {
			return tick.NewManualTicker()
		}

		if limit.MaxQueueWait > 0 {
			newTickerFunc = func() tick.Ticker {
				return tick.NewTimerTicker(limit.MaxQueueWait.Duration())
			}
		}

		result[limit.RPC] = NewConcurrencyLimiter(
			limit.MaxPerRepo,
			limit.MaxQueueSize,
			newTickerFunc,
			newPerRPCPromMonitor("gitaly", limit.RPC, queuedMetric, inProgressMetric,
				acquiringSecondsMetric, middleware.requestsDroppedMetric),
		)
	}

	// Set default for ReplicateRepository.
	replicateRepositoryFullMethod := "/gitaly.RepositoryService/ReplicateRepository"
	if _, ok := result[replicateRepositoryFullMethod]; !ok {
		result[replicateRepositoryFullMethod] = NewConcurrencyLimiter(
			1,
			0,
			func() tick.Ticker {
				return tick.NewManualTicker()
			},
			newPerRPCPromMonitor("gitaly", replicateRepositoryFullMethod, queuedMetric,
				inProgressMetric, acquiringSecondsMetric, middleware.requestsDroppedMetric))
	}

	middleware.methodLimiters = result
}
