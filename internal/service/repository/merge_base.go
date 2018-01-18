package repository

import (
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *server) FindMergeBase(ctx context.Context, req *pb.FindMergeBaseRequest) (*pb.FindMergeBaseResponse, error) {
	if len(req.Revisions) != 2 {
		return nil, status.Errorf(codes.InvalidArgument, "FindMergeBase: 2 revisions are required")
	}

	client, err := s.RepositoryServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.FindMergeBase(clientCtx, req)
}
