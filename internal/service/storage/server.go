package storage

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

type server struct{}

// NewServer creates a new instance of a gRPC storage server
func NewServer() pb.StorageServiceServer {
	return &server{}
}
