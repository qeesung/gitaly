package repository

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"golang.org/x/net/context"
)

type server struct{}

// NewServer creates a new instance of a gRPC repo server
func NewServer() pb.RepositoryServiceServer {
	return &server{}
}

func (s *server) ApplyGitattributes(ctx context.Context, in *pb.ApplyGitattributesRequest) (*pb.ApplyGitattributesResponse, error) {
	return nil, nil
}
