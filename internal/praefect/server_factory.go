package praefect

import (
	"net"
	"sync"

	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/transactions"
	"google.golang.org/grpc"
)

// NewServerFactory returns factory object for initialization of praefect gRPC servers.
func NewServerFactory(
	conf config.Config,
	logger *logrus.Entry,
	director proxy.StreamDirector,
	nodeMgr nodes.Manager,
	txMgr *transactions.Manager,
	queue datastore.ReplicationEventQueue,
	registry *protoregistry.Registry,
) *ServerFactory {
	return &ServerFactory{
		conf:     conf,
		logger:   logger,
		registry: registry,
		director: director,
		nodeMgr:  nodeMgr,
		txMgr:    txMgr,
		queue:    queue,
	}
}

// ServerFactory is a factory of praefect grpc servers
type ServerFactory struct {
	mtx      sync.Mutex
	conf     config.Config
	logger   *logrus.Entry
	registry *protoregistry.Registry
	director proxy.StreamDirector
	nodeMgr  nodes.Manager
	txMgr    *transactions.Manager
	queue    datastore.ReplicationEventQueue
	insecure []*grpc.Server
}

// Serve starts serving on the provided listener with newly created grpc.Server
func (s *ServerFactory) Serve(l net.Listener, secure bool) error {
	srv, err := s.create()
	if err != nil {
		return err
	}

	return srv.Serve(l)
}

// Stop stops both the secure and insecure servers.
func (s *ServerFactory) Stop() {
	for _, srv := range s.all() {
		srv.Stop()
	}
}

// GracefulStop stops both the secure and insecure servers gracefully.
func (s *ServerFactory) GracefulStop() {
	wg := sync.WaitGroup{}

	for _, srv := range s.all() {
		wg.Add(1)

		go func(s *grpc.Server) {
			s.GracefulStop()
			wg.Done()
		}(srv)
	}

	wg.Wait()
}

func (s *ServerFactory) create() (*grpc.Server, error) {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	s.insecure = append(s.insecure, s.createGRPC())

	return s.insecure[len(s.insecure)-1], nil
}

func (s *ServerFactory) createGRPC(grpcOpts ...grpc.ServerOption) *grpc.Server {
	return NewGRPCServer(
		s.conf,
		s.logger,
		s.registry,
		s.director,
		s.nodeMgr,
		s.txMgr,
		s.queue,
		grpcOpts...,
	)
}

func (s *ServerFactory) all() []*grpc.Server {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	var servers []*grpc.Server

	if s.insecure != nil {
		servers = append(servers, s.insecure...)
	}

	return servers
}
