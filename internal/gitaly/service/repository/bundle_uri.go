package repository

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// GenerateBundleURI generates a bundle on the server for bundle-URI use.
func (s *server) GenerateBundleURI(ctx context.Context, req *gitalypb.GenerateBundleURIRequest) (_ *gitalypb.GenerateBundleURIResponse, returnErr error) {
	if s.bundleURISink == nil {
		return nil, structerr.NewFailedPrecondition("no bundle-URI sink available")
	}

	repository := req.GetRepository()
	if err := s.locator.ValidateRepository(repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(repository)

	if err := s.bundleURISink.Generate(ctx, repo); err != nil {
		return nil, structerr.NewInternal("generate bundle: %w", err)
	}

	return &gitalypb.GenerateBundleURIResponse{}, nil
}
