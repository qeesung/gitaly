package info

import (
	"context"

	"github.com/golang/protobuf/ptypes"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func (s *Server) DatalossCheck(ctx context.Context, req *gitalypb.DatalossCheckRequest) (*gitalypb.DatalossCheckResponse, error) {
	from, err := ptypes.Timestamp(req.GetFrom())
	if err != nil {
		return nil, helper.ErrInvalidArgumentf("invalid 'from': %v", err)
	}

	to, err := ptypes.Timestamp(req.GetTo())
	if err != nil {
		return nil, helper.ErrInvalidArgumentf("invalid 'to': %v", err)
	}

	dead, err := s.queue.CountDeadReplicationJobs(ctx, from, to)
	if err != nil {
		return nil, err
	}

	return &gitalypb.DatalossCheckResponse{ByRelativePath: dead}, nil
}
