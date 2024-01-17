package gittest

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// ExecConfig contains configuration for ExecOpts.
type ExecConfig struct {
	// Stdin sets up stdin of the spawned command.
	Stdin io.Reader
	// Stdout sets up stdout of the spawned command. Note that `ExecOpts()` will not return any
	// output anymore if this field is set.
	Stdout io.Writer
	// Stderr sets up stderr of the spawned command. If this field is not set, the error is
	// dumped to test logs.
	Stderr io.Writer
	// Env contains environment variables that should be appended to the spawned command's
	// environment.
	Env []string
	// ExpectedExitCode is used to check the resulting exit code of the command. This can be used in case a command
	// is expected to return an error code.
	ExpectedExitCode int
}

// Exec runs a git command and returns the standard output, or fails.
func Exec(tb testing.TB, cfg config.Cfg, args ...string) []byte {
	tb.Helper()
	return ExecOpts(tb, cfg, ExecConfig{}, args...)
}

// ExecOpts runs a git command with the given configuration.
func ExecOpts(tb testing.TB, cfg config.Cfg, execCfg ExecConfig, args ...string) []byte {
	tb.Helper()

	cmd := createCommand(tb, cfg, execCfg, args...)

	// If the caller has passed an stdout writer to us we cannot use `cmd.Output()`. So
	// we detect this case and call `cmd.Run()` instead.
	if execCfg.Stdout != nil {
		if err := cmd.Run(); err != nil {
			handleExecErr(tb, cfg, execCfg, args, err)
		}

		return nil
	}

	output, err := cmd.Output()
	if err != nil {
		handleExecErr(tb, cfg, execCfg, args, err)
	}

	return output
}

func handleExecErr(tb testing.TB, cfg config.Cfg, execCfg ExecConfig, args []string, err error) {
	var stderr []byte
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if execCfg.ExpectedExitCode == exitErr.ExitCode() {
			return
		}
		stderr = exitErr.Stderr
	}
	tb.Log(cfg.Git.BinPath, args)
	if len(stderr) > 0 {
		tb.Logf("%s\n", stderr)
	}
	tb.Fatal(err)
}

// NewCommand creates a new Git command ready for execution.
func NewCommand(tb testing.TB, cfg config.Cfg, args ...string) *exec.Cmd {
	tb.Helper()
	return createCommand(tb, cfg, ExecConfig{}, args...)
}

func createCommand(tb testing.TB, cfg config.Cfg, execCfg ExecConfig, args ...string) *exec.Cmd {
	tb.Helper()

	ctx := testhelper.Context(tb)

	factory := NewCommandFactory(tb, cfg)
	execEnv := factory.GetExecutionEnvironment(ctx)

	gitConfig, err := factory.GlobalConfiguration(ctx)
	require.NoError(tb, err)
	gitConfig = append(gitConfig,
		git.ConfigPair{Key: "init.defaultBranch", Value: git.DefaultBranch},
		git.ConfigPair{Key: "init.templateDir", Value: ""},
		git.ConfigPair{Key: "user.name", Value: "Your Name"},
		git.ConfigPair{Key: "user.email", Value: "you@example.com"},
	)

	cmd := exec.CommandContext(ctx, execEnv.BinaryPath, args...)
	cmd.Env = command.AllowedEnvironment(os.Environ())
	cmd.Env = append(cmd.Env, "GIT_AUTHOR_DATE=1572776879 +0100", "GIT_COMMITTER_DATE=1572776879 +0100")
	cmd.Env = append(cmd.Env, git.ConfigPairsToGitEnvironment(gitConfig)...)
	cmd.Env = append(cmd.Env, execEnv.EnvironmentVariables...)
	cmd.Env = append(cmd.Env, execCfg.Env...)

	cmd.Stdout = execCfg.Stdout
	cmd.Stdin = execCfg.Stdin
	cmd.Stderr = execCfg.Stderr

	return cmd
}

var _ git.RepositoryExecutor = RepositoryPathExecutor{}

// RepositoryPathExecutor is a `git.RepositoryExecutor` that knows to execute commands in a Git repository identified by
// its absolute path.
type RepositoryPathExecutor struct {
	storage.Repository
	factory git.CommandFactory
}

// NewRepositoryPathExecutor creates a new RepositoryPathExecutor for the given repository.
func NewRepositoryPathExecutor(tb testing.TB, cfg config.Cfg, repoPath string) RepositoryPathExecutor {
	relativePath, err := filepath.Rel(cfg.Storages[0].Path, repoPath)
	require.NoError(tb, err)

	return RepositoryPathExecutor{
		Repository: &gitalypb.Repository{
			StorageName:  cfg.Storages[0].Name,
			RelativePath: relativePath,
		},
		factory: NewCommandFactory(tb, cfg),
	}
}

// Exec executes a command in the given repository.
func (e RepositoryPathExecutor) Exec(ctx context.Context, cmd git.Command, opts ...git.CmdOpt) (*command.Command, error) {
	return e.factory.New(ctx, e.Repository, cmd, opts...)
}

// ExecAndWait executes a command in the given repository and waits for it to exit.
func (e RepositoryPathExecutor) ExecAndWait(ctx context.Context, cmd git.Command, opts ...git.CmdOpt) error {
	command, err := e.Exec(ctx, cmd, opts...)
	if err != nil {
		return err
	}

	return command.Wait()
}

// GitVersion determines the Git version used by the executor.
func (e RepositoryPathExecutor) GitVersion(ctx context.Context) (git.Version, error) {
	return e.factory.GitVersion(ctx)
}

// ObjectHash determines the object hash used by the repository.
func (e RepositoryPathExecutor) ObjectHash(ctx context.Context) (git.ObjectHash, error) {
	return git.DetectObjectHash(ctx, e.factory, e.Repository)
}
