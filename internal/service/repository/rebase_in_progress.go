package repository

import (
	"fmt"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) IsRebaseInProgress(ctx context.Context, req *pb.IsRebaseInProgressRequest) (*pb.IsRebaseInProgressResponse, error) {
	if err := validateIsRebaseInProgressRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "IsRebaseInProgress: %v", err)
	}

	client, err := s.RepositoryServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.IsRebaseInProgress(clientCtx, req)
}

func validateIsRebaseInProgressRequest(req *pb.IsRebaseInProgressRequest) error {
	if req.GetRepository() == nil {
		return fmt.Errorf("empty Repository")
	}

	if req.GetRebaseId() == "" {
		return fmt.Errorf("empty RebaseId")
	}

	return nil
}
