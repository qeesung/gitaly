package smarthttp

import (
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/internal/storage"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct {
	locator                    storage.Locator
	infoRefCache               streamer
	packfileNegotiationMetrics *prometheus.CounterVec
}

// NewServer creates a new instance of a grpc SmartHTTPServer
func NewServer(locator storage.Locator, infoRefCache streamer, serverOpts ...ServerOpt) gitalypb.SmartHTTPServiceServer {
	s := &server{
		locator:      locator,
		infoRefCache: infoRefCache,
		packfileNegotiationMetrics: prometheus.NewCounterVec(
			prometheus.CounterOpts{},
			[]string{"git_negotiation_feature"},
		),
	}

	for _, serverOpt := range serverOpts {
		serverOpt(s)
	}

	return s
}

// ServerOpt is a self referential option for server
type ServerOpt func(s *server)

func WithPackfileNegotiationMetrics(c *prometheus.CounterVec) ServerOpt {
	return func(s *server) {
		s.packfileNegotiationMetrics = c
	}
}
