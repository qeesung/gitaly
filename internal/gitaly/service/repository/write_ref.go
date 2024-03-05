package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) WriteRef(ctx context.Context, req *gitalypb.WriteRefRequest) (*gitalypb.WriteRefResponse, error) {
	if err := validateWriteRefRequest(s.locator, req); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}
	if err := s.writeRef(ctx, req); err != nil {
		return nil, structerr.NewInternal("%w", err)
	}

	return &gitalypb.WriteRefResponse{}, nil
}

func (s *server) writeRef(ctx context.Context, req *gitalypb.WriteRefRequest) error {
	repo := s.localrepo(req.GetRepository())

	if string(req.Ref) == "HEAD" {
		if err := repo.SetDefaultBranch(ctx, s.txManager, git.ReferenceName(req.GetRevision())); err != nil {
			return fmt.Errorf("setting default branch: %w", err)
		}

		return nil
	}

	return updateRef(ctx, repo, req)
}

func updateRef(ctx context.Context, repo *localrepo.Repo, req *gitalypb.WriteRefRequest) (returnedErr error) {
	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object format: %w", err)
	}

	var newObjectID git.ObjectID
	if objectHash.IsZeroOID(git.ObjectID(req.GetRevision())) {
		// Passing the all-zeroes object ID as new value means that we should delete the
		// reference.
		newObjectID = objectHash.ZeroOID
	} else {
		// We need to resolve the new revision in order to make sure that we're actually
		// passing an object ID to git-update-ref(1), but more importantly this will also
		// ensure that the object ID we're updating to actually exists. Note that we also
		// verify that the object actually exists in the repository by adding "^{object}".
		var err error
		newObjectID, err = repo.ResolveRevision(ctx, git.Revision(req.GetRevision())+"^{object}")
		switch {
		case errors.Is(err, git.ErrReferenceNotFound):
			return structerr.NewNotFound("resolving new revision: %w", err).WithDetail(
				&gitalypb.ReferenceNotFoundError{
					ReferenceName: req.GetRevision(),
				},
			)
		case err != nil:
			return fmt.Errorf("resolving new revision: %w", err)
		}
	}

	var oldObjectID git.ObjectID
	if len(req.GetOldRevision()) > 0 {
		if objectHash.IsZeroOID(git.ObjectID(req.GetOldRevision())) {
			// Passing an all-zeroes object ID indicates that we should only update the
			// reference if it didn't previously exist.
			oldObjectID = objectHash.ZeroOID
		} else {
			var err error
			oldObjectID, err = repo.ResolveRevision(ctx, git.Revision(req.GetOldRevision())+"^{object}")
			switch {
			case errors.Is(err, git.ErrReferenceNotFound):
				return structerr.NewNotFound("resolving old revision: %w", err).WithDetail(
					&gitalypb.ReferenceNotFoundError{
						ReferenceName: req.GetOldRevision(),
					},
				)
			case err != nil:
				return fmt.Errorf("resolving old revision: %w", err)
			}
		}
	}

	u, err := updateref.New(ctx, repo)
	if err != nil {
		return fmt.Errorf("error when running creating new updater: %w", err)
	}
	defer func() {
		if err := u.Close(); err != nil && returnedErr == nil {
			returnedErr = fmt.Errorf("close updater: %w", err)
		}
	}()

	if err := u.Start(); err != nil {
		return fmt.Errorf("start reference transaction: %w", err)
	}

	if err := u.Update(git.ReferenceName(req.GetRef()), newObjectID, oldObjectID); err != nil {
		return fmt.Errorf("error when creating update-ref command: %w", err)
	}

	if err := u.Commit(); err != nil {
		var alreadyLockedErr updateref.AlreadyLockedError
		if errors.As(err, &alreadyLockedErr) {
			return structerr.NewAborted("reference is locked already").WithMetadata("reference", alreadyLockedErr.ReferenceName)
		}

		return fmt.Errorf("error when running update-ref command: %w", err)
	}

	return nil
}

func validateWriteRefRequest(locator storage.Locator, req *gitalypb.WriteRefRequest) error {
	if err := locator.ValidateRepository(req.GetRepository()); err != nil {
		return err
	}
	if err := git.ValidateRevision(req.Ref); err != nil {
		return fmt.Errorf("invalid ref: %w", err)
	}
	if err := git.ValidateRevision(req.Revision); err != nil {
		return fmt.Errorf("invalid revision: %w", err)
	}
	if len(req.OldRevision) > 0 {
		if err := git.ValidateRevision(req.OldRevision); err != nil {
			return fmt.Errorf("invalid OldRevision: %w", err)
		}
	}

	if !bytes.Equal(req.Ref, []byte("HEAD")) && !bytes.HasPrefix(req.Ref, []byte("refs/")) {
		return fmt.Errorf("ref has to be a full reference")
	}
	return nil
}
