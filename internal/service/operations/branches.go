package operations

import (
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
)

func (s *server) UserCreateBranch(ctx context.Context, req *pb.UserCreateBranchRequest) (*pb.UserCreateBranchResponse, error) {
	client, err := s.OperationServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.UserCreateBranch(clientCtx, req)
}

func (s *server) UserUpdateBranch(ctx context.Context, req *pb.UserUpdateBranchRequest) (*pb.UserUpdateBranchResponse, error) {
	return nil, helper.Unimplemented
}

func (s *server) UserDeleteBranch(ctx context.Context, req *pb.UserDeleteBranchRequest) (*pb.UserDeleteBranchResponse, error) {
	if len(req.BranchName) == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "Bad Request (empty branch name)")
	}

	if req.User == nil {
		return nil, status.Errorf(codes.InvalidArgument, "Bad Request (empty user)")
	}

	client, err := s.OperationServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.UserDeleteBranch(clientCtx, req)
}
