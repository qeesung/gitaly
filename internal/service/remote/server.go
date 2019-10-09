package remote

import (
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct {
	ruby *rubyserver.Server
	gitalypb.UnimplementedRemoteServiceServer
}

// NewServer creates a new instance of a grpc RemoteServiceServer
func NewServer(rs *rubyserver.Server) gitalypb.RemoteServiceServer {
	return &server{ruby: rs}
}
