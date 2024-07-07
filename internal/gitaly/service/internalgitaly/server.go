package internalgitaly

import (
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type server struct {
	gitalypb.UnimplementedInternalGitalyServer
	logger        log.Logger
	storages      []config.Storage
	locator       storage.Locator
	gitCmdFactory git.CommandFactory
}

// NewServer return an instance of the Gitaly service.
func NewServer(deps *service.Dependencies) gitalypb.InternalGitalyServer {
	return &server{
		logger:        deps.GetLogger(),
		storages:      deps.GetCfg().Storages,
		locator:       deps.GetLocator(),
		gitCmdFactory: deps.GetGitCmdFactory(),
	}
}
