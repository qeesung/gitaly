package limiter

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
)

const (
	// MaximumWatcherTimeout is the number of maximum allowed timeout when polling backoff events from watchers.
	// When this threshold is reached, a timeout polling is treated as a backoff event.
	MaximumWatcherTimeout = 5
)

// BackoffEvent is a signal that the current system is under pressure. It's returned by the watchers under the
// management of the AdaptiveCalculator at calibration points.
type BackoffEvent struct {
	WatcherName   string
	ShouldBackoff bool
	Reason        string
}

// ResourceWatcher is an interface of the watchers that monitor the system resources.
type ResourceWatcher interface {
	// Name returns the name of the resource watcher
	Name() string
	// Poll returns a backoff event when a watcher determine something goes wrong with the resource it is
	// monitoring. If everything is fine, it returns `nil`. Watchers are expected to respect the cancellation of
	// the input context.
	Poll(context.Context) (*BackoffEvent, error)
}

// AdaptiveCalculator is responsible for calculating the adaptive limits based on additive increase/multiplicative
// decrease (AIMD) algorithm. This method involves gradually increasing the limit during normal process functioning
// but quickly reducing it when an issue (backoff event) occurs. It receives a list of AdaptiveLimiter and a list of
// ResourceWatcher. Although the limits may have different settings (Initial, Min, Max, BackoffFactor), they all move
// as a whole. The caller accesses the current limits via AdaptiveLimiter.Current method.
//
// When the calculator starts, each limit value is set to its Initial limit. Periodically, the calculator polls the
// backoff events from the watchers. The current value of each limit is re-calibrated as follows:
// * limit = limit + 1 if there is no backoff event since the last calibration. The new limit cannot exceed max limit.
// * limit = limit * BackoffFactor otherwise. The new limit cannot be lower than min limit.
//
// A watcher returning an error is treated as a no backoff event.
type AdaptiveCalculator struct {
	sync.Mutex

	logger logrus.FieldLogger
	// started tells whether the calculator already starts. One calculator is allowed to be used once.
	started bool
	// calibration is the time duration until the next calibration event.
	calibration time.Duration
	// limits are the list of adaptive limits managed by this calculator.
	limits []AdaptiveLimiter
	// watchers stores a list of resource watchers that return the backoff events when queried.
	watchers []ResourceWatcher
	// watcherTimeouts is a map of counters for consecutive timeouts. The counter is reset when the associated
	// watcher returns a non-error event or exceeds MaximumWatcherTimeout.
	watcherTimeouts map[ResourceWatcher]*atomic.Int32
	// lastBackoffEvent stores the last backoff event collected from the watchers.
	lastBackoffEvent *BackoffEvent
	// tickerCreator is a custom function that returns a Ticker. It's mostly used in test the manual ticker
	tickerCreator func(duration time.Duration) helper.Ticker

	// currentLimitVec is the gauge of current limit value of an adaptive concurrency limit
	currentLimitVec *prometheus.GaugeVec
	// watcherErrorsVec is the counter of the total number of watcher errors
	watcherErrorsVec *prometheus.CounterVec
	// backoffEventsVec is the counter of the total number of backoff events
	backoffEventsVec *prometheus.CounterVec
}

// NewAdaptiveCalculator constructs a AdaptiveCalculator object. It's the responsibility of the caller to validate
// the correctness of input AdaptiveLimiter and ResourceWatcher.
func NewAdaptiveCalculator(calibration time.Duration, logger logrus.FieldLogger, limits []AdaptiveLimiter, watchers []ResourceWatcher) *AdaptiveCalculator {
	watcherTimeouts := map[ResourceWatcher]*atomic.Int32{}
	for _, watcher := range watchers {
		watcherTimeouts[watcher] = &atomic.Int32{}
	}

	return &AdaptiveCalculator{
		logger:           logger,
		calibration:      calibration,
		limits:           limits,
		watchers:         watchers,
		watcherTimeouts:  watcherTimeouts,
		lastBackoffEvent: nil,
		currentLimitVec: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "gitaly_concurrency_limiting_current_limit",
				Help: "The current limit value of an adaptive concurrency limit",
			},
			[]string{"limit"},
		),
		watcherErrorsVec: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_concurrency_limiting_watcher_errors_total",
				Help: "Counter of the total number of watcher errors",
			},
			[]string{"watcher"},
		),
		backoffEventsVec: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_concurrency_limiting_backoff_events_total",
				Help: "Counter of the total number of backoff events",
			},
			[]string{"watcher"},
		),
	}
}

// Start resets the current limit values and start a goroutine to poll the backoff events. This method exits after the
// mentioned goroutine starts.
func (c *AdaptiveCalculator) Start(ctx context.Context) (func(), error) {
	c.Lock()
	defer c.Unlock()

	if c.started {
		return nil, fmt.Errorf("adaptive calculator: already started")
	}
	c.started = true

	// Reset all limits to their initial limits
	for _, limit := range c.limits {
		c.updateLimit(limit, limit.Setting().Initial)
	}

	done := make(chan struct{})
	started := make(chan struct{})
	completed := make(chan struct{})

	go func(ctx context.Context) {
		close(started)

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer close(completed)

		tickerCreator := c.tickerCreator
		if tickerCreator == nil {
			tickerCreator = helper.NewTimerTicker
		}
		timer := tickerCreator(c.calibration)
		for {
			// Reset the timer to the next calibration point. It accounts for the resource polling latencies.
			timer.Reset()
			select {
			case <-timer.C():
				// If multiple watchers fire multiple backoff events, the calculator decreases once.
				// Usually, resources are highly correlated. When the memory level raises too high,
				// the CPU usage also increases due to page faulting, memory reclaim, GC activities, etc.
				// We might also have multiple watchers for the same resources, for example, memory
				// usage watcher and page fault counter. Hence, re-calibrating after each event will
				// cut the limits too aggressively.
				c.pollBackoffEvent(ctx)
				c.calibrateLimits(ctx)

				// Reset backoff event
				c.setLastBackoffEvent(nil)
			case <-done:
				timer.Stop()
				return
			}
		}
	}(ctx)

	<-started
	return func() {
		close(done)
		<-completed
	}, nil
}

// Describe is used to describe Prometheus metrics.
func (c *AdaptiveCalculator) Describe(descs chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(c, descs)
}

// Collect is used to collect Prometheus metrics.
func (c *AdaptiveCalculator) Collect(metrics chan<- prometheus.Metric) {
	c.currentLimitVec.Collect(metrics)
	c.watcherErrorsVec.Collect(metrics)
	c.backoffEventsVec.Collect(metrics)
}

func (c *AdaptiveCalculator) pollBackoffEvent(ctx context.Context) {
	// Set a timeout to prevent resource watcher runs forever. The deadline
	// is the next calibration event.
	ctx, cancel := context.WithTimeout(ctx, c.calibration)
	defer cancel()

	for _, w := range c.watchers {
		// If the context is cancelled, return early.
		if ctx.Err() != nil {
			return
		}

		logger := c.logger.WithField("watcher", w.Name())
		event, err := w.Poll(ctx)
		if err != nil {
			if err == context.DeadlineExceeded {
				c.watcherTimeouts[w].Add(1)
				// If the watcher timeouts for a number of consecutive times, treat it as a
				// backoff event.
				if timeoutCount := c.watcherTimeouts[w].Load(); timeoutCount >= MaximumWatcherTimeout {
					c.setLastBackoffEvent(&BackoffEvent{
						WatcherName:   w.Name(),
						ShouldBackoff: true,
						Reason:        fmt.Sprintf("%d consecutive polling timeout errors", timeoutCount),
					})
					// Reset the timeout counter. The next MaximumWatcherTimeout will trigger
					// another backoff event.
					c.watcherTimeouts[w].Store(0)
				}
			}

			if err != context.Canceled {
				c.watcherErrorsVec.WithLabelValues(w.Name()).Inc()
				logger.Errorf("poll from resource watcher: %s", err)
			}

			continue
		}
		// Reset the timeout counter if the watcher polls successfully.
		c.watcherTimeouts[w].Store(0)
		if event.ShouldBackoff {
			c.setLastBackoffEvent(event)
		}
	}
}

func (c *AdaptiveCalculator) calibrateLimits(ctx context.Context) {
	c.Lock()
	defer c.Unlock()

	if ctx.Err() != nil {
		return
	}

	for _, limit := range c.limits {
		setting := limit.Setting()

		var newLimit int
		logger := c.logger.WithField("limit", limit.Name())

		if c.lastBackoffEvent == nil {
			// Additive increase, one unit at a time
			newLimit = limit.Current() + 1
			if newLimit > setting.Max {
				newLimit = setting.Max
			}
			logger.WithFields(map[string]interface{}{
				"previous_limit": limit.Current(),
				"new_limit":      newLimit,
			}).Debugf("Additive increase")
		} else {
			// Multiplicative decrease
			newLimit = int(math.Floor(float64(limit.Current()) * setting.BackoffFactor))
			if newLimit < setting.Min {
				newLimit = setting.Min
			}
			logger.WithFields(map[string]interface{}{
				"previous_limit": limit.Current(),
				"new_limit":      newLimit,
				"watcher":        c.lastBackoffEvent.WatcherName,
				"reason":         c.lastBackoffEvent.Reason,
			}).Infof("Multiplicative decrease")
		}
		c.updateLimit(limit, newLimit)
	}
}

func (c *AdaptiveCalculator) setLastBackoffEvent(event *BackoffEvent) {
	c.Lock()
	defer c.Unlock()

	c.lastBackoffEvent = event
	if event != nil && event.ShouldBackoff {
		c.backoffEventsVec.WithLabelValues(event.WatcherName).Inc()
	}
}

func (c *AdaptiveCalculator) updateLimit(limit AdaptiveLimiter, newLimit int) {
	limit.Update(newLimit)
	c.currentLimitVec.WithLabelValues(limit.Name()).Set(float64(newLimit))
}
