package localrepo

import (
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// Factory builds Repo instances.
type Factory struct {
	logger       log.Logger
	locator      storage.Locator
	cmdFactory   git.CommandFactory
	catfileCache catfile.Cache
}

// StorageScopedFactory builds Repo instances for a given storage. The storage has been
// checked to exist.
type StorageScopedFactory struct {
	factory Factory
	storage string
}

// NewFactory returns a factory type that builds Repo instances. It helps avoid having to drill down Repo
// dependencies to all call sites that need to build a Repo.
func NewFactory(logger log.Logger, locator storage.Locator, cmdFactory git.CommandFactory, catfileCache catfile.Cache) Factory {
	return Factory{
		logger:       logger,
		locator:      locator,
		cmdFactory:   cmdFactory,
		catfileCache: catfileCache,
	}
}

// Build returns a Repo for the given repository.
func (f Factory) Build(repo *gitalypb.Repository) *Repo {
	return New(f.logger, f.locator, f.cmdFactory, f.catfileCache, repo)
}

// ScopeByStorage returns a StorageScopedFactory that builds Repo instances for a given storage
// from only a relative path. It checks the storage exists so the callers don't have to handle the error
// checking when building repositories.
func (f Factory) ScopeByStorage(ctx context.Context, storage string) (StorageScopedFactory, error) {
	// Check whether the storage exists so we know that the repositories are created successfully later.
	_, err := f.locator.GetStorageByName(ctx, storage)
	if err != nil {
		return StorageScopedFactory{}, fmt.Errorf("get storage by name: %w", err)
	}

	return StorageScopedFactory{
		factory: f,
		storage: storage,
	}, nil
}

// Build builds a Repo instance for the repository at relativePath in the storage.
func (f StorageScopedFactory) Build(relativePath string) *Repo {
	return f.factory.Build(&gitalypb.Repository{
		StorageName:  f.storage,
		RelativePath: relativePath,
	})
}
