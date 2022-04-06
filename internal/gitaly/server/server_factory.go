package server

import (
	"sync"

	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v14/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v14/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/middleware/limithandler"
	"google.golang.org/grpc"
)

// GitalyServerFactory is a factory of gitaly grpc servers
type GitalyServerFactory struct {
	registry         *backchannel.Registry
	cacheInvalidator cache.Invalidator
	limitHandlers    []*limithandler.LimiterMiddleware
	cfg              config.Cfg
	logger           *logrus.Entry
	externalServers  []*grpc.Server
	internalServers  []*grpc.Server
}

// NewGitalyServerFactory allows to create and start secure/insecure 'grpc.Server'-s with gitaly-ruby
// server shared in between.
func NewGitalyServerFactory(
	cfg config.Cfg,
	logger *logrus.Entry,
	registry *backchannel.Registry,
	cacheInvalidator cache.Invalidator,
	limitHandlers []*limithandler.LimiterMiddleware,
) *GitalyServerFactory {
	return &GitalyServerFactory{
		cfg:              cfg,
		logger:           logger,
		registry:         registry,
		cacheInvalidator: cacheInvalidator,
		limitHandlers:    limitHandlers,
	}
}

// Stop immediately stops all servers created by the GitalyServerFactory.
func (s *GitalyServerFactory) Stop() {
	for _, servers := range [][]*grpc.Server{
		s.externalServers,
		s.internalServers,
	} {
		for _, server := range servers {
			server.Stop()
		}
	}
}

// GracefulStop gracefully stops all servers created by the GitalyServerFactory. ExternalServers
// are stopped before the internal servers to ensure any RPCs accepted by the externals servers
// can still complete their requests to the internal servers. This is important for hooks calling
// back to Gitaly.
func (s *GitalyServerFactory) GracefulStop() {
	for _, servers := range [][]*grpc.Server{
		s.externalServers,
		s.internalServers,
	} {
		var wg sync.WaitGroup

		for _, server := range servers {
			wg.Add(1)
			go func(server *grpc.Server) {
				defer wg.Done()
				server.GracefulStop()
			}(server)
		}

		wg.Wait()
	}
}

// CreateExternal creates a new external gRPC server. The external servers are closed
// before the internal servers when gracefully shutting down.
func (s *GitalyServerFactory) CreateExternal(secure bool) (*grpc.Server, error) {
	server, err := New(
		secure,
		s.cfg,
		s.logger,
		s.registry,
		s.cacheInvalidator,
		s.limitHandlers,
	)
	if err != nil {
		return nil, err
	}

	s.externalServers = append(s.externalServers, server)
	return server, nil
}

// CreateInternal creates a new internal gRPC server. Internal servers are closed
// after the external ones when gracefully shutting down.
func (s *GitalyServerFactory) CreateInternal() (*grpc.Server, error) {
	server, err := New(
		false,
		s.cfg,
		s.logger,
		s.registry,
		s.cacheInvalidator,
		s.limitHandlers)
	if err != nil {
		return nil, err
	}

	s.internalServers = append(s.internalServers, server)
	return server, nil
}
