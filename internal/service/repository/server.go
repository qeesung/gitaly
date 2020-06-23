package repository

import (
	"context"
	"sync"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/storage"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
)

type server struct {
	ruby *rubyserver.Server
	gitalypb.UnimplementedRepositoryServiceServer
	connsByAddress       map[string]*grpc.ClientConn
	connsMtx             sync.RWMutex
	internalGitalySocket string
	locator              storage.Locator
}

// NewServer creates a new instance of a gRPC repo server
func NewServer(rs *rubyserver.Server, locator storage.Locator, internalGitalySocket string) gitalypb.RepositoryServiceServer {
	return &server{ruby: rs, locator: locator, connsByAddress: make(map[string]*grpc.ClientConn), internalGitalySocket: internalGitalySocket}
}

func (*server) FetchHTTPRemote(context.Context, *gitalypb.FetchHTTPRemoteRequest) (*gitalypb.FetchHTTPRemoteResponse, error) {
	return nil, helper.Unimplemented
}
