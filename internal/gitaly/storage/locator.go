package storage

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

// NewStorageNotFoundError returns a new error wrapping ErrStorageNotFound with an InvalidArgument
// error code and metadata.
func NewStorageNotFoundError(storageName string) error {
	return structerr.NewInvalidArgument("%w", ErrStorageNotFound).WithMetadata(
		"storage_name", storageName,
	)
}

// NewRepositoryNotFoundError returns a new error that wraps ErrRepositoryNotFound. The virtual
// storage and relative path are appended as error metadata.
func NewRepositoryNotFoundError(storageName string, relativePath string) error {
	return structerr.NewNotFound("%w", ErrRepositoryNotFound).WithMetadataItems(
		structerr.MetadataItem{Key: "storage_name", Value: storageName},
		structerr.MetadataItem{Key: "relative_path", Value: relativePath},
	)
}

var (
	// ErrStorageNotSet is returned when the storage name has not been set.
	ErrStorageNotSet = errors.New("storage name not set")
	// ErrStorageNotFound is returned when operating on a storage that doesn't exist.
	ErrStorageNotFound = errors.New("storage name not found")

	// ErrRepositoryNotSet is returned when the repository has not been set.
	ErrRepositoryNotSet = errors.New("repository not set")
	// ErrRepositoryPathNotSet is returned when the repository path has not been set.
	ErrRepositoryPathNotSet = errors.New("repository path not set")
	// ErrRepositoryNotFound is returned when operating on a repository that doesn't exist.
	//
	// This somewhat duplicates the above RepositoryNotFoundError but doesn't specify which
	// repository was not found. With repository IDs in use, the virtual storage and relative
	// path won't be available everywhere anymore.
	ErrRepositoryNotFound = errors.New("repository not found")
	// ErrRepositoryAlreadyExists is returned when attempting to create a repository that
	// already exists.
	ErrRepositoryAlreadyExists = errors.New("repository already exists")
	// ErrRepositoryNotValid is returned when operating on a path that is not a valid Git
	// repository.
	ErrRepositoryNotValid = errors.New("repository not valid")

	// ErrRelativePathEscapesRoot is returned when a relative path is passed that escapes the
	// storage's root directory.
	ErrRelativePathEscapesRoot = errors.New("relative path escapes root directory")
)

// Repository represents a storage-scoped repository.
type Repository interface {
	GetStorageName() string
	GetRelativePath() string
	GetGitObjectDirectory() string
	GetGitAlternateObjectDirectories() []string
}

// RepoPathEqual compares if two repositories are in the same location
func RepoPathEqual(a, b Repository) bool {
	return a.GetStorageName() == b.GetStorageName() &&
		a.GetRelativePath() == b.GetRelativePath()
}

// Locator allows to get info about location of the repository or storage at the local file system.
type Locator interface {
	// ValidateRepository validates whether the given repository is a valid Git repository. This
	// function can be configured by passing ValidateRepositoryOptions.
	ValidateRepository(context.Context, Repository, ...ValidateRepositoryOption) error
	// GetRepoPath returns the full path of the repository referenced by an RPC Repository message.
	// By default, it verifies that the path is an existing git directory. However, if invoked with
	// the `GetRepoPathOption` produced by `WithRepositoryVerificationSkipped()`, this validation
	// will be skipped. The errors returned are gRPC errors with relevant error codes and should be
	// passed back to gRPC without further decoration.
	GetRepoPath(ctx context.Context, repo Repository, opts ...GetRepoPathOption) (string, error)
	// GetStorageByName will return the path for the storage, which is fetched by
	// its key. An error is return if it cannot be found.
	GetStorageByName(ctx context.Context, storageName string) (string, error)

	// CacheDir returns the path to the cache dir for a storage.
	CacheDir(storageName string) (string, error)
	// TempDir returns the path to the temp dir for a storage.
	TempDir(storageName string) (string, error)
	// StateDir returns the path to the state dir for a storage.
	StateDir(storageName string) (string, error)
}

// ValidateRepositoryConfig is used to configure ValidateRepository.
type ValidateRepositoryConfig struct {
	// SkipStorageExistenceCheck determines whether the storage shall be checked for
	// existence or not. If set, the locator shall still perform parameter verification and
	// verify that whether the storage _would_ be valid if it existed, but not verify actual
	// existence. This will also skip the repository existence check. Required when
	// Praefect has not rewritten the repository storage name.
	SkipStorageExistenceCheck bool
	// SkipRepositoryExistenceCheck determines whether the repository shall be checked for
	// existence or not. If set, the locator shall still perform parameter verification and
	// verify that whether the repository _would_ be valid if it existed, but not verify actual
	// existence.
	SkipRepositoryExistenceCheck bool
}

// ValidateRepositoryOption is an option that can be passed to ValidateRepository.
type ValidateRepositoryOption func(*ValidateRepositoryConfig)

// WithSkipRepositoryExistenceCheck causes ValidateRepository to not check for repository
// existence. All other tests will still be performed.
func WithSkipRepositoryExistenceCheck() ValidateRepositoryOption {
	return func(cfg *ValidateRepositoryConfig) {
		cfg.SkipRepositoryExistenceCheck = true
	}
}

// WithSkipStorageExistenceCheck causes ValidateRepository to not check for storage
// existence. Repository existence check will also be skipped.
func WithSkipStorageExistenceCheck() ValidateRepositoryOption {
	return func(cfg *ValidateRepositoryConfig) {
		cfg.SkipStorageExistenceCheck = true
	}
}

// GetRepoPathConfig is used to configure GetRepoPath.
type GetRepoPathConfig struct {
	// SkipRepositoryVerification will cause GetRepoPath to skip verification the verification whether the
	// computed path is an actual Git repository or not.
	SkipRepositoryVerification bool
}

// GetRepoPathOption can be passed to GetRepoPath to change its default behavior.
type GetRepoPathOption func(*GetRepoPathConfig)

// WithRepositoryVerificationSkipped skips the repository path validation that ensures the Git
// directory is valid.
func WithRepositoryVerificationSkipped() GetRepoPathOption {
	return func(cfg *GetRepoPathConfig) {
		cfg.SkipRepositoryVerification = true
	}
}

// ValidateRelativePath validates a relative path by joining it with rootDir and verifying the result
// is either rootDir or a path within rootDir. Returns clean relative path from rootDir to relativePath
// or an ErrRelativePathEscapesRoot if the resulting path is not contained within rootDir.
func ValidateRelativePath(rootDir, relativePath string) (string, error) {
	absPath := filepath.Join(rootDir, relativePath)
	if rootDir != absPath && !strings.HasPrefix(absPath, rootDir+string(os.PathSeparator)) {
		return "", ErrRelativePathEscapesRoot
	}

	return filepath.Rel(rootDir, absPath)
}

// InvalidGitDirectoryError is returned when a Git directory being validated is invalid.
type InvalidGitDirectoryError struct {
	// MissingEntry is the expected directory entry that was missing.
	MissingEntry string
}

// Error returns the error message.
func (err InvalidGitDirectoryError) Error() string {
	return "invalid git directory"
}

// ValidateGitDirectory validates the directory at the given path looks like a valid
// Git repository. If the path points to a valid directory which doesn't contain the
// expected entries InvalidGitDirectoryError is returned. A directory is considered to
// be valid Git directory if it contains 'objects', 'refs' and 'HEAD'.
func ValidateGitDirectory(path string) error {
	if info, err := os.Stat(path); err != nil {
		return fmt.Errorf("stat: %w", err)
	} else if !info.IsDir() {
		return errors.New("not a directory")
	}

	for _, file := range []string{"objects", "refs", "HEAD"} {
		if _, err := os.Stat(filepath.Join(path, file)); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("stat %q: %w", file, err)
			}

			return InvalidGitDirectoryError{MissingEntry: file}
		}
	}

	return nil
}

// QuarantineDirectoryPrefix returns a prefix for use in the temporary directory. The prefix is
// based on the relative repository path and will stay stable for any given repository. This allows
// us to verify that a given quarantine object directory indeed belongs to the repository at hand.
// Ideally, this function would directly be located in the quarantine module, but this is not
// possible due to cyclic dependencies.
func QuarantineDirectoryPrefix(repo Repository) string {
	hash := [20]byte{}
	if repo != nil {
		hash = sha1.Sum([]byte(repo.GetRelativePath()))
	}
	return fmt.Sprintf("quarantine-%x-", hash[:8])
}
