package command

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/cgroups"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestNew_environment(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	extraVar := "FOOBAR=123456"

	var buf bytes.Buffer
	cmd, err := New(ctx, testhelper.SharedLogger(t), []string{"/usr/bin/env"}, WithStdout(&buf), WithEnvironment([]string{extraVar}))

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
		tc := tc

		t.Run(tc.key, func(t *testing.T) {
			if tc.key == "LD_LIBRARY_PATH" && runtime.GOOS == "darwin" {
				t.Skip("System Integrity Protection prevents using dynamic linker (dyld) environment variables on macOS. https://apple.co/2XDH4iC")
			}

			t.Setenv(tc.key, tc.value)

			var buf bytes.Buffer
			cmd, err := New(ctx, testhelper.SharedLogger(t), []string{"/usr/bin/env"}, WithStdout(&buf))
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
	cmd, err := New(ctx, testhelper.SharedLogger(t), []string{"/usr/bin/env"}, WithStdout(&buf))
	require.NoError(t, err)
	require.NoError(t, cmd.Wait())

	require.NotContains(t, strings.Split(buf.String(), "\n"), fmt.Sprintf("%s=%s", unexportedEnvKey, unexportedEnvVal))
}

func TestNew_dontSpawnWithCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(testhelper.Context(t))
	cancel()

	cmd, err := New(ctx, testhelper.SharedLogger(t), nil)
	require.Equal(t, err, ctx.Err())
	require.Nil(t, cmd)
}

func TestNew_rejectContextWithoutDone(t *testing.T) {
	t.Parallel()

	require.PanicsWithValue(t, "command spawned with context without Done() channel", func() {
		_, err := New(testhelper.ContextWithoutCancel(), testhelper.SharedLogger(t), []string{"true"})
		require.NoError(t, err)
	})
}

func TestNew_spawnTimeout(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	spawnTimeout := 200 * time.Millisecond
	spawnTokenManager := NewSpawnTokenManager(SpawnConfig{
		Timeout:     spawnTimeout,
		MaxParallel: 0,
	})

	tick := time.After(spawnTimeout / 2)
	errCh := make(chan error)
	go func() {
		_, err := New(ctx, testhelper.SharedLogger(t), []string{"true"}, WithSpawnTokenManager(spawnTokenManager))
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
	err := <-errCh
	var structErr structerr.Error
	require.ErrorAs(t, err, &structErr)
	details := structErr.Details()
	require.Len(t, details, 1)

	limitErr, ok := details[0].(*gitalypb.LimitError)
	require.True(t, ok)

	testhelper.RequireGrpcCode(t, err, codes.ResourceExhausted)
	require.Equal(t, "process spawn timed out after 200ms", limitErr.ErrorMessage)
	require.Equal(t, durationpb.New(0).AsDuration(), limitErr.RetryAfter.AsDuration())
}

func TestCommand_Wait_contextCancellationKillsCommand(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cancel := context.WithCancel(ctx)

	cmd, err := New(ctx, testhelper.SharedLogger(t), []string{"cat", "/dev/urandom"}, WithSetupStdout())
	require.NoError(t, err)

	// Read one byte to ensure the process is running.
	n, err := cmd.Read(make([]byte, 1))
	require.NoError(t, err)
	require.Equal(t, 1, n)

	cancel()

	// Verify that the cancelled context causes us to receive a read error when consuming the command's output. In
	// older days this used to succeed just fine, which had the consequence that we obtain partial stdout of the
	// killed process without noticing that it in fact got killed.
	_, err = io.ReadAll(cmd)

	require.Equal(t, &fs.PathError{
		Op: "read", Path: "|0", Err: os.ErrClosed,
	}, err)

	err = cmd.Wait()
	// Depending on whether the closed file descriptor or the sent signal arrive first, cat(1) may fail
	// either with SIGPIPE or with SIGKILL.
	require.Contains(t, []error{
		fmt.Errorf("signal: %s: %w", syscall.SIGTERM, context.Canceled),
		fmt.Errorf("signal: %s: %w", syscall.SIGPIPE, context.Canceled),
	}, err)
	require.ErrorIs(t, err, context.Canceled)
}

func TestNew_setupStdin(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	stdin := "Test value"

	var buf bytes.Buffer
	cmd, err := New(ctx, testhelper.SharedLogger(t), []string{"cat"}, WithSetupStdin(), WithStdout(&buf))
	require.NoError(t, err)

	_, err = fmt.Fprintf(cmd, "%s", stdin)
	require.NoError(t, err)

	require.NoError(t, cmd.Wait())
	require.Equal(t, stdin, buf.String())
}

func TestCommand_read(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	t.Run("without stdout set up", func(t *testing.T) {
		require.PanicsWithValue(t, "command has no reader", func() {
			cmd, err := New(ctx, testhelper.SharedLogger(t), []string{"echo", "test value"})
			require.NoError(t, err)
			_, _ = io.ReadAll(cmd)
		})
	})

	t.Run("with stdout set up", func(t *testing.T) {
		cmd, err := New(ctx, testhelper.SharedLogger(t), []string{"echo", "test value"}, WithSetupStdout())
		require.NoError(t, err)

		output, err := io.ReadAll(cmd)
		require.NoError(t, err)
		require.Equal(t, "test value\n", string(output))

		require.NoError(t, cmd.Wait())
	})
}

func TestCommand_readPartial(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cmd, err := New(ctx, testhelper.SharedLogger(t), []string{"head", "-c", "1000000", "/dev/urandom"}, WithSetupStdout())
	require.NoError(t, err)
	require.EqualError(t, cmd.Wait(), "signal: broken pipe")
}

func TestNew_nulByteInArgument(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cmd, err := New(ctx, testhelper.SharedLogger(t), []string{"sh", "-c", "hello\x00world"})
	require.Equal(t, fmt.Errorf("detected null byte in command argument %q", "hello\x00world"), err)
	require.Nil(t, cmd)
}

func TestNew_missingBinary(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cmd, err := New(ctx, testhelper.SharedLogger(t), []string{"command-non-existent"})
	require.EqualError(t, err, "starting process [command-non-existent]: exec: \"command-non-existent\": executable file not found in $PATH")
	require.Nil(t, cmd)
}

func TestCommand_stderrLogging(t *testing.T) {
	t.Parallel()

	binaryPath := testhelper.WriteExecutable(t, filepath.Join(testhelper.TempDir(t), "script"), []byte(`#!/usr/bin/env bash
		for i in {1..5}
		do
			echo 'hello world' 1>&2
		done
		exit 1
	`))

	logger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(logger)
	ctx := testhelper.Context(t)

	var stdout bytes.Buffer
	cmd, err := New(ctx, logger, []string{binaryPath}, WithStdout(&stdout))
	require.NoError(t, err)

	require.EqualError(t, cmd.Wait(), "exit status 1")
	require.Empty(t, stdout.Bytes())
	require.Equal(t, strings.Repeat("hello world\n", 5), hook.LastEntry().Message)
}

func TestCommand_stderrLoggingTruncation(t *testing.T) {
	t.Parallel()

	binaryPath := testhelper.WriteExecutable(t, filepath.Join(testhelper.TempDir(t), "script"), []byte(`#!/usr/bin/env bash
		for i in {1..1000}
		do
			printf '%06d zzzzzzzzzz\n' $i >&2
		done
		exit 1
	`))

	logger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(logger)
	ctx := testhelper.Context(t)

	var stdout bytes.Buffer
	cmd, err := New(ctx, logger, []string{binaryPath}, WithStdout(&stdout))
	require.NoError(t, err)

	require.Error(t, cmd.Wait())
	require.Empty(t, stdout.Bytes())
	require.Len(t, hook.LastEntry().Message, maxStderrBytes)
}

func TestCommand_stderrLoggingWithNulBytes(t *testing.T) {
	t.Parallel()

	binaryPath := testhelper.WriteExecutable(t, filepath.Join(testhelper.TempDir(t), "script"), []byte(`#!/usr/bin/env bash
		dd if=/dev/zero bs=1000 count=1000 status=none >&2
		exit 1
	`))

	logger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(logger)
	ctx := testhelper.Context(t)

	var stdout bytes.Buffer
	cmd, err := New(ctx, logger, []string{binaryPath}, WithStdout(&stdout))
	require.NoError(t, err)

	require.Error(t, cmd.Wait())
	require.Empty(t, stdout.Bytes())
	require.Equal(t, strings.Repeat("\x00", maxStderrLineLength), hook.LastEntry().Message)
}

func TestCommand_stderrLoggingLongLine(t *testing.T) {
	t.Parallel()

	binaryPath := testhelper.WriteExecutable(t, filepath.Join(testhelper.TempDir(t), "script"), []byte(`#!/usr/bin/env bash
		printf 'a%.0s' {1..8192} >&2
		printf '\n' >&2
		printf 'b%.0s' {1..8192} >&2
		exit 1
	`))

	logger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(logger)
	ctx := testhelper.Context(t)

	var stdout bytes.Buffer
	cmd, err := New(ctx, logger, []string{binaryPath}, WithStdout(&stdout))
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

	binaryPath := testhelper.WriteExecutable(t, filepath.Join(testhelper.TempDir(t), "script"), []byte(`#!/usr/bin/env bash
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

	logger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(logger)
	ctx := testhelper.Context(t)

	var stdout bytes.Buffer
	cmd, err := New(ctx, logger, []string{binaryPath}, WithStdout(&stdout))
	require.NoError(t, err)
	require.Error(t, cmd.Wait())

	require.Empty(t, stdout.Bytes())
	require.Len(t, hook.LastEntry().Message, maxStderrBytes)
}

type mockCgroupManager struct {
	cgroups.Manager
	path string
}

func (m mockCgroupManager) AddCommand(*exec.Cmd, ...cgroups.AddCommandOption) (string, error) {
	return m.path, nil
}

func (m mockCgroupManager) SupportsCloneIntoCgroup() bool {
	return true
}

func (m mockCgroupManager) CloneIntoCgroup(*exec.Cmd, ...cgroups.AddCommandOption) (string, io.Closer, error) {
	return m.path, io.NopCloser(nil), nil
}

func TestCommand_logMessage(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	logger := testhelper.NewLogger(t)
	logger.LogrusEntry().Logger.SetLevel(logrus.DebugLevel) //nolint:staticcheck
	hook := testhelper.AddLoggerHook(logger)

	cmd, err := New(ctx, logger, []string{"echo", "hello world"},
		WithCgroup(mockCgroupManager{
			path: "/sys/fs/cgroup/1",
		}, nil),
	)
	require.NoError(t, err)

	require.NoError(t, cmd.Wait())
	logEntry := hook.LastEntry()
	assert.Equal(t, cmd.Pid(), logEntry.Data["pid"])
	assert.Equal(t, []string{"echo", "hello world"}, logEntry.Data["args"])
	assert.Equal(t, 0, logEntry.Data["command.exitCode"])
	assert.Equal(t, "/sys/fs/cgroup/1", logEntry.Data["command.cgroup_path"])
}

func TestCommand_withFinalizer(t *testing.T) {
	t.Parallel()

	t.Run("context cancellation runs finalizer", func(t *testing.T) {
		ctx, cancel := context.WithCancel(testhelper.Context(t))

		finalizerCh := make(chan struct{})
		_, err := New(ctx, testhelper.SharedLogger(t), []string{"echo"}, WithFinalizer(func(context.Context, *Command) {
			close(finalizerCh)
		}))
		require.NoError(t, err)

		cancel()

		<-finalizerCh
	})

	t.Run("Wait runs finalizer", func(t *testing.T) {
		ctx := testhelper.Context(t)

		finalizerCh := make(chan struct{})
		cmd, err := New(ctx, testhelper.SharedLogger(t), []string{"echo"}, WithFinalizer(func(context.Context, *Command) {
			close(finalizerCh)
		}))
		require.NoError(t, err)

		require.NoError(t, cmd.Wait())

		<-finalizerCh
	})

	t.Run("Wait runs multiple finalizers", func(t *testing.T) {
		ctx := testhelper.Context(t)

		wg := sync.WaitGroup{}
		wg.Add(2)
		cmd, err := New(
			ctx,
			testhelper.SharedLogger(t),
			[]string{"echo"},
			WithFinalizer(func(context.Context, *Command) { wg.Done() }),
			WithFinalizer(func(context.Context, *Command) { wg.Done() }),
		)
		require.NoError(t, err)
		require.NoError(t, cmd.Wait())

		wg.Wait()
	})

	t.Run("Wait runs finalizer with the latest context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(testhelper.Context(t))
		//nolint:staticcheck
		ctx = context.WithValue(ctx, "hello", "world")

		_, err := New(ctx, testhelper.SharedLogger(t), []string{"echo"}, WithFinalizer(func(ctx context.Context, _ *Command) {
			require.Equal(t, "world", ctx.Value("hello"))
		}))
		require.NoError(t, err)

		cancel()
	})

	t.Run("process exit does not run finalizer", func(t *testing.T) {
		ctx := testhelper.Context(t)

		finalizerCh := make(chan struct{})
		_, err := New(ctx, testhelper.SharedLogger(t), []string{"echo"}, WithFinalizer(func(context.Context, *Command) {
			close(finalizerCh)
		}))
		require.NoError(t, err)

		select {
		case <-finalizerCh:
			// Command finalizers should only be running when we have either explicitly
			// called `Wait()` on the command, or when the context has been cancelled.
			// Otherwise we may run into the case where finalizers have already been ran
			// on the exited process even though we may still be busy handling the
			// output of that command, which may result in weird races.
			require.FailNow(t, "finalizer should not have been ran")
		case <-time.After(50 * time.Millisecond):
		}
	})
}
