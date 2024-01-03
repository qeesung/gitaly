package repository

import (
	"context"
	"os"

	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const attributesFileMode os.FileMode = perm.SharedFile

func (s *server) ApplyGitattributes(ctx context.Context, in *gitalypb.ApplyGitattributesRequest) (*gitalypb.ApplyGitattributesResponse, error) {
	return &gitalypb.ApplyGitattributesResponse{}, nil
}
