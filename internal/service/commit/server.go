package commit

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"golang.org/x/net/context"
)

type server struct{}

func (s *server) CommitLanguages(ctx context.Context, r *pb.CommitLanguagesRequest) (*pb.CommitLanguagesResponse, error) {
	return nil, nil
}

// NewServer creates a new instance of a grpc CommitServiceServer
func NewServer() pb.CommitServiceServer {
	return &server{}
}
