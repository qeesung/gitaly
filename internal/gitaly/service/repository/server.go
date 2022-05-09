package repository

import (
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedRepositoryServiceServer
	ruby                *rubyserver.Server
	conns               *client.Pool
	locator             storage.Locator
	txManager           transaction.Manager
	gitCmdFactory       git.CommandFactory
	cfg                 config.Cfg
	binDir              string
	loggingCfg          config.Logging
	catfileCache        catfile.Cache
	git2goExecutor      *git2go.Executor
	housekeepingManager housekeeping.Manager
}

// NewServer creates a new instance of a gRPC repo server
func NewServer(
	cfg config.Cfg,
	rs *rubyserver.Server,
	locator storage.Locator,
	txManager transaction.Manager,
	gitCmdFactory git.CommandFactory,
	catfileCache catfile.Cache,
	connsPool *client.Pool,
	git2goExecutor *git2go.Executor,
	housekeepingManager housekeeping.Manager,
) gitalypb.RepositoryServiceServer {
	return &server{
		ruby:                rs,
		locator:             locator,
		txManager:           txManager,
		gitCmdFactory:       gitCmdFactory,
		conns:               connsPool,
		cfg:                 cfg,
		binDir:              cfg.BinDir,
		loggingCfg:          cfg.Logging,
		catfileCache:        catfileCache,
		git2goExecutor:      git2goExecutor,
		housekeepingManager: housekeepingManager,
	}
}

func (s *server) localrepo(repo repository.GitRepo) *localrepo.Repo {
	return localrepo.New(s.locator, s.gitCmdFactory, s.catfileCache, repo)
}
