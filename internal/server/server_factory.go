package server

import (
	"net"
	"sync"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/service/hook"
	"google.golang.org/grpc"
)

// GitalyServerFactory is a factory of gitaly grpc servers
type GitalyServerFactory struct {
	mtx              sync.Mutex
	ruby             *rubyserver.Server
	gitlabAPI        hook.GitlabAPI
	secure, insecure []*grpc.Server
}

// NewGitalyServerFactory allows to create and start secure/insecure 'grpc.Server'-s with gitaly-ruby
// server shared in between.
func NewGitalyServerFactory(api hook.GitlabAPI) *GitalyServerFactory {
	return &GitalyServerFactory{ruby: &rubyserver.Server{}, gitlabAPI: api}
}

// StartRuby starts the ruby process
func (s *GitalyServerFactory) StartRuby() error {
	return s.ruby.Start()
}

// Stop stops all servers started by calling Serve and the gitaly-ruby server.
func (s *GitalyServerFactory) Stop() {
	for _, srv := range s.all() {
		srv.Stop()
	}

	s.ruby.Stop()
	CleanupInternalSocketDir()
}

// GracefulStop stops both the secure and insecure servers gracefully
func (s *GitalyServerFactory) GracefulStop() {
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

// Serve starts serving on the provided listener with newly created grpc.Server
func (s *GitalyServerFactory) Serve(l net.Listener, secure bool) error {
	srv := s.create(secure)

	return srv.Serve(l)
}

func (s *GitalyServerFactory) create(secure bool) *grpc.Server {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	if secure {
		s.secure = append(s.secure, NewSecure(s.ruby, s.gitlabAPI, config.Config))
		return s.secure[len(s.secure)-1]
	}

	s.insecure = append(s.insecure, NewInsecure(s.ruby, s.gitlabAPI, config.Config))

	return s.insecure[len(s.insecure)-1]
}

func (s *GitalyServerFactory) all() []*grpc.Server {
	s.mtx.Lock()
	defer s.mtx.Unlock()

	return append(s.secure[:], s.insecure...)
}
