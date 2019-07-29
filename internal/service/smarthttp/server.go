package smarthttp

import "gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"

type server struct{}

// NewServer creates a new instance of a grpc SmartHTTPServer
func NewServer() gitalypb.SmartHTTPServiceServer {
	return &server{}
}
