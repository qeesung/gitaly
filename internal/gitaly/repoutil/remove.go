package repoutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
)

// Remove will remove a repository in a race-free way with proper transactional semantics.
func Remove(
	ctx context.Context,
	logger log.Logger,
	locator storage.Locator,
	txManager transaction.Manager,
	repoCounter *counter.RepositoryCounter,
	repository storage.Repository,
) error {
	if err := remove(ctx, logger, locator, txManager, repository, os.RemoveAll); err != nil {
		return err
	}

	repoCounter.Decrement(repository)

	return nil
}

func remove(
	ctx context.Context,
	logger log.Logger,
	locator storage.Locator,
	txManager transaction.Manager,
	repository storage.Repository,
	removeAll func(string) error,
) error {
	path, err := locator.GetRepoPath(ctx, repository, storage.WithRepositoryVerificationSkipped())
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	tempDir, err := locator.TempDir(repository.GetStorageName())
	if err != nil {
		return structerr.NewInternal("temporary directory: %w", err)
	}

	if err := os.MkdirAll(tempDir, perm.PrivateDir); err != nil {
		return structerr.NewInternal("%w", err)
	}

	// Check whether the repository exists. If not, then there is nothing we can
	// remove. Historically, we didn't return an error in this case, which was just
	// plain bad RPC design: callers should be able to act on this, and if they don't
	// care they may still just return `NotFound` errors.
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return structerr.NewNotFound("repository does not exist")
		}

		return structerr.NewInternal("statting repository: %w", err)
	}

	// Lock the repository such that it cannot be created or removed by any concurrent
	// RPC call.
	unlock, err := Lock(ctx, logger, locator, repository)
	if err != nil {
		if errors.Is(err, safe.ErrFileAlreadyLocked) {
			return structerr.NewFailedPrecondition("repository is already locked")
		}
		return structerr.NewInternal("locking repository for removal: %w", err)
	}
	defer unlock()

	// Recheck whether the repository still exists after we have taken the lock. It
	// could be a concurrent RPC call removed the repository while we have not yet been
	// holding the lock.
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return structerr.NewNotFound("repository was concurrently removed")
		}
		return structerr.NewInternal("re-statting repository: %w", err)
	}

	if err := voteOnAction(ctx, txManager, repository, voting.Prepared); err != nil {
		return structerr.NewInternal("vote on rename: %w", err)
	}

	destDir, err := os.MkdirTemp(tempDir, filepath.Base(path)+"+removed-*")
	if err != nil {
		return fmt.Errorf("mkdir temp: %w", err)
	}

	defer func() {
		if err := removeAll(destDir); err != nil {
			logger.WithError(err).ErrorContext(ctx, "failed removing repository from temporary directory")
		}
	}()

	// We move the repository into our temporary directory first before we start to
	// delete it. This is done such that we don't leave behind a partially-removed and
	// thus likely corrupt repository.
	if err := os.Rename(path, filepath.Join(destDir, "repo")); err != nil {
		return structerr.NewInternal("staging repository for removal: %w", err)
	}

	if err := safe.NewSyncer().SyncParent(path); err != nil {
		return fmt.Errorf("sync removal: %w", err)
	}

	if err := voteOnAction(ctx, txManager, repository, voting.Committed); err != nil {
		return structerr.NewInternal("vote on finalizing: %w", err)
	}

	return nil
}

func voteOnAction(
	ctx context.Context,
	txManager transaction.Manager,
	repo storage.Repository,
	phase voting.Phase,
) error {
	return transaction.RunOnContext(ctx, func(tx txinfo.Transaction) error {
		var voteStep string
		switch phase {
		case voting.Prepared:
			voteStep = "pre-remove"
		case voting.Committed:
			voteStep = "post-remove"
		default:
			return fmt.Errorf("invalid removal step: %d", phase)
		}

		vote := fmt.Sprintf("%s %s", voteStep, repo.GetRelativePath())
		return txManager.Vote(ctx, tx, voting.VoteFromData([]byte(vote)), phase)
	})
}
