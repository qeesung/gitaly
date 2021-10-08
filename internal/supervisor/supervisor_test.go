package supervisor

import (
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func TestRespawnAfterCrashWithoutCircuitBreaker(t *testing.T) {
	t.Parallel()

	pidServer := buildPidServer(t)
	tempDir := testhelper.TempDir(t)

	config, err := NewConfigFromEnv()
	require.NoError(t, err)

	process, err := New(config, t.Name(), nil, []string{pidServer}, tempDir, 0, nil, nil)
	require.NoError(t, err)
	defer process.Stop()

	attempts := config.CrashThreshold
	require.True(t, attempts > 2, "config.CrashThreshold sanity check")

	pids, err := tryConnect(filepath.Join(tempDir, "socket"), attempts, 1*time.Second)
	require.NoError(t, err)

	require.Equal(t, attempts, len(pids), "number of pids should equal number of attempts")

	previous := 0
	for _, pid := range pids {
		require.True(t, pid > 0, "pid > 0")
		require.NotEqual(t, previous, pid, "pid sanity check")
		previous = pid
	}
}

func TestTooManyCrashes(t *testing.T) {
	t.Parallel()

	pidServer := buildPidServer(t)
	tempDir := testhelper.TempDir(t)

	config, err := NewConfigFromEnv()
	require.NoError(t, err)

	process, err := New(config, t.Name(), nil, []string{pidServer}, tempDir, 0, nil, nil)
	require.NoError(t, err)
	defer process.Stop()

	attempts := config.CrashThreshold + 1
	require.True(t, attempts > 2, "config.CrashThreshold sanity check")

	pids, err := tryConnect(filepath.Join(tempDir, "socket"), attempts, 1*time.Second)
	require.Error(t, err, "circuit breaker should cause a connection error / timeout")

	require.Equal(t, config.CrashThreshold, len(pids), "number of pids should equal circuit breaker threshold")
}

func TestSpawnFailure(t *testing.T) {
	t.Parallel()

	pidServer := buildPidServer(t)
	tempDir := testhelper.TempDir(t)

	config, err := NewConfigFromEnv()
	require.NoError(t, err)
	config.CrashWaitTime = 2 * time.Second

	notFoundExe := filepath.Join(tempDir, "not-found")
	require.NoError(t, os.RemoveAll(notFoundExe))
	defer func() { require.NoError(t, os.Remove(notFoundExe)) }()

	process, err := New(config, t.Name(), nil, []string{notFoundExe}, tempDir, 0, nil, nil)
	require.NoError(t, err)
	defer process.Stop()

	time.Sleep(1 * time.Second)

	pids, err := tryConnect(filepath.Join(tempDir, "socket"), 1, 1*time.Millisecond)
	require.Error(t, err, "connection must fail because executable cannot be spawned")
	require.Equal(t, 0, len(pids))

	// 'Fix' the spawning problem of our process
	require.NoError(t, os.Symlink(pidServer, notFoundExe))

	// After CrashWaitTime, the circuit breaker should have closed
	pids, err = tryConnect(filepath.Join(tempDir, "socket"), 1, config.CrashWaitTime)

	require.NoError(t, err, "process should be accepting connections now")
	require.Equal(t, 1, len(pids), "we should have received the pid of the new process")
	require.True(t, pids[0] > 0, "pid sanity check")
}

func tryConnect(socketPath string, attempts int, timeout time.Duration) (pids []int, err error) {
	ctx, cancel := testhelper.Context(testhelper.ContextWithTimeout(timeout))
	defer cancel()

	for j := 0; j < attempts; j++ {
		var curPid int
		for {
			curPid, err = getPid(ctx, socketPath)
			if err == nil {
				break
			}

			select {
			case <-ctx.Done():
				return pids, ctx.Err()
			case <-time.After(5 * time.Millisecond):
				// sleep
			}
		}
		if err != nil {
			return pids, err
		}

		pids = append(pids, curPid)
		if curPid > 0 {
			syscall.Kill(curPid, syscall.SIGKILL)
		}
	}

	return pids, err
}

func getPid(ctx context.Context, socket string) (int, error) {
	var err error
	var conn net.Conn

	for {
		conn, err = net.DialTimeout("unix", socket, 1*time.Millisecond)
		if err == nil {
			break
		}

		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(5 * time.Millisecond):
			// sleep
		}
	}
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	response, err := io.ReadAll(conn)
	if err != nil {
		return 0, err
	}

	return strconv.Atoi(string(response))
}

func buildPidServer(t *testing.T) string {
	t.Helper()

	sourcePath, err := filepath.Abs("test-scripts/pid-server.go")
	require.NoError(t, err)

	return testhelper.BuildBinary(t, testhelper.TempDir(t), sourcePath)
}
