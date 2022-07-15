package testcfg

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

var buildOnceByName sync.Map

// BuildGitalyGit2Go builds the gitaly-git2go command and installs it into the binary directory.
func BuildGitalyGit2Go(t testing.TB, cfg config.Cfg) string {
	return buildGitalyCommand(t, cfg, "gitaly-git2go-v15")
}

// BuildGitalyWrapper builds the gitaly-wrapper command and installs it into the binary directory.
func BuildGitalyWrapper(t *testing.T, cfg config.Cfg) string {
	return buildGitalyCommand(t, cfg, "gitaly-wrapper")
}

// BuildGitalyLFSSmudge builds the gitaly-lfs-smudge command and installs it into the binary
// directory.
func BuildGitalyLFSSmudge(t *testing.T, cfg config.Cfg) string {
	return buildGitalyCommand(t, cfg, "gitaly-lfs-smudge")
}

// BuildGitalyHooks builds the gitaly-hooks command and installs it into the binary directory.
func BuildGitalyHooks(t testing.TB, cfg config.Cfg) string {
	return buildGitalyCommand(t, cfg, "gitaly-hooks")
}

// BuildGitalySSH builds the gitaly-ssh command and installs it into the binary directory.
func BuildGitalySSH(t testing.TB, cfg config.Cfg) string {
	return buildGitalyCommand(t, cfg, "gitaly-ssh")
}

// BuildPraefect builds the praefect command and installs it into the binary directory.
func BuildPraefect(t testing.TB, cfg config.Cfg) string {
	return buildGitalyCommand(t, cfg, "praefect")
}

// buildGitalyCommand builds an executable and places it in the correct directory depending
// whether it is packed in the production build or not.
func buildGitalyCommand(t testing.TB, cfg config.Cfg, executableName string) string {
	return BuildBinary(t, filepath.Dir(cfg.BinaryPath(executableName)), gitalyCommandPath(executableName))
}

var (
	sharedBinariesDir               string
	createGlobalBinaryDirectoryOnce sync.Once
)

// BuildBinary builds a Go binary once and copies it into the target directory. The source path can
// either be a ".go" file or a directory containing Go files. Returns the path to the executable in
// the destination directory.
func BuildBinary(t testing.TB, targetDir, sourcePath string) string {
	createGlobalBinaryDirectoryOnce.Do(func() {
		sharedBinariesDir = testhelper.CreateGlobalDirectory(t, "bins")
	})
	require.NotEmpty(t, sharedBinariesDir, "creation of shared binary directory failed")

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
	require.True(t, ok)

	buildOnce.Do(func() {
		require.NoFileExists(t, sharedBinaryPath, "binary has already been built")

		cfg := Build(t)
		gitCommandFactory := gittest.NewCommandFactory(t, cfg)
		gitExecEnv := gitCommandFactory.GetExecutionEnvironment(context.TODO())

		// Unfortunately, Go has started to execute Git as parts of its build process in
		// order to embed VCS information into the resulting binary. In Gitaly we're doing a
		// bunch of things to verify that we don't ever use Git information from outside of
		// our defined parameters: we intercept Git executed via PATH, and we also override
		// Git configuration locations. So executing Git without special logic simply does
		// not work.
		//
		// While we could in theory just ask it not to do that via `-buildvcs=false`, this
		// option is only understood with Go 1.18+. So we have the option between either
		// using logic that is conditional on the Go version here, or alternatively we fix
		// the environment to allow for the execution of Git. We opt for the latter here and
		// set up a Git command factory.
		gitEnvironment := make([]string, 0, len(os.Environ()))

		// We need to filter out some environments we set globally in our tests which would
		// cause Git to not operate correctly.
		for _, env := range os.Environ() {
			if !strings.HasPrefix(env, "GIT_DIR=") {
				gitEnvironment = append(gitEnvironment, env)
			}
		}

		// Furthermore, as we're using the Git command factory which may or may not use
		// bundled Git we need to append some environment variables that make Git find its
		// auxiliary helper binaries.
		gitEnvironment = append(gitEnvironment, gitExecEnv.EnvironmentVariables...)

		// And last but not least we need to override PATH so that our Git binary from the
		// command factory is up front.
		gitEnvironment = append(gitEnvironment, fmt.Sprintf(
			"PATH=%s:%s", filepath.Dir(gitExecEnv.BinaryPath), os.Getenv("PATH"),
		))

		// Go 1.18 has started to extract VCS information so that it can be embedded into
		// the resulting binary and will thus execute Git in the Gitaly repository. In CI,
		// the Gitaly repository is owned by a different user than the one that is executing
		// tests though, which means that Git will refuse to open the repository because of
		// CVE-2022-24765.
		//
		// Let's override this mechanism by labelling the Git repository as safe. While this
		// does in theory make us vulnerable to this exploit, it is clear that any adversary
		// would already have arbitrary code execution because we are executing code right
		// now that would be controlled by the very same adversary.
		//
		// Note that we cannot pass `safe.directory` via command line arguments by design.
		// Instead, we just override the system-level gitconfig to point to a temporary file
		// that contains this setting.
		_, currentFile, _, ok := runtime.Caller(0)
		require.True(t, ok)
		gitconfigPath := filepath.Join(testhelper.TempDir(t), "gitconfig")
		require.NoError(t, os.WriteFile(gitconfigPath, []byte(
			"[safe]\ndirectory = "+filepath.Join(filepath.Dir(currentFile), "..", "..", "..")+"\n"), 0o400),
		)
		gitEnvironment = append(gitEnvironment,
			"GIT_CONFIG_SYSTEM="+gitconfigPath,
		)

		buildTags := []string{
			"static", "system_libgit2", "gitaly_test",
		}
		if os.Getenv("GITALY_TESTING_ENABLE_FIPS") != "" {
			buildTags = append(buildTags, "fips")
		}

		cmd := exec.Command(
			"go",
			"build",
			"-tags", strings.Join(buildTags, ","),
			"-o", sharedBinaryPath,
			sourcePath,
		)
		cmd.Env = gitEnvironment

		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "building Go executable: %v, output: %q", err, output)
	})

	require.FileExists(t, sharedBinaryPath, "%s does not exist", executableName)
	require.NoFileExists(t, targetPath, "%s exists already -- do you try to build it twice?", executableName)

	require.NoError(t, os.MkdirAll(targetDir, os.ModePerm))

	// We hard-link the file into place instead of copying it because copying used to cause
	// ETXTBSY errors in CI. This is likely caused by a bug in the overlay filesystem used by
	// Docker, so we just work around this by linking the file instead. It's more efficient
	// anyway, the only thing is that no test must modify the binary directly. But let's count
	// on that.
	require.NoError(t, os.Link(sharedBinaryPath, targetPath))

	return targetPath
}

func gitalyCommandPath(command string) string {
	return fmt.Sprintf("gitlab.com/gitlab-org/gitaly/v15/cmd/%s", command)
}
