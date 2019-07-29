package repository

import (
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func removeOriginInRepo(ctx context.Context, repository *gitalypb.Repository) error {
	cmd, err := git.Command(ctx, repository, "remote", "remove", "origin")

	if err != nil {
		return fmt.Errorf("remote cmd start: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("remote cmd wait: %v", err)
	}

	return nil
}
