package info

import (
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// Server is a InfoService server
type Server struct{}

// NewServer creates a new instance of a grpc InfoServiceServer
func NewServer(conf config.Config) gitalypb.InfoServiceServer {
	s := &Server{}

	return s
}
