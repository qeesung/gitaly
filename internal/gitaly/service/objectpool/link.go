package objectpool

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) LinkRepositoryToObjectPool(ctx context.Context, req *gitalypb.LinkRepositoryToObjectPoolRequest) (*gitalypb.LinkRepositoryToObjectPoolResponse, error) {
	if req.GetRepository() == nil {
		return nil, status.Error(codes.InvalidArgument, "no repository")
	}

	pool, err := s.poolForRequest(req)
	if err != nil {
		return nil, err
	}

	repo := s.localrepo(req.GetRepository())

	if err := pool.Link(ctx, repo); err != nil {
		return nil, helper.ErrInternal(err)
	}

	return &gitalypb.LinkRepositoryToObjectPoolResponse{}, nil
}
