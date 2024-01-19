package trace2hooks

import (
	"context"
	"encoding/json"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/trace2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"golang.org/x/time/rate"
)

// NewLogExporter initializes LogExporter, which is a hook that uses the parsed
// trace2 events from the manager to export them to the Gitaly log. It's invocations are limited
// by the rateLimiter. The limiter allows maxBurstToken number of events to happen at once and then
// replenishes by maxEventPerSecond. It works on the token bucket algorithm where you have a number
// of tokens in the bucket to start and you can consume them in each call whilst the bucket gets
// refilled at the specified rate.
func NewLogExporter(rl *rate.Limiter, logger log.Logger) *LogExporter {
	return &LogExporter{
		rateLimiter: rl,
		logger:      logger,
	}
}

// LogExporter is a trace2 hook that adds trace2 api event logs to Gitaly's logs.
type LogExporter struct {
	logger      log.Logger
	rateLimiter *rate.Limiter
}

// Name returns the name of tracing exporter
func (t *LogExporter) Name() string {
	return "log_exporter"
}

// Handle will log the trace in a readable json format in Gitaly's logs. Metadata is also collected
// and additional information is added to the log. It is also rate limited to protect it from overload
// when there are a lot of trace2 events triggered from git operations.
func (t *LogExporter) Handle(rootCtx context.Context, trace *trace2.Trace) error {
	if !t.rateLimiter.Allow() {
		// When the event is not allowed, return an error to the caller, this may cause traces to be skipped/dropped.
		return fmt.Errorf("rate has exceeded current limit")
	}

	trace.Walk(rootCtx, func(ctx context.Context, t *trace2.Trace) context.Context {
		t.SetMetadata("elapsed_ms", fmt.Sprintf("%d", t.FinishTime.Sub(t.StartTime).Milliseconds()))

		return ctx
	})

	childrenJSON, err := json.Marshal(trace.Children)
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	escapedChildren := json.RawMessage(childrenJSON)

	t.logger.WithFields(log.Fields{
		"name":        trace.Name,
		"thread":      trace.Thread,
		"component":   "trace2hooks." + t.Name(),
		"start_time":  trace.StartTime,
		"finish_time": trace.FinishTime,
		"metadata":    trace.Metadata,
		"children":    escapedChildren,
	}).Info("Git Trace2 API")
	return nil
}
