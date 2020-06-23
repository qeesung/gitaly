package objectpool

import (
	"gitlab.com/gitlab-org/gitaly/internal/storage"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedObjectPoolServiceServer
	locator storage.Locator
}

// NewServer creates a new instance of a gRPC repo server
func NewServer(locator storage.Locator) gitalypb.ObjectPoolServiceServer {
	return &server{locator: locator}
}
