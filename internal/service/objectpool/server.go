package objectpool

import (
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedObjectPoolServiceServer
}

// NewServer creates a new instance of a gRPC repo server
func NewServer() gitalypb.ObjectPoolServiceServer {
	return &server{}
}
