package git

import (
	"context"
	"os/exec"

	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/git/alternates"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

// Command creates a git.Command with the given args
func Command(ctx context.Context, repo *pb.Repository, args ...string) (*command.Command, error) {
	repoPath, env, err := alternates.PathAndEnv(repo)
	if err != nil {
		return nil, err
	}

	args = append([]string{"--git-dir", repoPath}, args...)
	return command.New(ctx, exec.Command(command.GitPath(), args...), nil, nil, nil, env...)
}
