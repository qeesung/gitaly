package gittest

import (
	"io"
	"os"
	"os/exec"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

// ExecConfig contains configuration for ExecOpts.
type ExecConfig struct {
	// Stdin sets up stdin of the spawned command.
	Stdin io.Reader
	// Stdout sets up stdout of the spawned command. Note that `ExecOpts()` will not return any
	// output anymore if this field is set.
	Stdout io.Writer
	// Env contains environment variables that should be appended to the spawned command's
	// environment.
	Env []string
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
			tb.Log(cfg.Git.BinPath, args)
			if ee, ok := err.(*exec.ExitError); ok {
				tb.Logf("%s\n", ee.Stderr)
			}
			tb.Fatal(err)
		}

		return nil
	}

	output, err := cmd.Output()
	if err != nil {
		tb.Log(cfg.Git.BinPath, args)
		if ee, ok := err.(*exec.ExitError); ok {
			tb.Logf("%s: %s\n", ee.Stderr, output)
		}
		tb.Fatal(err)
	}

	return output
}

// NewCommand creates a new Git command ready for execution.
func NewCommand(tb testing.TB, cfg config.Cfg, args ...string) *exec.Cmd {
	tb.Helper()
	return createCommand(tb, cfg, ExecConfig{}, args...)
}

func createCommand(tb testing.TB, cfg config.Cfg, execCfg ExecConfig, args ...string) *exec.Cmd {
	tb.Helper()

	ctx := testhelper.Context(tb)

	execEnv := NewCommandFactory(tb, cfg).GetExecutionEnvironment(ctx)

	cmd := exec.CommandContext(ctx, execEnv.BinaryPath, args...)
	cmd.Env = command.AllowedEnvironment(os.Environ())
	cmd.Env = append(cmd.Env,
		"GIT_AUTHOR_DATE=1572776879 +0100",
		"GIT_COMMITTER_DATE=1572776879 +0100",
		"GIT_CONFIG_COUNT=4",
		"GIT_CONFIG_KEY_0=init.defaultBranch",
		"GIT_CONFIG_VALUE_0=master",
		"GIT_CONFIG_KEY_1=init.templateDir",
		"GIT_CONFIG_VALUE_1=",
		"GIT_CONFIG_KEY_2=user.name",
		"GIT_CONFIG_VALUE_2=Your Name",
		"GIT_CONFIG_KEY_3=user.email",
		"GIT_CONFIG_VALUE_3=you@example.com",
	)
	cmd.Env = append(cmd.Env, execEnv.EnvironmentVariables...)
	cmd.Env = append(cmd.Env, execCfg.Env...)

	cmd.Stdout = execCfg.Stdout
	cmd.Stdin = execCfg.Stdin

	return cmd
}
