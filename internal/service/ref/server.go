package ref

import (
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct {
	ruby *rubyserver.Server
	gitalypb.UnimplementedRefServiceServer
}

// NewServer creates a new instance of a grpc RefServer
func NewServer(rs *rubyserver.Server) gitalypb.RefServiceServer {
	return &server{ruby: rs}
}
