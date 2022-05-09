package objectpool

import (
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedObjectPoolServiceServer
	locator             storage.Locator
	gitCmdFactory       git.CommandFactory
	catfileCache        catfile.Cache
	txManager           transaction.Manager
	housekeepingManager housekeeping.Manager
}

// NewServer creates a new instance of a gRPC repo server
func NewServer(
	locator storage.Locator,
	gitCmdFactory git.CommandFactory,
	catfileCache catfile.Cache,
	txManager transaction.Manager,
	housekeepingManager housekeeping.Manager,
) gitalypb.ObjectPoolServiceServer {
	return &server{
		locator:             locator,
		gitCmdFactory:       gitCmdFactory,
		catfileCache:        catfileCache,
		txManager:           txManager,
		housekeepingManager: housekeepingManager,
	}
}

func (s *server) localrepo(repo repository.GitRepo) *localrepo.Repo {
	return localrepo.New(s.locator, s.gitCmdFactory, s.catfileCache, repo)
}
