package git

import (
	"context"
	"io"
	"os/exec"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
)

// unsafeCmd creates a git.unsafeCmd with the given args and Repository
func unsafeCmd(ctx context.Context, repo repository.GitRepo, args ...string) (*command.Command, error) {
	args, env, err := argsAndEnv(repo, args...)
	if err != nil {
		return nil, err
	}

	return unsafeBareCmd(ctx, nil, nil, nil, env, args...)
}

// unsafeStdinCmd creates a git.Command with the given args and Repository that is
// suitable for Write()ing to
func unsafeStdinCmd(ctx context.Context, repo repository.GitRepo, args ...string) (*command.Command, error) {
	args, env, err := argsAndEnv(repo, args...)
	if err != nil {
		return nil, err
	}

	return unsafeBareCmd(ctx, command.SetupStdin, nil, nil, env, args...)
}

func argsAndEnv(repo repository.GitRepo, args ...string) ([]string, []string, error) {
	repoPath, env, err := alternates.PathAndEnv(repo)
	if err != nil {
		return nil, nil, err
	}

	args = append([]string{"--git-dir", repoPath}, args...)

	return args, env, nil
}

// unsafeBareCmd creates a git.Command with the given args, stdin/stdout/stderr, and env
func unsafeBareCmd(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, env []string, args ...string) (*command.Command, error) {
	env = append(env, command.GitEnv...)

	return command.New(ctx, exec.Command(command.GitPath(), args...), stdin, stdout, stderr, env...)
}

// unsafeCmdWithoutRepo works like Command but without a git repository
func unsafeCmdWithoutRepo(ctx context.Context, args ...string) (*command.Command, error) {
	return unsafeBareCmd(ctx, nil, nil, nil, nil, args...)
}
