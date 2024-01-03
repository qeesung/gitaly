package repository

import (
	"context"
	"os"

	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const attributesFileMode os.FileMode = perm.SharedFile

func (s *server) ApplyGitattributes(ctx context.Context, in *gitalypb.ApplyGitattributesRequest) (*gitalypb.ApplyGitattributesResponse, error) {
	return nil, structerr.NewUnimplemented(
		"ApplyGitattributes is deprecated in git 2.43.0+ and soon will be removed")
}
