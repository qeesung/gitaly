package repository

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"

	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
)

func (s *server) Fsck(ctx context.Context, req *pb.FsckRequest) (*pb.FsckResponse, error) {
	client, err := s.RepositoryServiceClient(ctx)
	if err != nil {
		return nil, err
	}

	clientCtx, err := rubyserver.SetHeaders(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	return client.Fsck(clientCtx, req)
}
