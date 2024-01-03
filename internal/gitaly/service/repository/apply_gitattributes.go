package repository

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) ApplyGitattributes(ctx context.Context, in *gitalypb.ApplyGitattributesRequest) (*gitalypb.ApplyGitattributesResponse, error) {
	// From 2.43.0+, git starts supporting reading .gitattributes from HEAD ref.
	// Once git 2.43.0 is deployed, we can stop using info/attributes in related RPCs,
	// As a result, ApplyGitattributes() is made as a no-op,
	// so that Gitaly clients will stop writing to info/attributes;
	// This gRPC will be totally removed in the once all the housekeeping on removing info/attributes is done.
	return &gitalypb.ApplyGitattributesResponse{}, nil
}
