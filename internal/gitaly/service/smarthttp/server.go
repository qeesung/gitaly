package smarthttp

import (
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedSmartHTTPServiceServer
	locator                    storage.Locator
	gitCmdFactory              git.CommandFactory
	packfileNegotiationMetrics *prometheus.CounterVec
	infoRefCache               infoRefCache
	txManager                  transaction.Manager
}

// NewServer creates a new instance of a grpc SmartHTTPServer
func NewServer(
	locator storage.Locator,
	gitCmdFactory git.CommandFactory,
	txManager transaction.Manager,
	cache cache.Streamer,
	serverOpts ...ServerOpt,
) gitalypb.SmartHTTPServiceServer {
	s := &server{
		locator:       locator,
		gitCmdFactory: gitCmdFactory,
		txManager:     txManager,
		packfileNegotiationMetrics: prometheus.NewCounterVec(
			prometheus.CounterOpts{},
			[]string{"git_negotiation_feature"},
		),
		infoRefCache: newInfoRefCache(cache),
	}

	for _, serverOpt := range serverOpts {
		serverOpt(s)
	}

	return s
}

// ServerOpt is a self referential option for server
type ServerOpt func(s *server)

//nolint: stylecheck // This is unintentionally missing documentation.
func WithPackfileNegotiationMetrics(c *prometheus.CounterVec) ServerOpt {
	return func(s *server) {
		s.packfileNegotiationMetrics = c
	}
}
