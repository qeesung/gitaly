package testhelper

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/version"
)

var buildOnceByName sync.Map

// BuildGitalyGit2Go builds the gitaly-git2go command and installs it into the binary directory.
func BuildGitalyGit2Go(t testing.TB, cfg config.Cfg) {
	BuildBinary(t, cfg.BinDir, gitalyCommandPath("gitaly-git2go"), func(t testing.TB) {
		// The link is needed because gitaly uses version-named binary.
		// Please check out https://gitlab.com/gitlab-org/gitaly/-/issues/3647 for more info.
		require.NoError(t, os.Link(filepath.Join(cfg.BinDir, "gitaly-git2go"), filepath.Join(cfg.BinDir, "gitaly-git2go-"+version.GetModuleVersion())))
	})
}

// BuildGitalyLFSSmudge builds the gitaly-lfs-smudge command and installs it into the binary
// directory.
func BuildGitalyLFSSmudge(t *testing.T, cfg config.Cfg) {
	BuildBinary(t, cfg.BinDir, gitalyCommandPath("gitaly-lfs-smudge"), nil)
}

// BuildGitalyHooks builds the gitaly-hooks command and installs it into the binary directory.
func BuildGitalyHooks(t testing.TB, cfg config.Cfg) {
	BuildBinary(t, cfg.BinDir, gitalyCommandPath("gitaly-hooks"), nil)
}

// BuildGitalySSH builds the gitaly-ssh command and installs it into the binary directory.
func BuildGitalySSH(t testing.TB, cfg config.Cfg) {
	BuildBinary(t, cfg.BinDir, gitalyCommandPath("gitaly-ssh"), nil)
}

// BuildPraefect builds the praefect command and installs it into the binary directory.
func BuildPraefect(t testing.TB, cfg config.Cfg) {
	BuildBinary(t, cfg.BinDir, gitalyCommandPath("praefect"), nil)
}

// BuildBinary builds a Go binary once and copies it into the target directory. The source path can
// either be a ".go" file or a directory containing Go files. Returns the path to the executable in
// the destination directory.
func BuildBinary(t testing.TB, targetDir, sourcePath string, postBuild func(testing.TB)) string {
	require.NotEmpty(t, testDirectory, "you must call testhelper.Configure() first")

	var (
		// executableName is the name of the executable.
		executableName = filepath.Base(sourcePath)
		// sharedBinariesDir is where all binaries will be compiled into. This directory is
		// shared between all tests.
		sharedBinariesDir = filepath.Join(testDirectory, "bins")
		// sharedBinaryPath is the path to the binary shared between all tests.
		sharedBinaryPath = filepath.Join(sharedBinariesDir, executableName)
		// targetPath is the final path where the binary should be copied to.
		targetPath = filepath.Join(targetDir, executableName)
	)

	buildOnceInterface, _ := buildOnceByName.LoadOrStore(executableName, &sync.Once{})
	buildOnce, ok := buildOnceInterface.(*sync.Once)
	require.True(t, ok)

	buildOnce.Do(func() {
		require.NoError(t, os.MkdirAll(sharedBinariesDir, os.ModePerm))
		require.NoFileExists(t, sharedBinaryPath, "binary has already been built")

		MustRunCommand(t, nil,
			"go",
			"build",
			"-tags", "static,system_libgit2",
			"-o", sharedBinaryPath,
			sourcePath,
		)
		if postBuild != nil {
			postBuild(t)
		}
	})

	require.FileExists(t, sharedBinaryPath, "%s does not exist", executableName)
	require.NoFileExists(t, targetPath, "%s exists already -- do you try to build it twice?", executableName)

	require.NoError(t, os.MkdirAll(targetDir, os.ModePerm))
	CopyFile(t, sharedBinaryPath, targetPath)
	require.NoError(t, os.Chmod(targetPath, 0o755))

	return targetPath
}

func gitalyCommandPath(command string) string {
	return fmt.Sprintf("gitlab.com/gitlab-org/gitaly/v14/cmd/%s", command)
}
