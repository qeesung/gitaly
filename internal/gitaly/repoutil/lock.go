package repoutil

import (
	"context"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

// Lock attempts to lock the entire repository directory such that only one
// process can obtain the lock at a time.
//
// The repositories parent directory will be created if it does not exist.
//
// Returns the error safe.ErrFileAlreadyLocked if the repository is already
// locked.
func Lock(ctx context.Context, logger log.Logger, locator storage.Locator, repository storage.Repository) (func(), error) {
	path, err := locator.GetRepoPath(ctx, repository, storage.WithRepositoryVerificationSkipped())
	if err != nil {
		return nil, err
	}

	// Create the parent directory in case it doesn't exist yet.
	if err := os.MkdirAll(filepath.Dir(path), perm.PrivateDir); err != nil {
		return nil, structerr.NewInternal("create directories: %w", err)
	}

	// We're somewhat abusing this file writer given that we simply want to assert that
	// the target directory doesn't exist and isn't created while we want to move the
	// new repository into place. We thus only use the locking semantics of the writer,
	// but will never commit it.
	locker, err := safe.NewLockingFileWriter(path)
	if err != nil {
		return nil, err
	}

	unlock := func() {
		if err := locker.Close(); err != nil {
			logger.WithError(err).ErrorContext(ctx, "closing repository locker failed")
		}
	}

	if err := locker.Lock(); err != nil {
		unlock()

		return nil, err
	}

	return unlock, nil
}
