package walk

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// FindRepositories finds all repositories in a storage and provides their information to the caller-provided action.
func FindRepositories(
	ctx context.Context,
	locator storage.Locator,
	storageName string,
	repoAction func(relPath string, gitDirInfo fs.FileInfo) error,
) error {
	storagePath, err := locator.GetStorageByName(ctx, storageName)
	if err != nil {
		return structerr.NewNotFound("looking up storage: %w", err)
	}

	return filepath.WalkDir(storagePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}

			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// keep walking
		}

		relPath, err := filepath.Rel(storagePath, path)
		if err != nil {
			return err
		}

		// Don't walk Gitaly's internal files.
		if relPath == config.GitalyDataPrefix {
			return fs.SkipDir
		}

		if locator.ValidateRepository(ctx, &gitalypb.Repository{
			StorageName:  storageName,
			RelativePath: relPath,
		}) == nil {
			gitDirInfo, err := os.Stat(path)
			if err != nil {
				return err
			}

			if err := repoAction(relPath, gitDirInfo); err != nil {
				return err
			}

			// once we know we are inside a git directory, we don't
			// want to continue walking inside since that is
			// resource intensive and unnecessary
			return filepath.SkipDir
		}

		return nil
	})
}
