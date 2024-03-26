package smarthttp

import (
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bundleuri"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedSmartHTTPServiceServer
	logger                     log.Logger
	locator                    storage.Locator
	gitCmdFactory              git.CommandFactory
	packfileNegotiationMetrics *prometheus.CounterVec
	infoRefCache               infoRefCache
	txManager                  transaction.Manager
	backupLocator              backup.Locator
	backupSink                 backup.Sink
	bundleURISink              *bundleuri.Sink
}

// NewServer creates a new instance of a grpc SmartHTTPServer
func NewServer(deps *service.Dependencies, serverOpts ...ServerOpt) gitalypb.SmartHTTPServiceServer {
	s := &server{
		logger:        deps.GetLogger(),
		locator:       deps.GetLocator(),
		gitCmdFactory: deps.GetGitCmdFactory(),
		txManager:     deps.GetTxManager(),
		packfileNegotiationMetrics: prometheus.NewCounterVec(
			prometheus.CounterOpts{},
			[]string{"git_negotiation_feature"},
		),
		infoRefCache:  newInfoRefCache(deps.GetLogger(), deps.GetDiskCache()),
		backupLocator: deps.GetBackupLocator(),
		backupSink:    deps.GetBackupSink(),
		bundleURISink: deps.GetBundleURISink(),
	}

	for _, serverOpt := range serverOpts {
		serverOpt(s)
	}

	return s
}

// ServerOpt is a self referential option for server
type ServerOpt func(s *server)

//nolint:revive // This is unintentionally missing documentation.
func WithPackfileNegotiationMetrics(c *prometheus.CounterVec) ServerOpt {
	return func(s *server) {
		s.packfileNegotiationMetrics = c
	}
}
