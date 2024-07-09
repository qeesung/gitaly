package testcfg

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/version"
)

var buildOnceByName sync.Map

// BuildGitalyGPG builds the gitaly-gpg command and installs it into the binary directory.
func BuildGitalyGPG(tb testing.TB, cfg config.Cfg) string {
	return buildGitalyCommand(tb, cfg, "gitaly-gpg")
}

// BuildGitalyWrapper builds the gitaly-wrapper command and installs it into the binary directory.
func BuildGitalyWrapper(tb testing.TB, cfg config.Cfg) string {
	return buildGitalyCommand(tb, cfg, "gitaly-wrapper")
}

// BuildGitalyLFSSmudge builds the gitaly-lfs-smudge command and installs it into the binary
// directory.
func BuildGitalyLFSSmudge(tb testing.TB, cfg config.Cfg) string {
	return buildGitalyCommand(tb, cfg, "gitaly-lfs-smudge")
}

// BuildGitalyHooks builds the gitaly-hooks command and installs it into the binary directory.
func BuildGitalyHooks(tb testing.TB, cfg config.Cfg) string {
	return buildGitalyCommand(tb, cfg, "gitaly-hooks")
}

// BuildGitalySSH builds the gitaly-ssh command and installs it into the binary directory.
func BuildGitalySSH(tb testing.TB, cfg config.Cfg) string {
	return buildGitalyCommand(tb, cfg, "gitaly-ssh")
}

// BuildPraefect builds the praefect command and installs it into the binary directory.
func BuildPraefect(tb testing.TB, cfg config.Cfg) string {
	return buildGitalyCommand(tb, cfg, "praefect")
}

// BuildGitaly builds the gitaly binary and installs it into the binary directory. The gitaly binary
// embeds other binaries it needs to use when servicing requests. The packed binaries are not built
// prior to building this gitaly binary and thus cannot be guaranteed to be from the same build.
func BuildGitaly(tb testing.TB, cfg config.Cfg) string {
	return buildGitalyCommand(tb, cfg, "gitaly")
}

// buildGitalyCommand builds an executable and places it in the correct directory depending
// whether it is packed in the production build or not.
func buildGitalyCommand(tb testing.TB, cfg config.Cfg, executableName string) string {
	path := BuildBinary(tb, filepath.Dir(cfg.BinaryPath(executableName)), gitalyCommandPath(executableName))
	if runtime.GOOS == "darwin" {
		output, err := exec.Command("codesign", "-s", "-", path).CombinedOutput()
		require.NoError(tb, err, "output: %q", output)
	}

	return path
}

var (
	sharedBinariesDir               string
	createGlobalBinaryDirectoryOnce sync.Once
)

// BuildBinary builds a Go binary once and copies it into the target directory. The source path can
// either be a ".go" file or a directory containing Go files. Returns the path to the executable in
// the destination directory.
func BuildBinary(tb testing.TB, targetDir, sourcePath string) string {
	createGlobalBinaryDirectoryOnce.Do(func() {
		sharedBinariesDir = testhelper.CreateGlobalDirectory(tb, "bins")
	})
	require.NotEmpty(tb, sharedBinariesDir, "creation of shared binary directory failed")

	var (
		// executableName is the name of the executable.
		executableName = filepath.Base(sourcePath)
		// sharedBinaryPath is the path to the binary shared between all tests.
		sharedBinaryPath = filepath.Join(sharedBinariesDir, executableName)
		// targetPath is the final path where the binary should be copied to.
		targetPath = filepath.Join(targetDir, executableName)
	)

	buildOnceInterface, _ := buildOnceByName.LoadOrStore(executableName, &sync.Once{})
	buildOnce, ok := buildOnceInterface.(*sync.Once)
	require.True(tb, ok)

	buildOnce.Do(func() {
		require.NoFileExists(tb, sharedBinaryPath, "binary has already been built")

		// We need to filter out some environments we set globally in our tests which would
		// cause Git to not operate correctly.
		filteredEnvironment := make([]string, 0, len(os.Environ()))
		for _, env := range os.Environ() {
			if !strings.HasPrefix(env, "GIT_DIR=") {
				filteredEnvironment = append(filteredEnvironment, env)
			}
		}

		buildTags := []string{
			"gitaly_test",
		}
		if os.Getenv("GITALY_TESTING_ENABLE_FIPS") != "" {
			buildTags = append(buildTags, "fips")
		}

		cmd := exec.Command(
			"go",
			"build",
			"-buildvcs=false",
			"-tags", strings.Join(buildTags, ","),
			"-ldflags", fmt.Sprintf("-X %s/version.version=%s", testhelper.PkgPath("internal"), version.GetVersion()),
			"-o", sharedBinaryPath,
			sourcePath,
		)
		cmd.Env = filteredEnvironment

		output, err := cmd.CombinedOutput()
		require.NoError(tb, err, "building Go executable: %v, output: %q", err, output)
	})

	require.FileExists(tb, sharedBinaryPath, "%s does not exist", executableName)
	require.NoFileExists(tb, targetPath, "%s exists already -- do you try to build it twice?", executableName)

	require.NoError(tb, os.MkdirAll(targetDir, perm.PrivateDir))

	// We hard-link the file into place instead of copying it because copying used to cause
	// ETXTBSY errors in CI. This is likely caused by a bug in the overlay filesystem used by
	// Docker, so we just work around this by linking the file instead. It's more efficient
	// anyway, the only thing is that no test must modify the binary directly. But let's count
	// on that.
	require.NoError(tb, os.Link(sharedBinaryPath, targetPath))

	return targetPath
}

func gitalyCommandPath(command string) string {
	return fmt.Sprintf("%s/cmd/%s", testhelper.PkgPath(), command)
}
