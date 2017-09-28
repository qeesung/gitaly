package operations

import (
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
)

func (s *server) UserDeleteTag(ctx context.Context, req *pb.UserDeleteTagRequest) (*pb.UserDeleteTagResponse, error) {
	client, err := s.OperationServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.UserDeleteTag(clientCtx, req)
}

func (s *server) UserCreateTag(ctx context.Context, req *pb.UserCreateTagRequest) (*pb.UserCreateTagResponse, error) {
	client, err := s.OperationServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.UserCreateTag(clientCtx, req)
}
