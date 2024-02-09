package limiter

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestAdaptiveCalculator_alreadyStarted(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, func(t *testing.T, ctx context.Context) {
		calculator := NewAdaptiveCalculator(10*time.Millisecond, testhelper.SharedLogger(t), nil, nil)

		stop, err := calculator.Start(ctx)
		require.NoError(t, err)

		stop2, err := calculator.Start(ctx)
		require.Errorf(t, err, "adaptive calculator: already started")
		require.Nil(t, stop2)

		stop()
	})
}

func TestAdaptiveCalculator_realTimerTicker(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, func(t *testing.T, ctx context.Context) {
		logger := testhelper.NewLogger(t)
		hook := testhelper.AddLoggerHook(logger)

		limit := newTestLimit("testLimit", 25, 100, 10, 0.5)
		watcher := newTestWatcher("testWatcher", []string{"", "", "", "", ""}, nil)

		calibration := 10 * time.Millisecond
		calculator := NewAdaptiveCalculator(calibration, logger, []AdaptiveLimiter{limit}, []ResourceWatcher{watcher})

		stop, err := calculator.Start(ctx)
		require.NoError(t, err)
		limit.waitForEvents(6)
		stop()

		require.Equal(t, []int{25, 26, 27, 28, 29, 30}, limit.currents[:6])
		assertLogs(t, []string{}, hook.AllEntries())
	})
}

func TestAdaptiveCalculator(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UseResizableSemaphoreInConcurrencyLimiter, featureflag.UseResizableSemaphoreLifoStrategy).Run(t, func(t *testing.T, ctx context.Context) {
		tests := []struct {
			desc       string
			limits     []AdaptiveLimiter
			watchers   []ResourceWatcher
			waitEvents int
			// The first captured limit is the initial limit
			expectedLimits  map[string][]int
			expectedLogs    []string
			expectedMetrics string
		}{
			{
				desc:       "Empty watchers",
				waitEvents: 5,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit", 25, 100, 10, 0.5),
				},
				watchers: []ResourceWatcher{},
				expectedLimits: map[string][]int{
					"testLimit": {25, 26, 27, 28, 29, 30},
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit"} {testLimit}

`,
			},
			{
				desc:           "Empty limits and watchers",
				waitEvents:     5,
				limits:         []AdaptiveLimiter{},
				watchers:       []ResourceWatcher{},
				expectedLimits: map[string][]int{},
			},
			{
				desc:       "Additive increase",
				waitEvents: 5,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit", 25, 100, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher", []string{"", "", "", "", ""}, nil),
				},
				expectedLimits: map[string][]int{
					"testLimit": {25, 26, 27, 28, 29, 30},
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit"} {testLimit}

`,
			},
			{
				desc:       "Additive increase until reaching the max limit",
				waitEvents: 5,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit", 25, 27, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher", []string{"", "", "", "", ""}, nil),
				},
				expectedLimits: map[string][]int{
					"testLimit": {25, 26, 27, 27, 27, 27},
				},
				// In this test, the current limit never exceeds the max value. No need to replace the value.
				expectedMetrics: `# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit"} 27

`,
			},
			{
				desc:       "Additive increase until a backoff event",
				waitEvents: 7,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit", 25, 100, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher", []string{"", "", "", "", "cgroup exceeds limit", "", ""}, nil),
				},
				expectedLimits: map[string][]int{
					"testLimit": {25, 26, 27, 28, 29, 14, 15, 16},
				},
				expectedLogs: []string{
					`level=info msg="Multiplicative decrease" limit=testLimit new_limit=14 previous_limit=29 reason="cgroup exceeds limit" stats.current=5678 stats.threshold=1234 watcher=testWatcher`,
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_backoff_events_total Counter of the total number of backoff events
# TYPE gitaly_concurrency_limiting_backoff_events_total counter
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher"} 1
# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit"} {testLimit}

`,
			},
			{
				desc:       "Multiplicative decrease until reaching min limit",
				waitEvents: 6,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit", 25, 100, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher", []string{"", "", "reason 1", "reason 2", "reason 3", ""}, nil),
				},
				expectedLimits: map[string][]int{
					"testLimit": {25, 26, 27, 13, 10, 10, 11},
				},
				expectedLogs: []string{
					`level=info msg="Multiplicative decrease" limit=testLimit new_limit=13 previous_limit=27 reason="reason 1" stats.current=5678 stats.threshold=1234 watcher=testWatcher`,
					`level=info msg="Multiplicative decrease" limit=testLimit new_limit=10 previous_limit=13 reason="reason 2" stats.current=5678 stats.threshold=1234 watcher=testWatcher`,
					`level=info msg="Multiplicative decrease" limit=testLimit new_limit=10 previous_limit=10 reason="reason 3" stats.current=5678 stats.threshold=1234 watcher=testWatcher`,
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_backoff_events_total Counter of the total number of backoff events
# TYPE gitaly_concurrency_limiting_backoff_events_total counter
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher"} 3
# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit"} {testLimit}

`,
			},
			{
				desc:       "Additive increase multiple limits",
				waitEvents: 5,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit1", 25, 100, 10, 0.5),
					newTestLimit("testLimit2", 15, 30, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher", []string{"", "", "", "", ""}, nil),
				},
				expectedLimits: map[string][]int{
					"testLimit1": {25, 26, 27, 28, 29, 30},
					"testLimit2": {15, 16, 17, 18, 19, 20},
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit1"} {testLimit1}
gitaly_concurrency_limiting_current_limit{limit="testLimit2"} {testLimit2}

`,
			},
			{
				desc:       "Additive increase multiple limits until a backoff event",
				waitEvents: 7,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit1", 25, 100, 10, 0.5),
					newTestLimit("testLimit2", 15, 30, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher", []string{"", "", "", "", "", "cgroup exceeds limit", ""}, nil),
				},
				expectedLimits: map[string][]int{
					"testLimit1": {25, 26, 27, 28, 29, 30, 15, 16},
					"testLimit2": {15, 16, 17, 18, 19, 20, 10, 11},
				},
				expectedLogs: []string{
					`level=info msg="Multiplicative decrease" limit=testLimit1 new_limit=15 previous_limit=30 reason="cgroup exceeds limit" stats.current=5678 stats.threshold=1234 watcher=testWatcher`,
					`level=info msg="Multiplicative decrease" limit=testLimit2 new_limit=10 previous_limit=20 reason="cgroup exceeds limit" stats.current=5678 stats.threshold=1234 watcher=testWatcher`,
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_backoff_events_total Counter of the total number of backoff events
# TYPE gitaly_concurrency_limiting_backoff_events_total counter
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher"} 1
# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit1"} {testLimit1}
gitaly_concurrency_limiting_current_limit{limit="testLimit2"} {testLimit2}

`,
			},
			{
				desc:       "Additive increase multiple limits until a backoff event with multiple watchers",
				waitEvents: 10,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit1", 25, 100, 10, 0.5),
					newTestLimit("testLimit2", 15, 30, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher1", []string{"", "", "", "", "", "", "", "", "cgroup exceeds limit 1", ""}, nil),
					newTestWatcher("testWatcher2", []string{"", "", "", "", "", "", "", "", "", ""}, nil),
					newTestWatcher("testWatcher3", []string{"", "", "cgroup exceeds limit 2", "", "", "", "", "", ""}, nil),
				},
				expectedLimits: map[string][]int{
					"testLimit1": {25, 26, 27, 13, 14, 15, 16, 17, 18, 10, 11},
					"testLimit2": {15, 16, 17, 10, 11, 12, 13, 14, 15, 10, 11},
				},
				expectedLogs: []string{
					`level=info msg="Multiplicative decrease" limit=testLimit1 new_limit=13 previous_limit=27 reason="cgroup exceeds limit 2" stats.current=5678 stats.threshold=1234 watcher=testWatcher3`,
					`level=info msg="Multiplicative decrease" limit=testLimit2 new_limit=10 previous_limit=17 reason="cgroup exceeds limit 2" stats.current=5678 stats.threshold=1234 watcher=testWatcher3`,
					`level=info msg="Multiplicative decrease" limit=testLimit1 new_limit=10 previous_limit=18 reason="cgroup exceeds limit 1" stats.current=5678 stats.threshold=1234 watcher=testWatcher1`,
					`level=info msg="Multiplicative decrease" limit=testLimit2 new_limit=10 previous_limit=15 reason="cgroup exceeds limit 1" stats.current=5678 stats.threshold=1234 watcher=testWatcher1`,
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_backoff_events_total Counter of the total number of backoff events
# TYPE gitaly_concurrency_limiting_backoff_events_total counter
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher1"} 1
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher3"} 1
# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit1"} {testLimit1}
gitaly_concurrency_limiting_current_limit{limit="testLimit2"} {testLimit2}

`,
			},
			{
				desc:       "Additive increase multiple limits until multiple watchers return multiple errors",
				waitEvents: 5,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit1", 25, 100, 10, 0.5),
					newTestLimit("testLimit2", 15, 30, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher1", []string{"", "", "cgroup exceeds limit 1", "", ""}, nil),
					newTestWatcher("testWatcher2", []string{"", "", "", "", ""}, nil),
					newTestWatcher("testWatcher3", []string{"", "", "cgroup exceeds limit 2", "", ""}, nil),
				},
				expectedLimits: map[string][]int{
					"testLimit1": {25, 26, 27, 13, 14, 15},
					"testLimit2": {15, 16, 17, 10, 11, 12},
				},
				expectedLogs: []string{
					`level=info msg="Multiplicative decrease" limit=testLimit1 new_limit=13 previous_limit=27 reason="cgroup exceeds limit 2" stats.current=5678 stats.threshold=1234 watcher=testWatcher3`,
					`level=info msg="Multiplicative decrease" limit=testLimit2 new_limit=10 previous_limit=17 reason="cgroup exceeds limit 2" stats.current=5678 stats.threshold=1234 watcher=testWatcher3`,
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_backoff_events_total Counter of the total number of backoff events
# TYPE gitaly_concurrency_limiting_backoff_events_total counter
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher1"} 1
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher3"} 1
# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit1"} {testLimit1}
gitaly_concurrency_limiting_current_limit{limit="testLimit2"} {testLimit2}

`,
			},
			{
				desc:       "a watcher returns an error",
				waitEvents: 5,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit1", 25, 100, 10, 0.5),
					newTestLimit("testLimit2", 15, 30, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher1", []string{"", "", "", "", ""}, []error{nil, nil, nil, nil, nil}),
					newTestWatcher("testWatcher2", []string{"", "", "", "", ""}, []error{nil, nil, nil, fmt.Errorf("unexpected"), nil}),
					newTestWatcher("testWatcher3", []string{"", "", "", "", ""}, []error{nil, fmt.Errorf("unexpected"), nil, nil, nil}),
				},
				expectedLimits: map[string][]int{
					"testLimit1": {25, 26, 27, 28, 29, 30},
					"testLimit2": {15, 16, 17, 18, 19, 20},
				},
				expectedLogs: []string{
					`level=error msg="poll from resource watcher failed" error=unexpected watcher=testWatcher3`,
					`level=error msg="poll from resource watcher failed" error=unexpected watcher=testWatcher2`,
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit1"} {testLimit1}
gitaly_concurrency_limiting_current_limit{limit="testLimit2"} {testLimit2}
# HELP gitaly_concurrency_limiting_watcher_errors_total Counter of the total number of watcher errors
# TYPE gitaly_concurrency_limiting_watcher_errors_total counter
gitaly_concurrency_limiting_watcher_errors_total{watcher="testWatcher2"} 1
gitaly_concurrency_limiting_watcher_errors_total{watcher="testWatcher3"} 1

`,
			},
			{
				desc:       "a watcher returns an error at the same time another watcher returns backoff event",
				waitEvents: 5,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit1", 25, 100, 10, 0.5),
					newTestLimit("testLimit2", 15, 30, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher1", []string{"", "", "", "backoff please", ""}, []error{nil, nil, nil, nil, nil}),
					newTestWatcher("testWatcher2", []string{"", "", "", "", ""}, []error{nil, nil, nil, fmt.Errorf("unexpected"), nil}),
					newTestWatcher("testWatcher3", []string{"", "", "", "", ""}, []error{nil, fmt.Errorf("unexpected"), nil, nil, nil}),
				},
				expectedLimits: map[string][]int{
					"testLimit1": {25, 26, 27, 28, 14, 15},
					"testLimit2": {15, 16, 17, 18, 10, 11},
				},
				expectedLogs: []string{
					`level=error msg="poll from resource watcher failed" error=unexpected watcher=testWatcher3`,
					`level=error msg="poll from resource watcher failed" error=unexpected watcher=testWatcher2`,
					`level=info msg="Multiplicative decrease" limit=testLimit1 new_limit=14 previous_limit=28 reason="backoff please" stats.current=5678 stats.threshold=1234 watcher=testWatcher1`,
					`level=info msg="Multiplicative decrease" limit=testLimit2 new_limit=10 previous_limit=18 reason="backoff please" stats.current=5678 stats.threshold=1234 watcher=testWatcher1`,
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_backoff_events_total Counter of the total number of backoff events
# TYPE gitaly_concurrency_limiting_backoff_events_total counter
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher1"} 1
# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit1"} {testLimit1}
gitaly_concurrency_limiting_current_limit{limit="testLimit2"} {testLimit2}
# HELP gitaly_concurrency_limiting_watcher_errors_total Counter of the total number of watcher errors
# TYPE gitaly_concurrency_limiting_watcher_errors_total counter
gitaly_concurrency_limiting_watcher_errors_total{watcher="testWatcher2"} 1
gitaly_concurrency_limiting_watcher_errors_total{watcher="testWatcher3"} 1

`,
			},
			{
				desc:       "a watcher returns context canceled error",
				waitEvents: 5,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit1", 25, 100, 10, 0.5),
					newTestLimit("testLimit2", 15, 30, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher1", []string{"", "", "", "", ""}, []error{nil, nil, nil, nil, context.Canceled}),
					newTestWatcher("testWatcher2", []string{"", "", "", "", ""}, []error{nil, nil, nil, nil, context.Canceled}),
					newTestWatcher("testWatcher3", []string{"", "", "", "", ""}, []error{nil, nil, nil, nil, context.Canceled}),
				},
				expectedLimits: map[string][]int{
					"testLimit1": {25, 26, 27, 28, 29, 30},
					"testLimit2": {15, 16, 17, 18, 19, 20},
				},
				expectedLogs: []string{},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit1"} {testLimit1}
gitaly_concurrency_limiting_current_limit{limit="testLimit2"} {testLimit2}

`,
			},
			{
				desc:       "a watcher returns some timeout errors",
				waitEvents: 5,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit1", 25, 100, 10, 0.5),
					newTestLimit("testLimit2", 15, 30, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher("testWatcher", []string{"", "", "", "", ""}, []error{nil, context.DeadlineExceeded, context.DeadlineExceeded, nil, nil}),
				},
				expectedLimits: map[string][]int{
					"testLimit1": {25, 26, 27, 28, 29, 30},
					"testLimit2": {15, 16, 17, 18, 19, 20},
				},
				expectedLogs: []string{
					// Not enough timeout to trigger a backoff event
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit1"} {testLimit1}
gitaly_concurrency_limiting_current_limit{limit="testLimit2"} {testLimit2}
# HELP gitaly_concurrency_limiting_watcher_errors_total Counter of the total number of watcher errors
# TYPE gitaly_concurrency_limiting_watcher_errors_total counter
gitaly_concurrency_limiting_watcher_errors_total{watcher="testWatcher"} 2

`,
			},
			{
				desc:       "a watcher returns 5 consecutive timeout errors",
				waitEvents: 6,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit1", 25, 100, 10, 0.5),
					newTestLimit("testLimit2", 15, 30, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher(
						"testWatcher",
						[]string{"", "", "", "", "", ""},
						[]error{context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, nil},
					),
				},
				expectedLimits: map[string][]int{
					"testLimit1": {25, 26, 27, 28, 29, 14, 15},
					"testLimit2": {15, 16, 17, 18, 19, 10, 11},
				},
				expectedLogs: []string{
					// The last timeout triggers a backoff event, then increases again
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
					`level=info msg="Multiplicative decrease" limit=testLimit1 new_limit=14 previous_limit=29 reason="5 consecutive polling timeout errors" watcher=testWatcher`,
					`level=info msg="Multiplicative decrease" limit=testLimit2 new_limit=10 previous_limit=19 reason="5 consecutive polling timeout errors" watcher=testWatcher`,
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_backoff_events_total Counter of the total number of backoff events
# TYPE gitaly_concurrency_limiting_backoff_events_total counter
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher"} 1
# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit1"} {testLimit1}
gitaly_concurrency_limiting_current_limit{limit="testLimit2"} {testLimit2}
# HELP gitaly_concurrency_limiting_watcher_errors_total Counter of the total number of watcher errors
# TYPE gitaly_concurrency_limiting_watcher_errors_total counter
gitaly_concurrency_limiting_watcher_errors_total{watcher="testWatcher"} 5

`,
			},
			{
				desc:       "a watcher returns 6 consecutive timeout errors",
				waitEvents: 7,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit1", 25, 100, 10, 0.5),
					newTestLimit("testLimit2", 15, 30, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher(
						"testWatcher",
						[]string{"", "", "", "", "", "", ""},
						[]error{context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, nil},
					),
				},
				expectedLimits: map[string][]int{
					// The one next to the last triggers an event, but the last timeout does not trigger one.
					"testLimit1": {25, 26, 27, 28, 29, 14, 15, 16},
					"testLimit2": {15, 16, 17, 18, 19, 10, 11, 12},
				},
				expectedLogs: []string{
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
					`level=info msg="Multiplicative decrease" limit=testLimit1 new_limit=14 previous_limit=29 reason="5 consecutive polling timeout errors" watcher=testWatcher`,
					`level=info msg="Multiplicative decrease" limit=testLimit2 new_limit=10 previous_limit=19 reason="5 consecutive polling timeout errors" watcher=testWatcher`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher`,
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_backoff_events_total Counter of the total number of backoff events
# TYPE gitaly_concurrency_limiting_backoff_events_total counter
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher"} 1
# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit1"} {testLimit1}
gitaly_concurrency_limiting_current_limit{limit="testLimit2"} {testLimit2}
# HELP gitaly_concurrency_limiting_watcher_errors_total Counter of the total number of watcher errors
# TYPE gitaly_concurrency_limiting_watcher_errors_total counter
gitaly_concurrency_limiting_watcher_errors_total{watcher="testWatcher"} 6

`,
			},
			{
				desc:       "multiple watchers returns 5 consecutive timeout errors",
				waitEvents: 6,
				limits: []AdaptiveLimiter{
					newTestLimit("testLimit1", 25, 100, 10, 0.5),
					newTestLimit("testLimit2", 15, 30, 10, 0.5),
				},
				watchers: []ResourceWatcher{
					newTestWatcher(
						"testWatcher1",
						[]string{"", "", "", "", "", ""},
						[]error{context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, nil},
					),
					newTestWatcher(
						"testWatcher2",
						[]string{"", "", "", "", "", ""},
						[]error{context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, context.DeadlineExceeded, nil},
					),
				},
				expectedLimits: map[string][]int{
					"testLimit1": {25, 26, 27, 28, 29, 14, 15},
					"testLimit2": {15, 16, 17, 18, 19, 10, 11},
				},
				expectedLogs: []string{
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher1`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher2`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher1`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher2`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher1`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher2`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher1`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher2`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher1`,
					`level=error msg="poll from resource watcher failed" error="context deadline exceeded" watcher=testWatcher2`,
					`level=info msg="Multiplicative decrease" limit=testLimit1 new_limit=14 previous_limit=29 reason="5 consecutive polling timeout errors" watcher=testWatcher2`,
					`level=info msg="Multiplicative decrease" limit=testLimit2 new_limit=10 previous_limit=19 reason="5 consecutive polling timeout errors" watcher=testWatcher2`,
				},
				expectedMetrics: `# HELP gitaly_concurrency_limiting_backoff_events_total Counter of the total number of backoff events
# TYPE gitaly_concurrency_limiting_backoff_events_total counter
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher1"} 1
gitaly_concurrency_limiting_backoff_events_total{watcher="testWatcher2"} 1
# HELP gitaly_concurrency_limiting_current_limit The current limit value of an adaptive concurrency limit
# TYPE gitaly_concurrency_limiting_current_limit gauge
gitaly_concurrency_limiting_current_limit{limit="testLimit1"} {testLimit1}
gitaly_concurrency_limiting_current_limit{limit="testLimit2"} {testLimit2}
# HELP gitaly_concurrency_limiting_watcher_errors_total Counter of the total number of watcher errors
# TYPE gitaly_concurrency_limiting_watcher_errors_total counter
gitaly_concurrency_limiting_watcher_errors_total{watcher="testWatcher1"} 5
gitaly_concurrency_limiting_watcher_errors_total{watcher="testWatcher2"} 5

`,
			},
		}
		for _, tc := range tests {
			t.Run(tc.desc, func(t *testing.T) {
				logger := testhelper.NewLogger(t)
				hook := testhelper.AddLoggerHook(logger)

				tickerDone := make(chan struct{})
				ticker := helper.NewCountTicker(tc.waitEvents, func() {
					close(tickerDone)
				})

				// This test setup uses a manual ticker. This calibration duration is irrelevant to the actual
				// calibration cycle of the calculation. However, the calculator uses this value to determine a
				// timeout when polling events from the watchers. Thus, we need to pass an unrealistically high
				// value. Otherwise, the tests might be flaky when running on slow machines.
				calibration := 1 * time.Hour
				calculator := NewAdaptiveCalculator(calibration, logger, tc.limits, tc.watchers)
				calculator.tickerCreator = func(duration time.Duration) helper.Ticker { return ticker }

				stop, err := calculator.Start(ctx)
				require.NoError(t, err)

				<-tickerDone
				for _, limit := range tc.limits {
					limit.(*testLimit).waitForEvents(tc.waitEvents)
				}

				stop()

				for name, expectedLimits := range tc.expectedLimits {
					limit := findLimitWithName(tc.limits, name)
					require.NotNil(t, limit, "not found limit with name %q", name)
					require.Equal(t, expectedLimits, limit.currents[:tc.waitEvents+1])
				}

				// Replace the current limit in the metrics. The above test setup adds some time buffer. There
				// might be some extra calibrations after the test finishes.
				metrics := tc.expectedMetrics
				for _, l := range tc.limits {
					metrics = strings.Replace(metrics, fmt.Sprintf("{%s}", l.Name()), fmt.Sprintf("%d", l.Current()), -1)
				}
				assertLogs(t, tc.expectedLogs, hook.AllEntries())
				require.NoError(t, testutil.CollectAndCompare(calculator, strings.NewReader(metrics),
					"gitaly_concurrency_limiting_current_limit",
					"gitaly_concurrency_limiting_backoff_events_total",
					"gitaly_concurrency_limiting_watcher_errors_total",
				))
			})
		}
	})
}

func assertLogs(t *testing.T, expectedLogs []string, entries []*logrus.Entry) {
	require.Equal(t, len(expectedLogs), len(entries))
	for index, expectedLog := range expectedLogs {
		msg, err := entries[index].String()
		require.NoError(t, err)
		require.Contains(t, msg, expectedLog)
	}
}

func findLimitWithName(limits []AdaptiveLimiter, name string) *testLimit {
	for _, l := range limits {
		limit := l.(*testLimit)
		if limit.name == name {
			return limit
		}
	}
	return nil
}

type testLimit struct {
	sync.Mutex

	currents       []int
	name           string
	initial        int
	max            int
	min            int
	backoffBackoff float64
}

func newTestLimit(name string, initial int, max int, min int, backoff float64) *testLimit {
	return &testLimit{name: name, initial: initial, max: max, min: min, backoffBackoff: backoff}
}

func (l *testLimit) Name() string { return l.name }
func (l *testLimit) Current() int {
	l.Lock()
	defer l.Unlock()

	if len(l.currents) == 0 {
		return 0
	}
	return l.currents[len(l.currents)-1]
}

func (l *testLimit) waitForEvents(n int) {
	for {
		l.Lock()
		if len(l.currents) >= n {
			l.Unlock()
			return
		}
		l.Unlock()

		// Tiny sleep to prevent CPU exhaustion
		time.Sleep(1 * time.Millisecond)
	}
}

func (l *testLimit) Update(val int) {
	l.Lock()
	defer l.Unlock()

	l.currents = append(l.currents, val)
}

func (*testLimit) AfterUpdate(_ AfterUpdateHook) {}

func (l *testLimit) Setting() AdaptiveSetting {
	return AdaptiveSetting{
		Initial:       l.initial,
		Max:           l.max,
		Min:           l.min,
		BackoffFactor: l.backoffBackoff,
	}
}

type testWatcher struct {
	name   string
	events []*BackoffEvent
	errors []error
	index  int
}

func (w *testWatcher) Name() string {
	return w.name
}

func (w *testWatcher) Poll(context.Context) (*BackoffEvent, error) {
	index := w.index
	if index >= len(w.events) {
		index = len(w.events) - 1
	}
	var err error
	if w.errors != nil {
		err = w.errors[index]
	}
	w.index++
	return w.events[index], err
}

func newTestWatcher(name string, reasons []string, errors []error) *testWatcher {
	var events []*BackoffEvent
	for _, reason := range reasons {
		event := &BackoffEvent{
			ShouldBackoff: reason != "",
			Reason:        reason,
			WatcherName:   name,
			Stats: map[string]any{
				"current":   5678,
				"threshold": 1234,
			},
		}
		events = append(events, event)
	}
	return &testWatcher{name: name, events: events, errors: errors}
}
