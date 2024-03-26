package repository

import (
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) ApplyGitattributes(ctx context.Context, in *gitalypb.ApplyGitattributesRequest) (*gitalypb.ApplyGitattributesResponse, error) {
	// In git 2.43.0+, gitattributes supports reading from HEAD:.gitattributes,
	// so info/attributes is no longer needed. To make sure info/attributes file is cleaned up,
	// we delete it if it exists when reading from HEAD:.gitattributes is called.
	// This logic can be removed when ApplyGitattributes and GetInfoAttributes PRC are totally removed from
	// the code base.
	repository := in.GetRepository()
	if err := s.locator.ValidateRepository(repository); err != nil {
		return nil, fmt.Errorf("validate repo error: %w", err)
	}
	repoPath, err := s.locator.GetRepoPath(repository)
	if err != nil {
		return nil, fmt.Errorf("fail to find repo path at %s: %w", repoPath, err)
	}
	if deletionErr := deleteInfoAttributesFile(repoPath); deletionErr != nil {
		return nil, fmt.Errorf("fail to delete info/gitattributes file at %s: %w", repoPath, deletionErr)
	}

	// Once git 2.43.0 is deployed, we can stop using info/attributes in related RPCs,
	// As a result, ApplyGitattributes() is made as a no-op,
	// so that Gitaly clients will stop writing to info/attributes.
	// This gRPC will be totally removed in the once all the housekeeping on removing info/attributes is done.
	return &gitalypb.ApplyGitattributesResponse{}, nil
}
