package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func (s *server) RemoveRepository(ctx context.Context, in *gitalypb.RemoveRepositoryRequest) (*gitalypb.RemoveRepositoryResponse, error) {
	repo := in.GetRepository()

	path, err := s.locator.GetPath(repo)
	if err != nil {
		return nil, helper.ErrInternal(err)
	}

	tempDir, err := s.locator.TempDir(repo.GetStorageName())
	if err != nil {
		return nil, helper.ErrInternalf("temporary directory: %w", err)
	}

	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return nil, helper.ErrInternal(err)
	}

	base := filepath.Base(path)
	destDir := filepath.Join(tempDir, base+"+removed")

	// Check whether the repository exists. If not, then there is nothing we can
	// remove. Historically, we didn't return an error in this case, which was just
	// plain bad RPC design: callers should be able to act on this, and if they don't
	// care they may still just return `NotFound` errors.
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, helper.ErrNotFoundf("repository does not exist")
		}

		return nil, helper.ErrInternalf("statting repository: %w", err)
	}

	locker, err := safe.NewLockingFileWriter(path)
	if err != nil {
		return nil, helper.ErrInternalf("creating locker: %w", err)
	}
	defer func() {
		if err := locker.Close(); err != nil {
			ctxlogrus.Extract(ctx).Error("closing repository locker: %w", err)
		}
	}()

	// Lock the repository such that it cannot be created or removed by any concurrent
	// RPC call.
	if err := locker.Lock(); err != nil {
		if errors.Is(err, safe.ErrFileAlreadyLocked) {
			return nil, helper.ErrFailedPreconditionf("repository is already locked")
		}
		return nil, helper.ErrInternalf("locking repository for removal: %w", err)
	}

	// Recheck whether the repository still exists after we have taken the lock. It
	// could be a concurrent RPC call removed the repository while we have not yet been
	// holding the lock.
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil, helper.ErrNotFoundf("repository was concurrently removed")
		}
		return nil, helper.ErrInternalf("re-statting repository: %w", err)
	}

	if err := s.voteOnAction(ctx, repo, voting.Prepared); err != nil {
		return nil, helper.ErrInternalf("vote on rename: %v", err)
	}

	// We move the repository into our temporary directory first before we start to
	// delete it. This is done such that we don't leave behind a partially-removed and
	// thus likely corrupt repository.
	if err := os.Rename(path, destDir); err != nil {
		return nil, helper.ErrInternalf("staging repository for removal: %w", err)
	}

	if err := os.RemoveAll(destDir); err != nil {
		return nil, helper.ErrInternalf("removing repository: %w", err)
	}

	if err := s.voteOnAction(ctx, repo, voting.Committed); err != nil {
		return nil, helper.ErrInternalf("vote on finalizing: %v", err)
	}

	return &gitalypb.RemoveRepositoryResponse{}, nil
}

func (s *server) voteOnAction(ctx context.Context, repo *gitalypb.Repository, phase voting.Phase) error {
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
		return s.txManager.Vote(ctx, tx, voting.VoteFromData([]byte(vote)), phase)
	})
}
