package git

import (
	"context"
	"io"
	"os/exec"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git/alternates"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
)

// Command creates a git.Command with the given args and Repository
//
// Deprecated: use git.SafeCmd instead
func Command(ctx context.Context, repo repository.GitRepo, args ...string) (*command.Command, error) {
	args, env, err := argsAndEnv(repo, args...)
	if err != nil {
		return nil, err
	}

	return BareCommand(ctx, nil, nil, nil, env, args...)
}

// StdinCommand creates a git.Command with the given args and Repository that is
// suitable for Write()ing to
//
// Deprecated: Use git.SafeStdinCmd instead
func StdinCommand(ctx context.Context, repo repository.GitRepo, args ...string) (*command.Command, error) {
	args, env, err := argsAndEnv(repo, args...)
	if err != nil {
		return nil, err
	}

	return BareCommand(ctx, command.SetupStdin, nil, nil, env, args...)
}

func argsAndEnv(repo repository.GitRepo, args ...string) ([]string, []string, error) {
	repoPath, env, err := alternates.PathAndEnv(repo)
	if err != nil {
		return nil, nil, err
	}

	args = append([]string{"--git-dir", repoPath}, args...)

	return args, env, nil
}

// BareCommand creates a git.Command with the given args, stdin/stdout/stderr, and env
//
// Deprecated: use git.SafeBareCmd
func BareCommand(ctx context.Context, stdin io.Reader, stdout, stderr io.Writer, env []string, args ...string) (*command.Command, error) {
	env = append(env, command.GitEnv...)

	return command.New(ctx, exec.Command(command.GitPath(), args...), stdin, stdout, stderr, env...)
}

// CommandWithoutRepo works like Command but without a git repository
func CommandWithoutRepo(ctx context.Context, args ...string) (*command.Command, error) {
	return BareCommand(ctx, nil, nil, nil, nil, args...)
}
