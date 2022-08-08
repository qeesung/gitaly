package server

import (
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

// Server is a ServerService server
type Server struct {
	gitalypb.UnimplementedServerServiceServer
	conf   config.Config
	conns  service.Connections
	checks []service.CheckFunc
}

// NewServer creates a new instance of a grpc ServerServiceServer
func NewServer(conf config.Config, conns service.Connections, checks []service.CheckFunc) gitalypb.ServerServiceServer {
	s := &Server{
		conf:   conf,
		conns:  conns,
		checks: checks,
	}

	return s
}
