package repository

import pb "gitlab.com/gitlab-org/gitaly-proto/go"

type server struct{}

// NewServer creates a new instance of a gRPC repo server
func NewServer() pb.RepositoryServiceServer {
	return &server{}
}
