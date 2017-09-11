package diff

import (
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
)

const msgSizeThreshold = 5 * 1024

type server struct {
	MsgSizeThreshold int
	*rubyserver.Server
}

// NewServer creates a new instance of a gRPC DiffServer
func NewServer(rs *rubyserver.Server) pb.DiffServiceServer {
	return &server{
		MsgSizeThreshold: msgSizeThreshold,
		Server:           rs,
	}
}
