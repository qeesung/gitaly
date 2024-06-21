package smarthttp

import (
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bundleuri"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedSmartHTTPServiceServer
	logger                     log.Logger
	cfg                        config.Cfg
	locator                    storage.Locator
	gitCmdFactory              git.CommandFactory
	catfileCache               catfile.Cache
	packfileNegotiationMetrics *prometheus.CounterVec
	infoRefCache               infoRefCache
	txManager                  transaction.Manager
	txRegistry                 *storagemgr.TransactionRegistry
	hookManager                hook.Manager
	updater                    *updateref.UpdaterWithHooks
	backupLocator              backup.Locator
	backupSink                 backup.Sink
	bundleURISink              *bundleuri.Sink
	inflightTracker            *service.InProgressTracker
	generateBundles            bool
	partitionMgr               *storagemgr.PartitionManager
	transactionRegistry        *storagemgr.TransactionRegistry
}

// NewServer creates a new instance of a grpc SmartHTTPServer
func NewServer(deps *service.Dependencies, serverOpts ...ServerOpt) gitalypb.SmartHTTPServiceServer {
	s := &server{
		logger:        deps.GetLogger(),
		cfg:           deps.GetCfg(),
		locator:       deps.GetLocator(),
		gitCmdFactory: deps.GetGitCmdFactory(),
		catfileCache:  deps.GetCatfileCache(),
		txManager:     deps.GetTxManager(),
		txRegistry:    deps.GetTransactionRegistry(),
		hookManager:   deps.GetHookManager(),
		updater:       deps.GetUpdaterWithHooks(),
		packfileNegotiationMetrics: prometheus.NewCounterVec(
			prometheus.CounterOpts{},
			[]string{"git_negotiation_feature"},
		),
		infoRefCache:        newInfoRefCache(deps.GetLogger(), deps.GetDiskCache()),
		backupLocator:       deps.GetBackupLocator(),
		backupSink:          deps.GetBackupSink(),
		bundleURISink:       deps.GetBundleURISink(),
		inflightTracker:     deps.GetInProgressTracker(),
		generateBundles:     deps.GetCfg().BundleURI.Autogeneration,
		partitionMgr:        deps.GetPartitionManager(),
		transactionRegistry: deps.GetTransactionRegistry(),
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

func (s *server) localrepo(repo storage.Repository) *localrepo.Repo {
	return localrepo.New(s.logger, s.locator, s.gitCmdFactory, s.catfileCache, repo)
}
