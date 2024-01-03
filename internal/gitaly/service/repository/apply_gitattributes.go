package repository

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const attributesFileMode os.FileMode = perm.SharedFile

func (s *server) ApplyGitattributes(ctx context.Context, in *gitalypb.ApplyGitattributesRequest) (*gitalypb.ApplyGitattributesResponse, error) {
	if bytes.EqualFold(in.GetRevision(), []byte("invalid")) {
		return nil, structerr.NewInvalidArgument("revision: %w", fmt.Errorf("invalid revision"))
	}

	return &gitalypb.ApplyGitattributesResponse{}, nil
}
