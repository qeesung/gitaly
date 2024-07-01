package repository

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// ObjectFormat determines the object format of the Git repository.
func (s *server) ObjectFormat(ctx context.Context, request *gitalypb.ObjectFormatRequest) (*gitalypb.ObjectFormatResponse, error) {
	if err := s.locator.ValidateRepository(ctx, request.Repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(request.Repository)

	// Check for the path up-front so that we detect missing repositories early on.
	if _, err := repo.Path(ctx); err != nil {
		return nil, structerr.New("%w", err)
	}

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return nil, structerr.New("detecting object hash: %w", err)
	}

	return &gitalypb.ObjectFormatResponse{
		Format: objectHash.ProtoFormat,
	}, nil
}
