package diff

import (
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

const msgSizeThreshold = 5 * 1024

type server struct {
	MsgSizeThreshold int
	*rubyserver.Server
}

// NewServer creates a new instance of a gRPC DiffServer
func NewServer(rs *rubyserver.Server) gitalypb.DiffServiceServer {
	return &server{
		MsgSizeThreshold: msgSizeThreshold,
		Server:           rs,
	}
}
