package snapshot

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/gitstorage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
)

// snapshot is a snapshot of a file system's state.
type snapshot struct {
	// root is the absolute path of the snapshot.
	root string
	// prefix is the snapshot root relative to the storage root.
	prefix string
}

// Root returns the root of the snapshot's file system.
func (s *snapshot) Root() string {
	return s.root
}

// Prefix returns the prefix of the snapshot within the original root file system.
func (s *snapshot) Prefix() string {
	return s.prefix
}

// RelativePath returns the given relative path rewritten to point to the relative
// path in the snapshot.
func (s *snapshot) RelativePath(relativePath string) string {
	return filepath.Join(s.prefix, relativePath)
}

// NewSnapshot creates a new file system snapshot of the given root directory. The snapshot is created by copying
// the directory hierarchy and hard linking the files in place. The copied directory hierarchy is placed
// at destinationPath. Only files within Git directories are included in the snapshot. The provided relative
// paths are used to select the Git repositories that are included.
//
// destinationPath must be a subdirectory within roothPath. The prefix of the snapshot within the root file system
// can be retrieved by calling Prefix.
func NewSnapshot(ctx context.Context, rootPath, destinationPath string, relativePaths []string) (FileSystem, error) {
	snapshotPrefix, err := filepath.Rel(rootPath, destinationPath)
	if err != nil {
		return nil, fmt.Errorf("rel snapshot prefix: %w", err)
	}

	if err := createRepositorySnapshots(ctx, rootPath, snapshotPrefix, relativePaths); err != nil {
		return nil, fmt.Errorf("create repository snapshots: %w", err)
	}

	return &snapshot{root: destinationPath, prefix: snapshotPrefix}, nil
}

// createRepositorySnapshots creates a snapshot of the partition containing all repositories at the given relative paths
// and their alternates.
func createRepositorySnapshots(ctx context.Context, storagePath, snapshotPrefix string, relativePaths []string) error {
	// Create the root directory always to as the storage would also exist always.
	if err := os.Mkdir(filepath.Join(storagePath, snapshotPrefix), perm.PrivateDir); err != nil {
		return fmt.Errorf("mkdir snapshot root: %w", err)
	}

	snapshottedRepositories := make(map[string]struct{}, len(relativePaths))
	for _, relativePath := range relativePaths {
		if _, ok := snapshottedRepositories[relativePath]; ok {
			continue
		}

		sourcePath := filepath.Join(storagePath, relativePath)
		if err := storage.ValidateGitDirectory(sourcePath); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				// It's okay if the repository does not exist. We'll create a snapshot without the directory,
				// and the RPC handlers can handle the situation as best fit.
				continue
			}

			// The transaction logic doesn't require the snapshotted repository to be valid. We want to ensure
			// we only snapshot a 'leaf'/project directories in the storage. Otherwise relative paths like
			// `@hashed/xx` could attempt to snapshot an entire subtree. As Gitaly doesn't control the directory
			// hierarchy yet, we achieve this protection by only snapshotting valid Git directories.
			return fmt.Errorf("validate git directory: %w", err)
		}

		targetPath := filepath.Join(storagePath, snapshotPrefix, relativePath)
		if err := createRepositorySnapshot(ctx, sourcePath, targetPath); err != nil {
			return fmt.Errorf("create snapshot: %w", err)
		}

		snapshottedRepositories[relativePath] = struct{}{}

		// Read the repository's 'objects/info/alternates' file to figure out whether it is connected
		// to an alternate. If so, we need to include the alternate repository in the snapshot along
		// with the repository itself to ensure the objects from the alternate are also available.
		if alternate, err := gitstorage.ReadAlternatesFile(targetPath); err != nil && !errors.Is(err, gitstorage.ErrNoAlternate) {
			return fmt.Errorf("get alternate path: %w", err)
		} else if alternate != "" {
			// The repository had an alternate. The path is a relative from the repository's 'objects' directory
			// to the alternate's 'objects' directory. Build the relative path of the alternate repository.
			alternateRelativePath := filepath.Dir(filepath.Join(relativePath, "objects", alternate))
			if _, ok := snapshottedRepositories[alternateRelativePath]; ok {
				continue
			}

			// Include the alternate repository in the snapshot as well.
			if err := createRepositorySnapshot(ctx,
				filepath.Join(storagePath, alternateRelativePath),
				filepath.Join(storagePath, snapshotPrefix, alternateRelativePath),
			); err != nil {
				return fmt.Errorf("create alternate snapshot: %w", err)
			}

			snapshottedRepositories[alternateRelativePath] = struct{}{}
		}
	}

	return nil
}

// createRepositorySnapshot snapshots a repository's current state at snapshotPath. This is done by
// recreating the repository's directory structure and hard linking the repository's files in their
// correct locations there. This effectively does a copy-free clone of the repository. Since the files
// are shared between the snapshot and the repository, they must not be modified. Git doesn't modify
// existing files but writes new ones so this property is upheld.
func createRepositorySnapshot(ctx context.Context, repositoryPath, snapshotPath string) error {
	// This creates the parent directory hierarchy regardless of whether the repository exists or not. It also
	// doesn't consider the permissions in the storage. While not 100% correct, we have no logic that cares about
	// the storage hierarchy above repositories.
	//
	// The repository's directory itself is not yet created as whether it should be created depends on whether the
	// repository exists or not.
	if err := os.MkdirAll(filepath.Dir(snapshotPath), perm.PrivateDir); err != nil {
		return fmt.Errorf("create parent directory hierarchy: %w", err)
	}

	if err := createDirectorySnapshot(ctx, repositoryPath, snapshotPath, map[string]struct{}{
		// Don't include worktrees in the snapshot. All of the worktrees in the repository should be leftover
		// state from before transaction management was introduced as the transactions would create their
		// worktrees in the snapshot.
		housekeeping.WorktreesPrefix:      {},
		housekeeping.GitlabWorktreePrefix: {},
	}); err != nil {
		return fmt.Errorf("create directory snapshot: %w", err)
	}

	return nil
}

// createDirectorySnapshot recursively recreates the directory structure from originalDirectory into
// snapshotDirectory and hard links files into the same locations in snapshotDirectory.
//
// skipRelativePaths can be provided to skip certain entries from being included in the snapshot.
func createDirectorySnapshot(ctx context.Context, originalDirectory, snapshotDirectory string, skipRelativePaths map[string]struct{}) error {
	if err := filepath.Walk(originalDirectory, func(oldPath string, info fs.FileInfo, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && oldPath == originalDirectory {
				// The directory being snapshotted does not exist. This is fine as the transaction
				// may be about to create it.
				return nil
			}

			return err
		}

		relativePath, err := filepath.Rel(originalDirectory, oldPath)
		if err != nil {
			return fmt.Errorf("rel: %w", err)
		}

		if _, ok := skipRelativePaths[relativePath]; ok {
			return fs.SkipDir
		}

		newPath := filepath.Join(snapshotDirectory, relativePath)
		if info.IsDir() {
			if err := os.Mkdir(newPath, info.Mode().Perm()); err != nil {
				return fmt.Errorf("create dir: %w", err)
			}
		} else if info.Mode().IsRegular() {
			if err := os.Link(oldPath, newPath); err != nil {
				return fmt.Errorf("link file: %w", err)
			}
		} else {
			return fmt.Errorf("unsupported file mode: %q", info.Mode())
		}

		return nil
	}); err != nil {
		return fmt.Errorf("walk: %w", err)
	}

	return nil
}
