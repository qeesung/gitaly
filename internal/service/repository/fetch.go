package repository

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"

	"golang.org/x/net/context"
)

func (s *server) FetchSourceBranch(ctx context.Context, req *pb.FetchSourceBranchRequest) (*pb.FetchSourceBranchResponse, error) {
	return nil, helper.Unimplemented
}
