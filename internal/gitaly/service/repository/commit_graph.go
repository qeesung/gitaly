package repository

import (
	"context"

	gitalyerrors "gitlab.com/gitlab-org/gitaly/v15/internal/errors"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

// WriteCommitGraph write or update commit-graph file in a repository
func (s *server) WriteCommitGraph(
	ctx context.Context,
	in *gitalypb.WriteCommitGraphRequest,
) (*gitalypb.WriteCommitGraphResponse, error) {
	if in.GetRepository() == nil {
		return nil, helper.ErrInvalidArgument(gitalyerrors.ErrEmptyRepository)
	}

	repo := s.localrepo(in.GetRepository())

	if in.GetSplitStrategy() != gitalypb.WriteCommitGraphRequest_SizeMultiple {
		return nil, helper.ErrInvalidArgumentf("unsupported split strategy: %v", in.GetSplitStrategy())
	}

	writeCommitGraphCfg, err := housekeeping.WriteCommitGraphConfigForRepository(ctx, repo)
	if err != nil {
		return nil, helper.ErrInternalf("getting commit-graph config: %w", err)
	}

	if err := housekeeping.WriteCommitGraph(ctx, repo, writeCommitGraphCfg); err != nil {
		return nil, err
	}

	return &gitalypb.WriteCommitGraphResponse{}, nil
}
