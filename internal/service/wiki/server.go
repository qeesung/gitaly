package wiki

import (
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct {
	ruby *rubyserver.Server
	gitalypb.UnimplementedWikiServiceServer
}

// NewServer creates a new instance of a grpc WikiServiceServer
func NewServer(rs *rubyserver.Server) gitalypb.WikiServiceServer {
	return &server{ruby: rs}
}
