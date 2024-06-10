package objectpool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	housekeepingmgr "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/manager"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

var (
	// ErrInvalidPoolDir is returned when the object pool relative path is malformed.
	ErrInvalidPoolDir = errors.New("invalid object pool directory")

	// ErrInvalidPoolRepository indicates the directory the alternates file points to is not a valid git repository
	ErrInvalidPoolRepository = errors.New("object pool is not a valid git repository")

	// ErrAlternateObjectDirNotExist indicates a repository does not have an alternates file
	ErrAlternateObjectDirNotExist = errors.New("no alternates directory exists")
)

// ObjectPool are a way to de-dupe objects between repositories, where the objects
// live in a pool in a distinct repository which is used as an alternate object
// store for other repositories.
type ObjectPool struct {
	*localrepo.Repo

	logger              log.Logger
	locator             storage.Locator
	gitCmdFactory       git.CommandFactory
	txManager           transaction.Manager
	housekeepingManager housekeepingmgr.Manager
}

// FromProto returns an object pool object from its Protobuf representation. This function verifies
// that the object pool exists and is a valid pool repository.
func FromProto(
	ctx context.Context,
	logger log.Logger,
	locator storage.Locator,
	gitCmdFactory git.CommandFactory,
	catfileCache catfile.Cache,
	txManager transaction.Manager,
	housekeepingManager housekeepingmgr.Manager,
	proto *gitalypb.ObjectPool,
) (*ObjectPool, error) {
	poolPath, err := locator.GetRepoPath(ctx, proto.GetRepository(), storage.WithRepositoryVerificationSkipped())
	if err != nil {
		return nil, err
	}

	if !storage.IsPoolRepository(proto.GetRepository()) {
		// When creating repositories in the ObjectPool service we will first create the
		// repository in a temporary directory. So we need to check whether the path we see
		// here is in such a temporary directory and let it pass.
		tempDir, err := locator.TempDir(proto.GetRepository().GetStorageName())
		if err != nil {
			return nil, fmt.Errorf("getting temporary storage directory: %w", err)
		}

		if !strings.HasPrefix(poolPath, tempDir) {
			return nil, ErrInvalidPoolDir
		}
	}

	pool := &ObjectPool{
		Repo:                localrepo.New(logger, locator, gitCmdFactory, catfileCache, proto.GetRepository()),
		logger:              logger,
		locator:             locator,
		gitCmdFactory:       gitCmdFactory,
		txManager:           txManager,
		housekeepingManager: housekeepingManager,
	}

	if !pool.IsValid(ctx) {
		return nil, ErrInvalidPoolRepository
	}

	return pool, nil
}

// ToProto returns a new struct that is the protobuf definition of the ObjectPool
func (o *ObjectPool) ToProto() *gitalypb.ObjectPool {
	return &gitalypb.ObjectPool{
		Repository: &gitalypb.Repository{
			StorageName:  o.GetStorageName(),
			RelativePath: o.GetRelativePath(),
		},
	}
}

// Exists will return true if the pool path exists and is a directory
func (o *ObjectPool) Exists(ctx context.Context) bool {
	path, err := o.Path(ctx)
	if err != nil {
		return false
	}

	fi, err := os.Stat(path)
	if os.IsNotExist(err) || err != nil {
		return false
	}

	return fi.IsDir()
}

// IsValid checks if a repository exists, and if its valid.
func (o *ObjectPool) IsValid(ctx context.Context) bool {
	return o.locator.ValidateRepository(ctx, o.Repo) == nil
}

// Remove will remove the pool, and all its contents without preparing and/or
// updating the repositories depending on this object pool
// Subdirectories will remain to exist, and will never be cleaned up, even when
// these are empty.
func (o *ObjectPool) Remove(ctx context.Context) (err error) {
	path, err := o.Path(ctx)
	if err != nil {
		return nil
	}

	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove all: %w", err)
	}

	if err := safe.NewSyncer().SyncParent(path); err != nil {
		return fmt.Errorf("sync parent: %w", err)
	}

	return nil
}

// FromRepo returns an instance of ObjectPool that the repository points to
func FromRepo(
	ctx context.Context,
	logger log.Logger,
	locator storage.Locator,
	gitCmdFactory git.CommandFactory,
	catfileCache catfile.Cache,
	txManager transaction.Manager,
	housekeepingManager housekeepingmgr.Manager,
	repo *localrepo.Repo,
) (*ObjectPool, error) {
	storagePath, err := locator.GetStorageByName(ctx, repo.GetStorageName())
	if err != nil {
		return nil, err
	}

	repoPath, err := repo.Path(ctx)
	if err != nil {
		return nil, err
	}

	altInfo, err := stats.AlternatesInfoForRepository(repoPath)
	if err != nil {
		return nil, fmt.Errorf("getting alternates info: %w", err)
	}

	if !altInfo.Exists || len(altInfo.ObjectDirectories) == 0 {
		return nil, ErrAlternateObjectDirNotExist
	}

	absolutePoolObjectDirPath := altInfo.AbsoluteObjectDirectories()[0]
	relativePoolObjectDirPath, err := filepath.Rel(storagePath, absolutePoolObjectDirPath)
	if err != nil {
		return nil, err
	}

	objectPoolProto := &gitalypb.ObjectPool{
		Repository: &gitalypb.Repository{
			StorageName:  repo.GetStorageName(),
			RelativePath: filepath.Dir(relativePoolObjectDirPath),
		},
	}

	if locator.ValidateRepository(ctx, objectPoolProto.Repository) != nil {
		return nil, ErrInvalidPoolRepository
	}

	return FromProto(ctx, logger, locator, gitCmdFactory, catfileCache, txManager, housekeepingManager, objectPoolProto)
}
