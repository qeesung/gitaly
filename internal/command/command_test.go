package command

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	grpcmwtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestNew_environment(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	extraVar := "FOOBAR=123456"

	var buf bytes.Buffer
	cmd, err := New(ctx, exec.Command("/usr/bin/env"), WithStdout(&buf), WithEnvironment([]string{extraVar}))

	require.NoError(t, err)
	require.NoError(t, cmd.Wait())

	require.Contains(t, strings.Split(buf.String(), "\n"), extraVar)
}

func TestNew_exportedEnvironment(t *testing.T) {
	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		key   string
		value string
	}{
		{
			key:   "HOME",
			value: "/home/git",
		},
		{
			key:   "PATH",
			value: "/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin",
		},
		{
			key:   "LD_LIBRARY_PATH",
			value: "/path/to/your/lib",
		},
		{
			key:   "TZ",
			value: "foobar",
		},
		{
			key:   "GIT_TRACE",
			value: "true",
		},
		{
			key:   "GIT_TRACE_PACK_ACCESS",
			value: "true",
		},
		{
			key:   "GIT_TRACE_PACKET",
			value: "true",
		},
		{
			key:   "GIT_TRACE_PERFORMANCE",
			value: "true",
		},
		{
			key:   "GIT_TRACE_SETUP",
			value: "true",
		},
		{
			key:   "all_proxy",
			value: "http://localhost:4000",
		},
		{
			key:   "http_proxy",
			value: "http://localhost:5000",
		},
		{
			key:   "HTTP_PROXY",
			value: "http://localhost:6000",
		},
		{
			key:   "https_proxy",
			value: "https://localhost:5000",
		},
		{
			key:   "HTTPS_PROXY",
			value: "https://localhost:6000",
		},
		{
			key:   "no_proxy",
			value: "https://excluded:5000",
		},
		{
			key:   "NO_PROXY",
			value: "https://excluded:5000",
		},
	} {
		t.Run(tc.key, func(t *testing.T) {
			if tc.key == "LD_LIBRARY_PATH" && runtime.GOOS == "darwin" {
				t.Skip("System Integrity Protection prevents using dynamic linker (dyld) environment variables on macOS. https://apple.co/2XDH4iC")
			}

			t.Setenv(tc.key, tc.value)

			var buf bytes.Buffer
			cmd, err := New(ctx, exec.Command("/usr/bin/env"), WithStdout(&buf))
			require.NoError(t, err)
			require.NoError(t, cmd.Wait())

			expectedEnv := fmt.Sprintf("%s=%s", tc.key, tc.value)
			require.Contains(t, strings.Split(buf.String(), "\n"), expectedEnv)
		})
	}
}

func TestNew_unexportedEnv(t *testing.T) {
	ctx := testhelper.Context(t)

	unexportedEnvKey, unexportedEnvVal := "GITALY_UNEXPORTED_ENV", "foobar"
	t.Setenv(unexportedEnvKey, unexportedEnvVal)

	var buf bytes.Buffer
	cmd, err := New(ctx, exec.Command("/usr/bin/env"), WithStdout(&buf))
	require.NoError(t, err)
	require.NoError(t, cmd.Wait())

	require.NotContains(t, strings.Split(buf.String(), "\n"), fmt.Sprintf("%s=%s", unexportedEnvKey, unexportedEnvVal))
}

func TestNew_rejectContextWithoutDone(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t, contextWithoutDonePanic("command spawned with context without Done() channel"), func() {
		_, err := New(testhelper.ContextWithoutCancel(), exec.Command("true"))
		require.NoError(t, err)
	})
}

func TestNew_spawnTimeout(t *testing.T) {
	ctx := testhelper.Context(t)

	defer func(ch chan struct{}, t time.Duration) {
		spawnTokens = ch
		spawnConfig.Timeout = t
	}(spawnTokens, spawnConfig.Timeout)

	// This unbuffered channel will behave like a full/blocked buffered channel.
	spawnTokens = make(chan struct{})
	// Speed up the test by lowering the timeout
	spawnTimeout := 200 * time.Millisecond
	spawnConfig.Timeout = spawnTimeout

	tick := time.After(spawnTimeout / 2)

	errCh := make(chan error)
	go func() {
		_, err := New(ctx, exec.Command("true"))
		errCh <- err
	}()

	select {
	case <-errCh:
		require.FailNow(t, "expected spawning to be delayed")
	case <-tick:
		// This is the happy case: we expect spawning of the command to be delayed by up to
		// 200ms until it finally times out.
	}

	// And after some time we expect that spawning of the command fails due to the configured
	// timeout.
	require.Equal(t, fmt.Errorf("process spawn timed out after 200ms"), <-errCh)
}

func TestCommand_Wait_contextCancellationKillsCommand(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testhelper.Context(t))

	cmd, err := New(ctx, exec.CommandContext(ctx, "sleep", "1h"))
	require.NoError(t, err)

	// Cancel the command early.
	go cancel()

	err = cmd.Wait()
	require.Error(t, err)
	s, ok := ExitStatus(err)
	require.True(t, ok)
	require.Equal(t, -1, s)
}

func TestNew_setupStdin(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	stdin := "Test value"

	var buf bytes.Buffer
	cmd, err := New(ctx, exec.Command("cat"), WithSetupStdin(), WithStdout(&buf))
	require.NoError(t, err)

	_, err = fmt.Fprintf(cmd, "%s", stdin)
	require.NoError(t, err)

	require.NoError(t, cmd.Wait())
	require.Equal(t, stdin, buf.String())
}

func TestCommand_read(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cmd, err := New(ctx, exec.Command("echo", "test value"))
	require.NoError(t, err)

	output, err := io.ReadAll(cmd)
	require.NoError(t, err)
	require.Equal(t, "test value\n", string(output))

	require.NoError(t, cmd.Wait())
}

func TestNew_nulByteInArgument(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cmd, err := New(ctx, exec.Command("sh", "-c", "hello\x00world"))
	require.Equal(t, fmt.Errorf("detected null byte in command argument %q", "hello\x00world"), err)
	require.Nil(t, cmd)
}

func TestNew_missingBinary(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cmd, err := New(ctx, exec.Command("command-non-existent"))
	require.EqualError(t, err, "starting process [command-non-existent]: exec: \"command-non-existent\": executable file not found in $PATH")
	require.Nil(t, cmd)
}

func TestCommand_stderrLogging(t *testing.T) {
	t.Parallel()

	binaryPath := testhelper.WriteExecutable(t, filepath.Join(testhelper.TempDir(t), "script"), []byte(`#!/bin/bash
		for i in {1..5}
		do
			echo 'hello world' 1>&2
		done
		exit 1
	`))

	logger, hook := test.NewNullLogger()
	ctx := testhelper.Context(t)
	ctx = ctxlogrus.ToContext(ctx, logrus.NewEntry(logger))

	var stdout bytes.Buffer
	cmd, err := New(ctx, exec.Command(binaryPath), WithStdout(&stdout))
	require.NoError(t, err)

	require.EqualError(t, cmd.Wait(), "exit status 1")
	require.Empty(t, stdout.Bytes())
	require.Equal(t, strings.Repeat("hello world\n", 5), hook.LastEntry().Message)
}

func TestCommand_stderrLoggingTruncation(t *testing.T) {
	t.Parallel()

	binaryPath := testhelper.WriteExecutable(t, filepath.Join(testhelper.TempDir(t), "script"), []byte(`#!/bin/bash
		for i in {1..1000}
		do
			printf '%06d zzzzzzzzzz\n' $i >&2
		done
		exit 1
	`))

	logger, hook := test.NewNullLogger()
	ctx := testhelper.Context(t)
	ctx = ctxlogrus.ToContext(ctx, logrus.NewEntry(logger))

	var stdout bytes.Buffer
	cmd, err := New(ctx, exec.Command(binaryPath), WithStdout(&stdout))
	require.NoError(t, err)

	require.Error(t, cmd.Wait())
	require.Empty(t, stdout.Bytes())
	require.Len(t, hook.LastEntry().Message, maxStderrBytes)
}

func TestCommand_stderrLoggingWithNulBytes(t *testing.T) {
	t.Parallel()

	binaryPath := testhelper.WriteExecutable(t, filepath.Join(testhelper.TempDir(t), "script"), []byte(`#!/bin/bash
		dd if=/dev/zero bs=1000 count=1000 status=none >&2
		exit 1
	`))

	logger, hook := test.NewNullLogger()
	ctx := testhelper.Context(t)
	ctx = ctxlogrus.ToContext(ctx, logrus.NewEntry(logger))

	var stdout bytes.Buffer
	cmd, err := New(ctx, exec.Command(binaryPath), WithStdout(&stdout))
	require.NoError(t, err)

	require.Error(t, cmd.Wait())
	require.Empty(t, stdout.Bytes())
	require.Equal(t, strings.Repeat("\x00", maxStderrLineLength), hook.LastEntry().Message)
}

func TestCommand_stderrLoggingLongLine(t *testing.T) {
	t.Parallel()

	binaryPath := testhelper.WriteExecutable(t, filepath.Join(testhelper.TempDir(t), "script"), []byte(`#!/bin/bash
		printf 'a%.0s' {1..8192} >&2
		printf '\n' >&2
		printf 'b%.0s' {1..8192} >&2
		exit 1
	`))

	logger, hook := test.NewNullLogger()
	ctx := testhelper.Context(t)
	ctx = ctxlogrus.ToContext(ctx, logrus.NewEntry(logger))

	var stdout bytes.Buffer
	cmd, err := New(ctx, exec.Command(binaryPath), WithStdout(&stdout))
	require.NoError(t, err)

	require.Error(t, cmd.Wait())
	require.Empty(t, stdout.Bytes())
	require.Equal(t,
		strings.Join([]string{
			strings.Repeat("a", maxStderrLineLength),
			strings.Repeat("b", maxStderrLineLength),
		}, "\n"),
		hook.LastEntry().Message,
	)
}

func TestCommand_stderrLoggingMaxBytes(t *testing.T) {
	t.Parallel()

	binaryPath := testhelper.WriteExecutable(t, filepath.Join(testhelper.TempDir(t), "script"), []byte(`#!/bin/bash
		# This script is used to test that a command writes at most maxBytes to stderr. It
		# simulates the edge case where the logwriter has already written MaxStderrBytes-1
		# (9999) bytes

		# This edge case happens when 9999 bytes are written. To simulate this,
		# stderr_max_bytes_edge_case has 4 lines of the following format:
		#
		# line1: 3333 bytes long
		# line2: 3331 bytes
		# line3: 3331 bytes
		# line4: 1 byte
		#
		# The first 3 lines sum up to 9999 bytes written, since we write a 2-byte escaped
		# "\n" for each \n we see. The 4th line can be any data.

		printf 'a%.0s' {1..3333} >&2
		printf '\n' >&2
		printf 'a%.0s' {1..3331} >&2
		printf '\n' >&2
		printf 'a%.0s' {1..3331} >&2
		printf '\na\n' >&2
		exit 1
	`))

	logger, hook := test.NewNullLogger()
	ctx := testhelper.Context(t)
	ctx = ctxlogrus.ToContext(ctx, logrus.NewEntry(logger))

	var stdout bytes.Buffer
	cmd, err := New(ctx, exec.Command(binaryPath), WithStdout(&stdout))
	require.NoError(t, err)
	require.Error(t, cmd.Wait())

	require.Empty(t, stdout.Bytes())
	require.Len(t, hook.LastEntry().Message, maxStderrBytes)
}

type mockCgroupManager string

func (m mockCgroupManager) AddCommand(*Command, repository.GitRepo) (string, error) {
	return string(m), nil
}

func TestCommand_logMessage(t *testing.T) {
	t.Parallel()

	logger, hook := test.NewNullLogger()
	logger.SetLevel(logrus.DebugLevel)

	ctx := ctxlogrus.ToContext(testhelper.Context(t), logrus.NewEntry(logger))

	cmd, err := New(ctx, exec.Command("echo", "hello world"),
		WithCgroup(mockCgroupManager("/sys/fs/cgroup/1"), nil),
	)
	require.NoError(t, err)

	require.NoError(t, cmd.Wait())
	logEntry := hook.LastEntry()
	assert.Equal(t, cmd.Pid(), logEntry.Data["pid"])
	assert.Equal(t, []string{"echo", "hello world"}, logEntry.Data["args"])
	assert.Equal(t, 0, logEntry.Data["command.exitCode"])
	assert.Equal(t, "/sys/fs/cgroup/1", logEntry.Data["command.cgroup_path"])
}

func TestNew_commandSpawnTokenMetrics(t *testing.T) {
	defer func(old func(time.Time) float64) {
		getSpawnTokenAcquiringSeconds = old
	}(getSpawnTokenAcquiringSeconds)

	getSpawnTokenAcquiringSeconds = func(t time.Time) float64 {
		return 1
	}

	spawnTokenAcquiringSeconds.Reset()

	ctx := testhelper.Context(t)

	tags := grpcmwtags.NewTags()
	tags.Set("grpc.request.fullMethod", "/test.Service/TestRPC")
	ctx = grpcmwtags.SetInContext(ctx, tags)

	cmd, err := New(ctx, exec.Command("echo", "goodbye, cruel world."))

	require.NoError(t, err)
	require.NoError(t, cmd.Wait())

	expectedMetrics := `# HELP gitaly_command_spawn_token_acquiring_seconds_total Sum of time spent waiting for a spawn token
# TYPE gitaly_command_spawn_token_acquiring_seconds_total counter
gitaly_command_spawn_token_acquiring_seconds_total{cmd="echo",git_version="",grpc_method="TestRPC",grpc_service="test.Service"} 1
`
	require.NoError(
		t,
		testutil.CollectAndCompare(
			spawnTokenAcquiringSeconds,
			bytes.NewBufferString(expectedMetrics),
		),
	)
}
