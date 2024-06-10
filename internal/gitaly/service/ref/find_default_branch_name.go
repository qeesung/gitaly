package ref

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// FindDefaultBranchName returns the default branch name for the given repository
func (s *server) FindDefaultBranchName(ctx context.Context, in *gitalypb.FindDefaultBranchNameRequest) (*gitalypb.FindDefaultBranchNameResponse, error) {
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}
	repo := s.localrepo(repository)

	if in.GetHeadOnly() {
		head, err := repo.HeadReference(ctx)
		if err != nil {
			return nil, structerr.NewInternal("head reference: %w", err)
		}
		return &gitalypb.FindDefaultBranchNameResponse{Name: []byte(head)}, nil
	}

	defaultBranch, err := repo.GetDefaultBranch(ctx)
	if err != nil {
		return nil, structerr.NewInternal("get default branch: %w", err)
	}
	return &gitalypb.FindDefaultBranchNameResponse{Name: []byte(defaultBranch)}, nil
}
