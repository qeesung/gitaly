package trace2hooks

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/trace2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"golang.org/x/time/rate"
)

func TestLogExporter_Handle(t *testing.T) {
	current, err := time.Parse("2006-01-02T15:04:05Z", "2023-01-01T00:00:00Z")
	require.NoError(t, err)
	endTime := current.Add(7 * time.Second)
	exampleTrace := createExampleTrace(current)

	for _, tc := range []struct {
		desc          string
		setup         func(*testing.T) (context.Context, *trace2.Trace)
		expectedTrace trace2.Trace
	}{
		{
			desc: "receives trace consisting of root only",
			setup: func(t *testing.T) (context.Context, *trace2.Trace) {
				ctx := testhelper.Context(t)
				return ctx, &trace2.Trace{
					Thread:     "main",
					Name:       "root",
					StartTime:  current,
					FinishTime: endTime,
				}
			},
			expectedTrace: trace2.Trace{
				Thread:     "main",
				Name:       "root",
				StartTime:  current,
				FinishTime: endTime,
				Metadata:   map[string]string{"elapsed_ms": "7000"},
			},
		},
		{
			desc: "receives a complete trace",
			setup: func(t *testing.T) (context.Context, *trace2.Trace) {
				ctx := testhelper.Context(t)
				return ctx, exampleTrace
			},
			expectedTrace: trace2.Trace{
				Thread:     "main",
				Name:       "root",
				StartTime:  current,
				FinishTime: endTime,
				Metadata:   map[string]string{"elapsed_ms": "7000"},
				Children: []*trace2.Trace{
					{
						Thread:     "main",
						Name:       "version",
						StartTime:  current,
						FinishTime: current.Add(1 * time.Second), Metadata: map[string]string{"exe": "2.42.0", "elapsed_ms": "1000"},
						Depth: 1,
					}, {
						Thread:     "main",
						Name:       "start",
						StartTime:  current.Add(1 * time.Second),
						FinishTime: current.Add(2 * time.Second),
						Metadata:   map[string]string{"argv": "git fetch origin master", "elapsed_ms": "1000"},
						Depth:      1,
					}, {
						Thread:     "main",
						Name:       "def_repo",
						StartTime:  current.Add(2 * time.Second),
						FinishTime: current.Add(3 * time.Second),
						Metadata:   map[string]string{"worktree": "/Users/userx123/Documents/gitlab-development-kit", "elapsed_ms": "1000"},
						Depth:      1,
					}, {
						Thread:     "main",
						Name:       "index:do_read_index",
						StartTime:  current.Add(3 * time.Second),
						FinishTime: current.Add(6 * time.Second),
						Metadata:   map[string]string{"elapsed_ms": "3000"},
						Depth:      1,
						Children: []*trace2.Trace{
							{
								Thread:     "main",
								ChildID:    "0",
								Name:       "cache_tree:read",
								StartTime:  current.Add(3 * time.Second),
								FinishTime: current.Add(4 * time.Second),
								Metadata:   map[string]string{"elapsed_ms": "1000"},
								Depth:      2,
							},
							{
								Thread:     "main",
								ChildID:    "0",
								Name:       "data:index:read/version",
								StartTime:  current.Add(4 * time.Second),
								FinishTime: current.Add(5 * time.Second),
								Metadata:   map[string]string{"data": "2", "elapsed_ms": "1000"},
								Depth:      2,
							},
							{
								Thread:     "main",
								ChildID:    "0",
								Name:       "data:index:read/cache_nr",
								StartTime:  current.Add(5 * time.Second),
								FinishTime: current.Add(6 * time.Second),
								Metadata: map[string]string{
									"elapsed_ms": "1000",
									"data":       "1500",
								},
								Depth: 2,
							},
						},
					}, {
						Thread:     "main",
						Name:       "submodule:parallel/fetch",
						StartTime:  current.Add(6 * time.Second),
						FinishTime: current.Add(7 * time.Second),
						Metadata:   map[string]string{"elapsed_ms": "1000"},
						Depth:      1,
					},
				},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, trace := tc.setup(t)
			logger := testhelper.NewLogger(t)
			hook := testhelper.AddLoggerHook(logger)
			exporter := NewLogExporter(rate.NewLimiter(1, 1), logger)
			// execute and assertions
			err := exporter.Handle(ctx, trace)
			require.NoError(t, err)
			logEntry := hook.LastEntry()

			assert.Equal(t, tc.expectedTrace.Thread, logEntry.Data["thread"])
			assert.Equal(t, tc.expectedTrace.Name, logEntry.Data["name"])
			assert.Equal(t, tc.expectedTrace.Metadata, logEntry.Data["metadata"])
			assert.Equal(t, tc.expectedTrace.StartTime, logEntry.Data["start_time"])
			assert.Equal(t, tc.expectedTrace.FinishTime, logEntry.Data["finish_time"])
			var children []*trace2.Trace
			childErr := json.Unmarshal(logEntry.Data["children"].(json.RawMessage), &children)
			require.NoError(t, childErr)
			assert.Equal(t, tc.expectedTrace.Children, children)
		})
	}
}

func TestLogExporter_rateLimitFailureMock(t *testing.T) {
	errorMsg := "rate has exceeded current limit"

	for _, tc := range []struct {
		desc          string
		setup         func(*testing.T) (context.Context, *trace2.Trace)
		expectedError *regexp.Regexp
	}{
		{
			desc: "rate limit exceeded",
			setup: func(t *testing.T) (context.Context, *trace2.Trace) {
				ctx := testhelper.Context(t)
				return ctx, &trace2.Trace{
					Thread:     "main",
					Name:       "root",
					StartTime:  time.Time{},
					FinishTime: time.Time{}.Add(1 * time.Second),
				}
			},
			expectedError: regexp.MustCompile(errorMsg),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, trace := tc.setup(t)
			logger := testhelper.SharedLogger(t)

			rl := rate.NewLimiter(0, 1) // burst limit of 1, refreshing at 0 rps

			exporter := LogExporter{rateLimiter: rl, logger: logger}
			err := exporter.Handle(ctx, trace)
			require.NoError(t, err)
			err = exporter.Handle(ctx, trace)
			require.Error(t, err)
			require.Regexp(t, tc.expectedError, err)
		})
	}
}
