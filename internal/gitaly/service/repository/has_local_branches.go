package repository

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) HasLocalBranches(ctx context.Context, in *gitalypb.HasLocalBranchesRequest) (*gitalypb.HasLocalBranchesResponse, error) {
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}
	hasBranches, err := s.localrepo(repository).HasBranches(ctx)
	if err != nil {
		return nil, structerr.NewInternal("%w", err)
	}

	return &gitalypb.HasLocalBranchesResponse{Value: hasBranches}, nil
}
