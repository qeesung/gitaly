package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

const (
	// tmpRootPrefix is the directory in which we store temporary
	// directories.
	tmpRootPrefix = GitalyDataPrefix + "/tmp"

	// cachePrefix is the directory where all cache data is stored on a
	// storage location.
	cachePrefix = GitalyDataPrefix + "/cache"

	// statePrefix is the directory where all state data is stored on a
	// storage location.
	statePrefix = GitalyDataPrefix + "/state"
)

// NewLocator returns locator based on the provided configuration struct.
// As it creates a shallow copy of the provided struct changes made into provided struct
// may affect result of methods implemented by it.
func NewLocator(conf Cfg) storage.Locator {
	return &configLocator{conf: conf}
}

type configLocator struct {
	conf Cfg
}

// ValidateRepository validates whether the given path points to a valid Git repository. This
// function returns:
//
// - ErrRepositoryNotFound when the path cannot be found.
// - ErrRepositoryNotValid when the repository is not a valid Git repository.
// - An unspecified error when stat(3P)ing files fails due to other reasons.
func (l *configLocator) ValidateRepository(ctx context.Context, repo storage.Repository, opts ...storage.ValidateRepositoryOption) error {
	var cfg storage.ValidateRepositoryConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	// Only checking for `nil` isn't sufficient as Protobuf messages may be non-nil, but still
	// either invalid or empty. Thus we also explicitly verify whether both the storage name and
	// the relative path are unset.
	if repo == nil || repo.GetStorageName() == "" && repo.GetRelativePath() == "" {
		return structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet)
	}

	relativePath := repo.GetRelativePath()
	if len(relativePath) == 0 {
		return structerr.NewInvalidArgument("%w", storage.ErrRepositoryPathNotSet)
	}

	if cfg.SkipStorageExistenceCheck {
		return nil
	}

	storagePath, err := l.GetStorageByName(ctx, repo.GetStorageName())
	if err != nil {
		return err
	}

	if _, err := os.Stat(storagePath); err != nil {
		if os.IsNotExist(err) {
			return structerr.NewNotFound("storage does not exist").WithMetadata("storage_path", storagePath)
		}
		return structerr.New("storage path: %w", err).WithMetadata("storage_path", storagePath)
	}

	if _, err := storage.ValidateRelativePath(storagePath, relativePath); err != nil {
		return structerr.NewInvalidArgument("%w", err).WithMetadata("relative_path", relativePath)
	}

	path := filepath.Join(storagePath, repo.GetRelativePath())
	if path == "" {
		return structerr.NewInvalidArgument("repository path is empty")
	}

	if !cfg.SkipRepositoryExistenceCheck {
		if err := storage.ValidateGitDirectory(path); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return storage.NewRepositoryNotFoundError(repo.GetStorageName(), repo.GetRelativePath())
			}

			var errInvalidGitDir storage.InvalidGitDirectoryError
			if errors.As(err, &errInvalidGitDir) {
				return structerr.NewFailedPrecondition(
					"%w: %q does not exist", storage.ErrRepositoryNotValid, errInvalidGitDir.MissingEntry,
				).WithMetadata("repository_path", path)
			}

			return structerr.New("validate git directory: %w", err).WithMetadata("repository_path", path)
		}

		// See: https://gitlab.com/gitlab-org/gitaly/issues/1339
		//
		// This is a workaround for Gitaly running on top of an NFS mount. There
		// is a Linux NFS v4.0 client bug where opening the packed-refs file can
		// either result in a stale file handle or stale data. This can happen if
		// git gc runs for a long time while keeping open the packed-refs file.
		// Running stat() on the file causes the kernel to revalidate the cached
		// directory entry. We don't actually care if this file exists.
		_, _ = os.Stat(filepath.Join(path, "packed-refs"))
	}

	return nil
}

// GetRepoPath returns the full path of the repository referenced by an RPC Repository message.
// By default, it verifies that the path is an existing git directory. However, if invoked with
// the `GetRepoPathOption` produced by `WithRepositoryVerificationSkipped()`, this validation
// will be skipped. The errors returned are gRPC errors with relevant error codes and should be
// passed back to gRPC without further decoration.
func (l *configLocator) GetRepoPath(ctx context.Context, repo storage.Repository, opts ...storage.GetRepoPathOption) (string, error) {
	var cfg storage.GetRepoPathConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	var validationOptions []storage.ValidateRepositoryOption
	if cfg.SkipRepositoryVerification {
		validationOptions = []storage.ValidateRepositoryOption{
			storage.WithSkipRepositoryExistenceCheck(),
		}
	}

	if err := l.ValidateRepository(ctx, repo, validationOptions...); err != nil {
		return "", err
	}

	storagePath, err := l.GetStorageByName(ctx, repo.GetStorageName())
	if err != nil {
		return "", err
	}
	relativePath := repo.GetRelativePath()

	return filepath.Join(storagePath, relativePath), nil
}

// GetStorageByName will return the path for the storage, which is fetched by
// its key. An error is return if it cannot be found.
func (l *configLocator) GetStorageByName(ctx context.Context, storageName string) (string, error) {
	if storageName == "" {
		return "", structerr.NewInvalidArgument("%w", storage.ErrStorageNotSet)
	}

	storagePath, ok := l.conf.StoragePath(storageName)
	if !ok {
		return "", storage.NewStorageNotFoundError(storageName)
	}

	return storagePath, nil
}

// CacheDir returns the path to the cache dir for a storage.
func (l *configLocator) CacheDir(storageName string) (string, error) {
	return l.getPath(storageName, cachePrefix)
}

// StateDir returns the path to the state dir for a storage.
func (l *configLocator) StateDir(storageName string) (string, error) {
	return l.getPath(storageName, statePrefix)
}

// TempDir returns the path to the temp dir for a storage.
func (l *configLocator) TempDir(storageName string) (string, error) {
	return l.getPath(storageName, tmpRootPrefix)
}

func (l *configLocator) getPath(storageName, prefix string) (string, error) {
	storagePath, ok := l.conf.StoragePath(storageName)
	if !ok {
		return "", structerr.NewInvalidArgument("%s dir: no such storage: %q",
			filepath.Base(prefix), storageName)
	}

	return filepath.Join(storagePath, prefix), nil
}
