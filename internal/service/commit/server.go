package commit

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

type server struct{}

// NewServer creates a new instance of a grpc CommitServiceServer
func NewServer() pb.CommitServiceServer {
	return &server{}
}
