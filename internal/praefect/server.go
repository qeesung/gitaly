/*Package praefect is a Gitaly reverse proxy for transparently routing gRPC
calls to a set of Gitaly services.*/
package praefect

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/mwitkow/grpc-proxy/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Logger is a simple interface that allows loggers to be dependency injected
// into the praefect server
type Logger interface {
	Debugf(format string, args ...interface{})
}

// Coordinator takes care of directing client requests to the appropriate
// downstream server. The coordinator is thread safe; concurrent calls to
// register nodes are safe.
type Coordinator struct {
	log   Logger
	lock  sync.RWMutex
	nodes map[string]*grpc.ClientConn
}

// newCoordinator returns a new Coordinator that utilizes the provided logger
func newCoordinator(l Logger) *Coordinator {
	return &Coordinator{
		log:   l,
		nodes: make(map[string]*grpc.ClientConn),
	}
}

// streamDirector determines which downstream servers receive requests
func (c *Coordinator) streamDirector(ctx context.Context, fullMethodName string) (context.Context, *grpc.ClientConn, error) {
	// For phase 1, we need to route messages based on the storage location
	// to the appropriate Gitaly node.
	c.log.Debugf("Stream director received method %s", fullMethodName)

	// TODO: obtain storage location dynamically from RPC request message
	storageLoc := "test"

	c.lock.RLock()
	cc, ok := c.nodes[storageLoc]
	c.lock.RUnlock()

	if !ok {
		err := status.Error(
			codes.FailedPrecondition,
			fmt.Sprintf("no downstream node for storage location %q", storageLoc),
		)
		return nil, nil, err
	}

	return ctx, cc, nil
}

// Server is a praefect server
type Server struct {
	*Coordinator
	s *grpc.Server
}

// NewServer returns an initialized praefect gPRC proxy server configured
// with the provided gRPC server options
func NewServer(grpcOpts []grpc.ServerOption, l Logger) *Server {
	c := newCoordinator(l)
	grpcOpts = append(grpcOpts, proxyRequiredOpts(c.streamDirector)...)

	return &Server{
		s:           grpc.NewServer(grpcOpts...),
		Coordinator: c,
	}
}

// ErrStorageLocExists indicates a storage location has already been registered
// in the proxy for a downstream Gitaly node
var ErrStorageLocExists = errors.New("storage location already registered")

// RegisterNode will direct traffic to the supplied downstream connection when the storage location
// is encountered.
//
// TODO: Coordinator probably needs to handle dialing, or another entity
// needs to handle dialing to ensure keep alives and redialing logic
// exist for when downstream connections are severed.
func (c *Coordinator) RegisterNode(storageLoc string, node *grpc.ClientConn) {
	c.lock.Lock()
	c.nodes[storageLoc] = node
	c.lock.Unlock()
}

func proxyRequiredOpts(director proxy.StreamDirector) []grpc.ServerOption {
	return []grpc.ServerOption{
		grpc.CustomCodec(proxy.Codec()),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(director)),
	}
}

// Start will start the praefect gRPC proxy server listening at the provided
// listener. Function will block until the server is stopped or an
// unrecoverable error occurs.
func (srv *Server) Start(lis net.Listener) error {
	return srv.s.Serve(lis)
}

// Shutdown will attempt a graceful shutdown of the grpc server. If unable
// to gracefully shutdown within the context deadline, it will then
// forcefully shutdown the server and return a context cancellation error.
func (srv *Server) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		srv.s.GracefulStop()
		close(done)
	}()

	select {
	case <-ctx.Done():
		srv.s.Stop()
		return ctx.Err()
	case <-done:
		return nil
	}
}
