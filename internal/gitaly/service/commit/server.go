package commit

import (
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/linguist"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedCommitServiceServer
	locator       storage.Locator
	gitCmdFactory git.CommandFactory
	linguist      *linguist.Instance
	catfileCache  catfile.Cache
}

// NewServer creates a new instance of a grpc CommitServiceServer
func NewServer(
	locator storage.Locator,
	gitCmdFactory git.CommandFactory,
	ling *linguist.Instance,
	catfileCache catfile.Cache,
) gitalypb.CommitServiceServer {
	return &server{
		locator:       locator,
		gitCmdFactory: gitCmdFactory,
		linguist:      ling,
		catfileCache:  catfileCache,
	}
}

func (s *server) localrepo(repo repository.GitRepo) *localrepo.Repo {
	return localrepo.New(s.locator, s.gitCmdFactory, s.catfileCache, repo)
}
