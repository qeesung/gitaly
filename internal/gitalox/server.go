/*Package gitalox is a Gitaly reverse proxy for transparently routing gRPC
calls to a set of Gitaly services.*/
package gitalox

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/mwitkow/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"google.golang.org/grpc"
)

// dummyRepoService is an implementation of gitalypb.RepositoryServiceServer
// that solely exists to register the gRPC proxy server for the handlers in the
// RepositoryServiceServer interface.
//
// Over time, the RepositoryServiceServer interface will grow and require
// updates. The dummy implmentation can be regenerated by running:
//
//   make dumb_repo_gen.go
type dummyRepoService struct{}

type Server struct {
	s        *grpc.Server
	director *director
}

// director takes care of directing client requests to the appropriate
// downstream server
type director struct {
	sync.RWMutex
	nodes map[string]gitalypb.RepositoryServiceClient
}

func newDirector() *director {
	return &director{
		nodes: make(map[string]gitalypb.RepositoryServiceClient),
	}
}

// streamDirector determines which downstream servers receive requests
func (d *director) streamDirector() proxy.StreamDirector {
	return func(ctx context.Context, fullMethodName string) (context.Context, *grpc.ClientConn, error) {
		// TODO
		return nil, nil, nil
	}
}

// NewServer returns an initialized Gitalox proxy server for the given gRPC
// server
func NewServer(grpcOpts []grpc.ServerOption) *Server {
	dir := newDirector()
	grpcOpts = append(grpcOpts, proxyRequiredOpts(director)...)

	return &Server{
		s:        grpc.NewServer(opts...),
		director: newDirector(),
	}
}

// ErrStorageLocExists indicates a storage location has already been registered
// in the proxy for a downstream Gitaly node
var ErrStorageLocExists = errors.New("storage location already registered")

func (srv *Server) RegisterStorageNode(storageLoc string, node gitalypb.RepositoryServiceClient) error {
	srv.storage.RLock()
	_, ok := srv.storage.nodes[storageLoc]
	if ok {
		return ErrStorageLocExists
	}
	srv.storage.RUnlock()

	srv.storage.Lock()
	srv.storage.nodes[storageLoc] = node
	srv.storage.Unlock()

	return nil
}

func proxyRequiredOpts(director proxy.StreamDirector) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.CustomCodec(proxy.Codec()),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(director)),
	}
}

func (srv *Server) Start(ctx context.Context, lis net.Listener) error {
	gitalypb.RegisterRepositoryServiceServer(srv.s, &dummyRepoService{})
	srv.s.Serve(lis)

	return nil
}
