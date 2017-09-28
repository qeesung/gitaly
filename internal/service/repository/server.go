package repository

import (
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

type server struct {
	*rubyserver.Server
}

// NewServer creates a new instance of a gRPC repo server
func NewServer(rs *rubyserver.Server) pb.RepositoryServiceServer {
	return &server{rs}
}
