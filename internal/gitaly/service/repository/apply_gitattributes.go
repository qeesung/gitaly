package repository

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) ApplyGitattributes(ctx context.Context, in *gitalypb.ApplyGitattributesRequest) (*gitalypb.ApplyGitattributesResponse, error) {
	// In git 2.43.0+, gitattributes supports reading from HEAD:.gitattributes,
	// so info/attributes is no longer needed. Besides that, info/attributes file needs to
	// be cleaned because it has a higher precedence
	// than HEAD:.gitattributes. We want to avoid HEAD:.gitattributes being
	// overridden.
	//
	// To make sure info/attributes file is cleaned up,
	// we delete it if it exists when reading from HEAD:.gitattributes is called.
	// This logic can be removed when ApplyGitattributes and GetInfoAttributes RPC are totally removed from
	// the code base.
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(ctx, repository); err != nil {
		return nil, structerr.NewInvalidArgument("validate repo error: %w", err)
	}
	repoPath, err := s.locator.GetRepoPath(ctx, repository)
	if err != nil {
		return nil, structerr.NewInternal("find repo path: %w", err).WithMetadata("path", repoPath)
	}
	if deletionErr := deleteInfoAttributesFile(repoPath); deletionErr != nil {
		return nil, structerr.NewInternal("delete info/gitattributes file: %w", deletionErr).WithMetadata("path", repoPath)
	}

	// Once git 2.43.0 is deployed, we can stop using info/attributes in related RPCs,
	// As a result, ApplyGitattributes() is made as a no-op,
	// so that Gitaly clients will stop writing to info/attributes.
	// This gRPC will be totally removed in the once all the housekeeping on removing info/attributes is done.
	return &gitalypb.ApplyGitattributesResponse{}, nil
}
