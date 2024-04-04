package objectpool

import (
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	housekeepingmgr "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/manager"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedObjectPoolServiceServer
	logger              log.Logger
	locator             storage.Locator
	gitCmdFactory       git.CommandFactory
	catfileCache        catfile.Cache
	txManager           transaction.Manager
	walPartitionManager *storagemgr.PartitionManager
	housekeepingManager housekeepingmgr.Manager
	repositoryCounter   *counter.RepositoryCounter
}

// NewServer creates a new instance of a gRPC repo server
func NewServer(deps *service.Dependencies) gitalypb.ObjectPoolServiceServer {
	return &server{
		logger:              deps.GetLogger(),
		locator:             deps.GetLocator(),
		gitCmdFactory:       deps.GetGitCmdFactory(),
		catfileCache:        deps.GetCatfileCache(),
		txManager:           deps.GetTxManager(),
		walPartitionManager: deps.GetPartitionManager(),
		housekeepingManager: deps.GetHousekeepingManager(),
		repositoryCounter:   deps.GetRepositoryCounter(),
	}
}

func (s *server) localrepo(repo storage.Repository) *localrepo.Repo {
	return localrepo.New(s.logger, s.locator, s.gitCmdFactory, s.catfileCache, repo)
}
