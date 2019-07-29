package storage

import "gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"

type server struct{}

// NewServer creates a new instance of a gRPC storage server
func NewServer() gitalypb.StorageServiceServer {
	return &server{}
}
