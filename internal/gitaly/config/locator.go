package config

import (
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

// GetRepoPath returns the full path of the repository referenced by an RPC Repository message.
// By default, it verifies that the path is an existing git directory. However, if invoked with
// the `GetRepoPathOption` produced by `WithRepositoryVerificationSkipped()`, this validation
// will be skipped. The errors returned are gRPC errors with relevant error codes and should be
// passed back to gRPC without further decoration.
func (l *configLocator) GetRepoPath(repo storage.Repository, opts ...storage.GetRepoPathOption) (string, error) {
	var cfg storage.GetRepoPathConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	repoPath, err := l.GetPath(repo)
	if err != nil {
		return "", err
	}

	if cfg.SkipRepositoryVerification || storage.IsGitDirectory(repoPath) {
		return repoPath, nil
	}

	return "", structerr.NewNotFound("GetRepoPath: not a git repository: %q", repoPath)
}

// GetPath returns the path of the repo passed as first argument. An error is
// returned when either the storage can't be found or the path includes
// constructs trying to perform directory traversal.
func (l *configLocator) GetPath(repo storage.Repository) (string, error) {
	storagePath, err := l.GetStorageByName(repo.GetStorageName())
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(storagePath); err != nil {
		if os.IsNotExist(err) {
			return "", structerr.NewNotFound("GetPath: does not exist: %w", err)
		}
		return "", structerr.NewInternal("GetPath: storage path: %w", err)
	}

	relativePath := repo.GetRelativePath()
	if len(relativePath) == 0 {
		err := structerr.NewInvalidArgument("GetPath: relative path missing")
		return "", err
	}

	if _, err := storage.ValidateRelativePath(storagePath, relativePath); err != nil {
		return "", structerr.NewInvalidArgument("GetRepoPath: %w", err)
	}

	return filepath.Join(storagePath, relativePath), nil
}

// GetStorageByName will return the path for the storage, which is fetched by
// its key. An error is return if it cannot be found.
func (l *configLocator) GetStorageByName(storageName string) (string, error) {
	storagePath, ok := l.conf.StoragePath(storageName)
	if !ok {
		return "", structerr.NewInvalidArgument("GetStorageByName: no such storage: %q", storageName)
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
