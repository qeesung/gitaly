package repository

import (
	"context"

	gitalyerrors "gitlab.com/gitlab-org/gitaly/internal/errors"
	"gitlab.com/gitlab-org/gitaly/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func (s *server) Cleanup(ctx context.Context, in *gitalypb.CleanupRequest) (*gitalypb.CleanupResponse, error) {
	if in.GetRepository() == nil {
		return nil, helper.ErrInvalidArgument(gitalyerrors.ErrEmptyRepository)
	}

	repo := s.localrepo(in.GetRepository())

	if err := housekeeping.CleanupWorktrees(ctx, repo); err != nil {
		return nil, err
	}

	return &gitalypb.CleanupResponse{}, nil
}
