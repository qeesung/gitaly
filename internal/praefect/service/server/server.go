package server

import (
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/conn"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// Server is a ServerService server
type Server struct {
	clientCC *conn.ClientConnections
	conf     config.Config
}

// NewServer creates a new instance of a grpc ServerServiceServer
func NewServer(conf config.Config, clientConnections *conn.ClientConnections) gitalypb.ServerServiceServer {
	s := &Server{
		clientCC: clientConnections,
		conf:     conf,
	}

	return s
}
