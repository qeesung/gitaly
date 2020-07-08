package remote

import (
	"gitlab.com/gitlab-org/gitaly/internal/connection"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct {
	ruby *rubyserver.Server
	gitalypb.UnimplementedRemoteServiceServer

	conns *connection.Pool
}

// NewServer creates a new instance of a grpc RemoteServiceServer
func NewServer(rs *rubyserver.Server) gitalypb.RemoteServiceServer {
	return &server{
		ruby:  rs,
		conns: connection.NewPool(),
	}
}
