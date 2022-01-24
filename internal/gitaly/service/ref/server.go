package ref

import (
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedRefServiceServer
	txManager     transaction.Manager
	locator       storage.Locator
	gitCmdFactory git.CommandFactory
	catfileCache  catfile.Cache
}

// NewServer creates a new instance of a grpc RefServer
func NewServer(
	locator storage.Locator,
	gitCmdFactory git.CommandFactory,
	txManager transaction.Manager,
	catfileCache catfile.Cache,
) gitalypb.RefServiceServer {
	return &server{
		txManager:     txManager,
		locator:       locator,
		gitCmdFactory: gitCmdFactory,
		catfileCache:  catfileCache,
	}
}

func (s *server) localrepo(repo repository.GitRepo) *localrepo.Repo {
	return localrepo.New(s.locator, s.gitCmdFactory, s.catfileCache, repo)
}
