package diff

import (
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

const msgSizeThreshold = 5 * 1024

type server struct {
	MsgSizeThreshold int
	gitalypb.UnimplementedDiffServiceServer
}

// NewServer creates a new instance of a gRPC DiffServer
func NewServer() gitalypb.DiffServiceServer {
	return &server{
		MsgSizeThreshold: msgSizeThreshold,
	}
}
