package trace2hooks

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/trace2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tracing"
)

func TestTracingExporter_Handle(t *testing.T) {
	reporter, cleanup := testhelper.StubTracingReporter(t)
	defer cleanup()

	// Pin a timestamp for trace tree generation below. This way enables asserting the time
	// frames of spans correctly.
	current, err := time.Parse("2006-01-02T15:04:05Z", "2023-01-01T00:00:00Z")
	require.NoError(t, err)

	exampleTrace := createExampleTrace(current)

	for _, tc := range []struct {
		desc          string
		setup         func(*testing.T) (context.Context, *trace2.Trace)
		expectedSpans []*testhelper.Span
	}{
		{
			desc: "empty trace",
			setup: func(t *testing.T) (context.Context, *trace2.Trace) {
				_, ctx := tracing.StartSpan(testhelper.Context(t), "root", nil)
				return ctx, nil
			},
			expectedSpans: nil,
		},
		{
			desc: "receives trace consisting of root only",
			setup: func(t *testing.T) (context.Context, *trace2.Trace) {
				_, ctx := tracing.StartSpan(testhelper.Context(t), "root", nil)
				return ctx, &trace2.Trace{
					Thread:     "main",
					Name:       "root",
					StartTime:  current,
					FinishTime: time.Time{},
				}
			},
			expectedSpans: nil,
		},
		{
			desc: "receives a complete trace",
			setup: func(t *testing.T) (context.Context, *trace2.Trace) {
				_, ctx := tracing.StartSpan(testhelper.Context(t), "root", nil)

				return ctx, exampleTrace
			},
			expectedSpans: []*testhelper.Span{
				{
					Operation: "git:version",
					StartTime: current,
					Duration:  1 * time.Second,
					Tags: map[string]string{
						"childID": "",
						"thread":  "main",
						"exe":     "2.42.0",
					},
				},
				{
					Operation: "git:start",
					StartTime: current.Add(1 * time.Second),
					Duration:  1 * time.Second,
					Tags: map[string]string{
						"argv":    "git fetch origin master",
						"childID": "",
						"thread":  "main",
					},
				},
				{
					Operation: "git:def_repo",
					StartTime: current.Add(2 * time.Second),
					Duration:  1 * time.Second,
					Tags: map[string]string{
						"childID":  "",
						"thread":   "main",
						"worktree": "/Users/userx123/Documents/gitlab-development-kit",
					},
				},
				{
					Operation: "git:index:do_read_index",
					StartTime: current.Add(3 * time.Second),
					Duration:  3 * time.Second, // 3 children
					Tags: map[string]string{
						"childID": "",
						"thread":  "main",
					},
				},
				{
					Operation: "git:cache_tree:read",
					StartTime: current.Add(3 * time.Second),
					Duration:  1 * time.Second, // 3 children
					Tags: map[string]string{
						"childID": "0",
						"thread":  "main",
					},
				},
				{
					Operation: "git:data:index:read/version",
					StartTime: current.Add(4 * time.Second),
					Duration:  1 * time.Second, // 3 children
					Tags: map[string]string{
						"childID": "0",
						"thread":  "main",
						"data":    "2",
					},
				},
				{
					Operation: "git:data:index:read/cache_nr",
					StartTime: current.Add(5 * time.Second),
					Duration:  1 * time.Second, // 3 children
					Tags: map[string]string{
						"childID": "0",
						"thread":  "main",
						"data":    "1500",
					},
				},
				{
					Operation: "git:submodule:parallel/fetch",
					StartTime: current.Add(6 * time.Second),
					Duration:  1 * time.Second, // 3 children
					Tags: map[string]string{
						"childID": "",
						"thread":  "main",
					},
				},
			},
		},
		{
			desc: "receives a complete trace but tracing is not initialized",
			setup: func(t *testing.T) (context.Context, *trace2.Trace) {
				return testhelper.Context(t), exampleTrace
			},
			expectedSpans: nil,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			reporter.Reset()
			ctx, trace := tc.setup(t)

			exporter := NewTracingExporter()
			err := exporter.Handle(ctx, trace)
			require.NoError(t, err)

			spans := testhelper.ReportedSpans(t, reporter)
			require.Equal(t, tc.expectedSpans, spans)
		})
	}
}
