package ref

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

type server struct{}

// NewServer creates a new instance of a grpc RefServer
func NewServer() pb.RefServiceServer {
	return &server{}
}
