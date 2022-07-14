package hook

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestConcurrencyTracker(t *testing.T) {
	testCases := []struct {
		desc            string
		calls           func(ctx context.Context, c *ConcurrencyTracker)
		expectedLogData []logrus.Fields
	}{
		{
			desc: "single call",
			calls: func(ctx context.Context, c *ConcurrencyTracker) {
				finish := c.LogConcurrency(ctx, "repository", "a/b/c")
				defer finish()
				finish = c.LogConcurrency(ctx, "user_id", "user-123")
				defer finish()
			},
			expectedLogData: []logrus.Fields{
				{
					"concurrency_type": "repository",
					"concurrency_key":  "a/b/c",
					"concurrency":      1,
				},
				{
					"concurrency_type": "user_id",
					"concurrency_key":  "user-123",
					"concurrency":      1,
				},
			},
		},
		{
			desc: "multiple calls",
			calls: func(ctx context.Context, c *ConcurrencyTracker) {
				finish := c.LogConcurrency(ctx, "repository", "a/b/c")
				defer finish()
				finish = c.LogConcurrency(ctx, "repository", "a/b/c")
				defer finish()
				finish = c.LogConcurrency(ctx, "repository", "a/b/c")
				defer finish()
				finish = c.LogConcurrency(ctx, "user_id", "user-123")
				defer finish()
				finish = c.LogConcurrency(ctx, "user_id", "user-123")
				defer finish()
				finish = c.LogConcurrency(ctx, "user_id", "user-123")
				defer finish()
			},
			expectedLogData: []logrus.Fields{
				{
					"concurrency_type": "repository",
					"concurrency_key":  "a/b/c",
					"concurrency":      1,
				},
				{
					"concurrency_type": "repository",
					"concurrency_key":  "a/b/c",
					"concurrency":      2,
				},
				{
					"concurrency_type": "repository",
					"concurrency_key":  "a/b/c",
					"concurrency":      3,
				},
				{
					"concurrency_type": "user_id",
					"concurrency_key":  "user-123",
					"concurrency":      1,
				},
				{
					"concurrency_type": "user_id",
					"concurrency_key":  "user-123",
					"concurrency":      2,
				},
				{
					"concurrency_type": "user_id",
					"concurrency_key":  "user-123",
					"concurrency":      3,
				},
			},
		},
		{
			desc: "multiple finished calls",
			calls: func(ctx context.Context, c *ConcurrencyTracker) {
				finish := c.LogConcurrency(ctx, "repository", "a/b/c")
				finish()
				finish = c.LogConcurrency(ctx, "repository", "a/b/c")
				finish()
				finish = c.LogConcurrency(ctx, "repository", "a/b/c")
				finish()
				finish = c.LogConcurrency(ctx, "user_id", "user-123")
				finish()
				finish = c.LogConcurrency(ctx, "user_id", "user-123")
				finish()
				finish = c.LogConcurrency(ctx, "user_id", "user-123")
				finish()
			},
			expectedLogData: []logrus.Fields{
				{
					"concurrency_type": "repository",
					"concurrency_key":  "a/b/c",
					"concurrency":      1,
				},
				{
					"concurrency_type": "repository",
					"concurrency_key":  "a/b/c",
					"concurrency":      1,
				},
				{
					"concurrency_type": "repository",
					"concurrency_key":  "a/b/c",
					"concurrency":      1,
				},
				{
					"concurrency_type": "user_id",
					"concurrency_key":  "user-123",
					"concurrency":      1,
				},
				{
					"concurrency_type": "user_id",
					"concurrency_key":  "user-123",
					"concurrency":      1,
				},
				{
					"concurrency_type": "user_id",
					"concurrency_key":  "user-123",
					"concurrency":      1,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			l, hook := test.NewNullLogger()

			ctx = ctxlogrus.ToContext(ctx, logrus.NewEntry(l))

			c := NewConcurrencyTracker()

			tc.calls(ctx, c)

			require.Len(t, hook.Entries, len(tc.expectedLogData))
			for i := 0; i < len(hook.Entries); i++ {
				assert.Equal(t, tc.expectedLogData[i], hook.Entries[i].Data)
				assert.Equal(t, "concurrency", hook.Entries[i].Message)
			}

			assert.Len(t, c.concurrencyMap, 0)
		})
	}
}

func TestConcurrencyTrackerConcurrentCalls(t *testing.T) {
	ctx := testhelper.Context(t)

	l, hook := test.NewNullLogger()

	ctx = ctxlogrus.ToContext(ctx, logrus.NewEntry(l))

	c := NewConcurrencyTracker()

	var wg sync.WaitGroup
	wg.Add(3)

	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			finish := c.LogConcurrency(ctx, "repository", "a/b/c")
			defer finish()
		}()
	}

	wg.Wait()

	require.Len(t, hook.Entries, 3)

	for i := 0; i < len(hook.Entries); i++ {
		assert.Equal(t, "a/b/c", hook.Entries[i].Data["concurrency_key"])
		assert.Equal(t, "repository", hook.Entries[i].Data["concurrency_type"])
		assert.Equal(t, "concurrency", hook.Entries[i].Message)
	}

	assert.Len(t, c.concurrencyMap, 0)
}

func TestConcurrencyTracker_metrics(t *testing.T) {
	ctx := testhelper.Context(t)

	c := NewConcurrencyTracker()

	finish := c.LogConcurrency(ctx, "repository", "a")
	finish()
	c.LogConcurrency(ctx, "repository", "a")
	c.LogConcurrency(ctx, "repository", "b")
	c.LogConcurrency(ctx, "repository", "c")

	finish = c.LogConcurrency(ctx, "user_id", "user-1")
	finish()
	c.LogConcurrency(ctx, "user_id", "user-1")
	c.LogConcurrency(ctx, "user_id", "user-2")
	c.LogConcurrency(ctx, "user_id", "user-3")
	c.LogConcurrency(ctx, "user_id", "user-4")

	expectedMetrics := `# HELP gitaly_pack_objects_concurrent_processes Number of concurrent processes
# TYPE gitaly_pack_objects_concurrent_processes histogram
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="0"} 0
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="5"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="10"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="15"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="20"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="25"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="30"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="35"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="40"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="45"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="50"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="55"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="60"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="65"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="70"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="75"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="80"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="85"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="90"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="95"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="repository",le="+Inf"} 4
gitaly_pack_objects_concurrent_processes_sum{segment="repository"} 4
gitaly_pack_objects_concurrent_processes_count{segment="repository"} 4
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="0"} 0
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="5"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="10"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="15"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="20"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="25"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="30"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="35"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="40"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="45"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="50"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="55"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="60"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="65"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="70"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="75"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="80"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="85"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="90"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="95"} 5
gitaly_pack_objects_concurrent_processes_bucket{segment="user_id",le="+Inf"} 5
gitaly_pack_objects_concurrent_processes_sum{segment="user_id"} 5
gitaly_pack_objects_concurrent_processes_count{segment="user_id"} 5
# HELP gitaly_pack_objects_process_active_callers Number of unique callers that have an active pack objects processes
# TYPE gitaly_pack_objects_process_active_callers gauge
gitaly_pack_objects_process_active_callers{segment="repository"} 3
gitaly_pack_objects_process_active_callers{segment="user_id"} 4
# HELP gitaly_pack_objects_process_active_callers_total Total unique callers that have initiated a pack objects processes
# TYPE gitaly_pack_objects_process_active_callers_total counter
gitaly_pack_objects_process_active_callers_total{segment="repository"} 4
gitaly_pack_objects_process_active_callers_total{segment="user_id"} 5
`
	require.NoError(t, testutil.CollectAndCompare(
		c,
		bytes.NewBufferString(expectedMetrics),
	))
}
