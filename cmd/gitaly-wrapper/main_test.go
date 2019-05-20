package main

import (
	"io/ioutil"
	"os"
	"os/exec"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/config"
)

// TestStolenPid tests for regressions in https://gitlab.com/gitlab-org/gitaly/issues/1661
func TestStolenPid(t *testing.T) {
	defer func(oldValue string) {
		os.Setenv(config.EnvPidFile, oldValue)
	}(os.Getenv(config.EnvPidFile))

	pidFile, err := ioutil.TempFile("", "pidfile")
	require.NoError(t, err)
	defer os.Remove(pidFile.Name())

	os.Setenv(config.EnvPidFile, pidFile.Name())

	cmd := exec.Command("tail", "-f")
	require.NoError(t, cmd.Start())
	defer cmd.Process.Kill()

	_, err = pidFile.WriteString(strconv.Itoa(cmd.Process.Pid))
	require.NoError(t, err)
	require.NoError(t, pidFile.Close())

	gitaly, err := findGitaly()
	require.NoError(t, err)
	require.NotNil(t, gitaly)
	require.Equal(t, cmd.Process.Pid, gitaly.Pid)

	t.Run("stolen", func(t *testing.T) {
		require.False(t, isGitaly(gitaly, "/path/to/gitaly"))
	})

	t.Run("not stolen", func(t *testing.T) {
		require.True(t, isGitaly(gitaly, "/path/to/tail"))
	})
}
