package operations

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/internal/git/quarantine"
	"gitlab.com/gitlab-org/gitaly/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

//nolint: revive,stylecheck // This is unintentionally missing documentation.
type Server struct {
	gitalypb.UnimplementedOperationServiceServer
	hookManager    hook.Manager
	txManager      transaction.Manager
	locator        storage.Locator
	conns          *client.Pool
	git2goExecutor *git2go.Executor
	gitCmdFactory  git.CommandFactory
	catfileCache   catfile.Cache
	updater        *updateref.UpdaterWithHooks
}

// NewServer creates a new instance of a grpc OperationServiceServer
func NewServer(
	hookManager hook.Manager,
	txManager transaction.Manager,
	locator storage.Locator,
	conns *client.Pool,
	git2goExecutor *git2go.Executor,
	gitCmdFactory git.CommandFactory,
	catfileCache catfile.Cache,
	updater *updateref.UpdaterWithHooks,
) *Server {
	return &Server{
		hookManager:    hookManager,
		txManager:      txManager,
		locator:        locator,
		conns:          conns,
		git2goExecutor: git2goExecutor,
		gitCmdFactory:  gitCmdFactory,
		catfileCache:   catfileCache,
		updater:        updater,
	}
}

func (s *Server) localrepo(repo repository.GitRepo) *localrepo.Repo {
	return localrepo.New(s.locator, s.gitCmdFactory, s.catfileCache, repo)
}

func (s *Server) quarantinedRepo(
	ctx context.Context, repo *gitalypb.Repository,
) (*quarantine.Dir, *localrepo.Repo, error) {
	quarantineDir, err := quarantine.New(ctx, repo, s.locator)
	if err != nil {
		return nil, nil, helper.ErrInternalf("creating object quarantine: %w", err)
	}

	quarantineRepo := s.localrepo(quarantineDir.QuarantinedRepo())

	return quarantineDir, quarantineRepo, nil
}
