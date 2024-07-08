package config

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestPruneOldGitalyProcessDirectories(t *testing.T) {
	t.Run("no runtime directories", func(t *testing.T) {
		require.NoError(t, PruneOldGitalyProcessDirectories(testhelper.SharedLogger(t), testhelper.TempDir(t)))
	})

	t.Run("unset runtime directory", func(t *testing.T) {
		require.EqualError(t,
			PruneOldGitalyProcessDirectories(testhelper.SharedLogger(t), ""), "list gitaly process directory: open : no such file or directory")
	})

	t.Run("non-existent runtime directory", func(t *testing.T) {
		require.EqualError(t,
			PruneOldGitalyProcessDirectories(testhelper.SharedLogger(t),
				"/path/does/not/exist"), "list gitaly process directory: open /path/does/not/exist: no such file or directory")
	})

	t.Run("invalid, stale and active runtime directories", func(t *testing.T) {
		baseDir := testhelper.TempDir(t)
		cfg := Cfg{RuntimeDir: baseDir}

		// Setup a runtime directory for our process, it can't be stale as long as
		// we are running.
		ownRuntimeDir, err := SetupRuntimeDirectory(cfg, os.Getpid())
		require.NoError(t, err)

		expectedLogs := map[string]string{}
		expectedErrs := map[string]error{}

		// Setup runtime directories for processes that have finished.
		var prunableDirs []string
		for i := 0; i < 2; i++ {
			cmd := exec.Command("cat")
			require.NoError(t, cmd.Run())

			staleRuntimeDir, err := SetupRuntimeDirectory(cfg, cmd.Process.Pid)
			require.NoError(t, err)

			prunableDirs = append(prunableDirs, staleRuntimeDir)
			expectedLogs[staleRuntimeDir] = "removed leftover gitaly process directory"
		}

		// Setup runtime directory with pid of process not owned by git user
		rootRuntimeDir, err := SetupRuntimeDirectory(cfg, 1)
		require.NoError(t, err)
		expectedLogs[rootRuntimeDir] = "removed leftover gitaly process directory"
		prunableDirs = append(prunableDirs, rootRuntimeDir)

		// Create an unexpected file in the runtime directory
		unexpectedFilePath := filepath.Join(baseDir, "unexpected-file")
		require.NoError(t, os.WriteFile(unexpectedFilePath, []byte(""), perm.PublicFile))
		expectedLogs[unexpectedFilePath] = "ignoring file found in gitaly process directory"

		nonPrunableDirs := []string{ownRuntimeDir}

		// Setup some unexpected directories in the runtime directory
		for _, dirName := range []string{
			"nohyphen",
			"too-many-hyphens",
			"invalidprefix-3",
			"gitaly-invalidpid",
		} {
			dirPath := filepath.Join(baseDir, dirName)
			require.NoError(t, os.Mkdir(dirPath, perm.PrivateDir))
			expectedLogs[dirPath] = "could not prune entry"
			expectedErrs[dirPath] = errors.New("gitaly process directory contains an unexpected directory")
			nonPrunableDirs = append(nonPrunableDirs, dirPath)
		}

		logger := testhelper.NewLogger(t)
		hook := testhelper.AddLoggerHook(logger)
		require.NoError(t, PruneOldGitalyProcessDirectories(logger, cfg.RuntimeDir))

		actualLogs := map[string]string{}
		actualErrs := map[string]error{}
		for _, entry := range hook.AllEntries() {
			actualLogs[entry.Data["path"].(string)] = entry.Message
			if entry.Data["error"] != nil {
				err, ok := entry.Data["error"].(error)
				require.True(t, ok)
				actualErrs[entry.Data["path"].(string)] = err
			}
		}

		require.Equal(t, expectedLogs, actualLogs)
		require.Equal(t, expectedErrs, actualErrs)

		require.FileExists(t, unexpectedFilePath)

		for _, nonPrunableEntry := range nonPrunableDirs {
			require.DirExists(t, nonPrunableEntry, nonPrunableEntry)
		}

		for _, prunableEntry := range prunableDirs {
			require.NoDirExists(t, prunableEntry, prunableEntry)
		}
	})

	t.Run("gitaly-0 directory exists", func(t *testing.T) {
		baseDir := testhelper.TempDir(t)
		cfg := Cfg{RuntimeDir: baseDir}

		_, err := SetupRuntimeDirectory(cfg, 0)
		require.NoError(t, err)

		logger := testhelper.NewLogger(t)
		hook := testhelper.AddLoggerHook(logger)
		require.NoError(t, PruneOldGitalyProcessDirectories(logger, cfg.RuntimeDir))
		require.Len(t, hook.AllEntries(), 1)
		require.Equal(t, "removed gitaly directory with no pid", hook.LastEntry().Message)
	})
}
