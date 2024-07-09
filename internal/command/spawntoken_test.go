package command

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/durationpb"
)

// TestNewSpawnTokenManagerFromEnv mocks ENV variables, thus cannot run in parallel.
func TestNewSpawnTokenManagerFromEnv(t *testing.T) {
	for _, tc := range []struct {
		desc           string
		envs           map[string]string
		expectedErr    error
		expectedConfig SpawnConfig
	}{
		{
			desc: "spawn token ENVs are not set",
			expectedConfig: SpawnConfig{
				Timeout:     10 * time.Second,
				MaxParallel: 10,
			},
		},
		{
			desc: "spawn token ENVs are set correctly",
			envs: map[string]string{
				"GITALY_COMMAND_SPAWN_MAX_PARALLEL": "100",
				"GITALY_COMMAND_SPAWN_TIMEOUT":      "99s",
			},
			expectedConfig: SpawnConfig{
				Timeout:     99 * time.Second,
				MaxParallel: 100,
			},
		},
		{
			desc: "spawn token ENVs are set incorrectly",
			envs: map[string]string{
				"GITALY_COMMAND_SPAWN_MAX_PARALLEL": "100",
				"GITALY_COMMAND_SPAWN_TIMEOUT":      "hello",
			},
			expectedErr: &envconfig.ParseError{
				KeyName:   "GITALY_COMMAND_SPAWN_TIMEOUT",
				FieldName: "Timeout",
				TypeName:  "time.Duration",
				Value:     "hello",
				Err:       fmt.Errorf(`time: invalid duration "hello"`),
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			for key, value := range tc.envs {
				t.Setenv(key, value)
			}
			manager, err := NewSpawnTokenManagerFromEnv()
			if tc.expectedErr != nil {
				require.Equal(t, tc.expectedErr, err)
				require.Nil(t, manager)
			} else {
				require.NoError(t, err)
				require.NotNil(t, manager)
				require.Equal(t, manager.spawnConfig, tc.expectedConfig)
			}
		})
	}
}

func TestGetSpawnToken_CommandStats(t *testing.T) {
	t.Parallel()

	ctx := log.InitContextCustomFields(testhelper.Context(t))
	manager := NewSpawnTokenManager(SpawnConfig{
		Timeout:     time.Hour,
		MaxParallel: 1,
	})

	putFirstToken, err := manager.GetSpawnToken(ctx)
	require.NoError(t, err)

	waitSecondStarted := make(chan struct{})
	waitSecondAcquire := make(chan struct{})
	waitSecondPut := make(chan struct{})
	go func() {
		close(waitSecondStarted)
		putSecondToken, err := manager.GetSpawnToken(ctx)
		require.NoError(t, err)
		close(waitSecondAcquire)
		putSecondToken()
		<-waitSecondPut
	}()

	<-waitSecondStarted
	expected := strings.NewReader(`
# HELP gitaly_spawn_token_waiting_length The current length of the queue waiting for spawn tokens
# TYPE gitaly_spawn_token_waiting_length gauge
gitaly_spawn_token_waiting_length 1

	`)
	// Even after the second goroutine starts, there is a small possibility for the metric collection to run before
	// the second goroutine lines up for the token. There is no way to get a precise signal when the second
	// goroutine is waiting. Hence, the only solution is to poll for the metric until it changes. If something goes
	// wrong with the metrics or the second goroutine never starts (unlikely) or the metric never changes, the loop
	// exits with a timeout error. We expect the loop exits immediately in most cases.
	for {
		if err := testutil.CollectAndCompare(manager, expected, "gitaly_spawn_token_waiting_length"); err == nil {
			break
		}

		runtime.Gosched()
	}

	putFirstToken()
	// Wait for the goroutine to acquire the token
	<-waitSecondAcquire

	// As the second goroutine finishes waiting, the queue length goes back to 0
	expected = strings.NewReader(`
# HELP gitaly_spawn_token_waiting_length The current length of the queue waiting for spawn tokens
# TYPE gitaly_spawn_token_waiting_length gauge
gitaly_spawn_token_waiting_length 0

`)
	require.NoError(t, testutil.CollectAndCompare(manager, expected,
		"gitaly_spawn_token_waiting_length",
	))

	// Wait until for the second gorutine to put back the token
	close(waitSecondPut)
	expected = strings.NewReader(`
# HELP gitaly_spawn_forking_time_seconds Histogram of time waiting for spawn tokens
# TYPE gitaly_spawn_forking_time_seconds histogram
gitaly_spawn_forking_time_seconds_count 2
# HELP gitaly_spawn_timeouts_total Number of process spawn timeouts
# TYPE gitaly_spawn_timeouts_total counter
gitaly_spawn_timeouts_total 0
# HELP gitaly_spawn_waiting_time_seconds Histogram of time waiting for spawn tokens
# TYPE gitaly_spawn_waiting_time_seconds histogram
gitaly_spawn_waiting_time_seconds_count 2
# HELP gitaly_spawn_token_waiting_length The current length of the queue waiting for spawn tokens
# TYPE gitaly_spawn_token_waiting_length gauge
gitaly_spawn_token_waiting_length 0

`)
	require.NoError(t, testutil.CollectAndCompare(manager, expected,
		"gitaly_spawn_timeouts_total",
		"gitaly_spawn_forking_time_seconds_count",
		"gitaly_spawn_waiting_time_seconds_count",
		"gitaly_spawn_token_waiting_length",
	))

	customFields := log.CustomFieldsFromContext(ctx)
	require.NotNil(t, customFields)
	logrusFields := customFields.Fields()

	require.Contains(t, logrusFields, "command.spawn_token_wait_ms")
	require.Contains(t, logrusFields, "command.spawn_token_fork_ms")
}

func TestGetSpawnToken_CommandStats_timeout(t *testing.T) {
	t.Parallel()

	ctx := log.InitContextCustomFields(testhelper.Context(t))
	manager := NewSpawnTokenManager(SpawnConfig{
		Timeout: 1 * time.Millisecond,
	})
	_, err := manager.GetSpawnToken(ctx)

	var structErr structerr.Error
	require.ErrorAs(t, err, &structErr)
	details := structErr.Details()
	require.Len(t, details, 1)

	limitErr, ok := details[0].(*gitalypb.LimitError)
	require.True(t, ok)

	testhelper.RequireGrpcCode(t, err, codes.ResourceExhausted)
	require.Equal(t, "process spawn timed out after 1ms", limitErr.ErrorMessage)
	require.Equal(t, durationpb.New(0).AsDuration(), limitErr.RetryAfter.AsDuration())

	customFields := log.CustomFieldsFromContext(ctx)
	require.NotNil(t, customFields)
	logrusFields := customFields.Fields()

	require.GreaterOrEqual(t, logrusFields["command.spawn_token_wait_ms"], 0)
	require.NotContains(t, logrusFields, "command.spawn_token_fork_ms")
	require.Equal(t, logrusFields["command.spawn_token_error"], "spawn token timeout")

	expected := strings.NewReader(`
# HELP gitaly_spawn_forking_time_seconds Histogram of time waiting for spawn tokens
# TYPE gitaly_spawn_forking_time_seconds histogram
gitaly_spawn_forking_time_seconds_count 1
# HELP gitaly_spawn_timeouts_total Number of process spawn timeouts
# TYPE gitaly_spawn_timeouts_total counter
gitaly_spawn_timeouts_total 1
# HELP gitaly_spawn_waiting_time_seconds Histogram of time waiting for spawn tokens
# TYPE gitaly_spawn_waiting_time_seconds histogram
gitaly_spawn_waiting_time_seconds_count 1
# HELP gitaly_spawn_token_waiting_length The current length of the queue waiting for spawn tokens
# TYPE gitaly_spawn_token_waiting_length gauge
gitaly_spawn_token_waiting_length 0

`)
	require.NoError(t, testutil.CollectAndCompare(manager, expected,
		"gitaly_spawn_timeouts_total",
		"gitaly_spawn_forking_time_seconds_count",
		"gitaly_spawn_waiting_time_seconds_count",
		"gitaly_spawn_token_waiting_length",
	))
}
