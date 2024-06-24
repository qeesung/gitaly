package repository

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/bundleuri"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	housekeepingmgr "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/manager"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/quarantine"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/unarycache"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedRepositoryServiceServer
	logger              log.Logger
	conns               *client.Pool
	locator             storage.Locator
	txManager           transaction.Manager
	walPartitionManager *storagemgr.PartitionManager
	gitCmdFactory       git.CommandFactory
	cfg                 config.Cfg
	loggingCfg          config.Logging
	catfileCache        catfile.Cache
	housekeepingManager housekeepingmgr.Manager
	backupSink          backup.Sink
	backupLocator       backup.Locator
	bundleURISink       *bundleuri.Sink
	repositoryCounter   *counter.RepositoryCounter

	licenseCache *unarycache.Cache[git.ObjectID, *gitalypb.FindLicenseResponse]
}

// NewServer creates a new instance of a gRPC repo server
func NewServer(deps *service.Dependencies) gitalypb.RepositoryServiceServer {
	return &server{
		logger:              deps.GetLogger(),
		locator:             deps.GetLocator(),
		txManager:           deps.GetTxManager(),
		walPartitionManager: deps.GetPartitionManager(),
		gitCmdFactory:       deps.GetGitCmdFactory(),
		conns:               deps.GetConnsPool(),
		cfg:                 deps.GetCfg(),
		loggingCfg:          deps.GetCfg().Logging,
		catfileCache:        deps.GetCatfileCache(),
		housekeepingManager: deps.GetHousekeepingManager(),
		backupSink:          deps.GetBackupSink(),
		backupLocator:       deps.GetBackupLocator(),
		bundleURISink:       deps.GetBundleURISink(),
		repositoryCounter:   deps.GetRepositoryCounter(),

		licenseCache: newLicenseCache(),
	}
}

func (s *server) localrepo(repo storage.Repository) *localrepo.Repo {
	return localrepo.New(s.logger, s.locator, s.gitCmdFactory, s.catfileCache, repo)
}

func (s *server) quarantinedRepo(
	ctx context.Context, repo *gitalypb.Repository,
) (*quarantine.Dir, *localrepo.Repo, func() error, error) {
	quarantineDir, cleanup, err := quarantine.New(ctx, repo, s.logger, s.locator)
	if err != nil {
		return nil, nil, nil, structerr.NewInternal("creating object quarantine: %w", err)
	}

	quarantineRepo := s.localrepo(quarantineDir.QuarantinedRepo())

	return quarantineDir, quarantineRepo, cleanup, nil
}
