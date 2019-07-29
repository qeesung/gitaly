package server

import "gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"

type server struct{}

// NewServer creates a new instance of a grpc ServerServiceServer
func NewServer() gitalypb.ServerServiceServer {
	return &server{}
}
