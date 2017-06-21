package smarthttp

import pb "gitlab.com/gitlab-org/gitaly-proto/go"

type server struct{}

// NewServer creates a new instance of a grpc SmartHTTPServer
func NewServer() pb.SmartHTTPServiceServer {
	return &server{}
}
