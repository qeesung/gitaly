package ref

import (
	"errors"
	"fmt"
	"io"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// UpdateReferences updates a set of references atomically. It allows for reference creations, deletions and updates in
// a raceless way.
func (s *server) UpdateReferences(server gitalypb.RefService_UpdateReferencesServer) error {
	ctx := server.Context()

	request, err := server.Recv()
	if err != nil {
		return fmt.Errorf("receiving initial request: %w", err)
	}

	if err := s.locator.ValidateRepository(ctx, request.GetRepository()); err != nil {
		return err
	}
	repo := s.localrepo(request.GetRepository())

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object hash: %w", err)
	}

	// Unset the repository so that we can more easily verify inside of the loop that all incoming requests
	// ain't got a repository set anymore.
	request.Repository = nil

	updater, err := updateref.New(ctx, repo)
	if err != nil {
		return fmt.Errorf("creating updater: %w", err)
	}

	if err := updater.Start(); err != nil {
		return fmt.Errorf("starting updater: %w", err)
	}

	for {
		// Only the first request may have its repository set.
		if request.GetRepository() != nil {
			return structerr.NewInvalidArgument("repository set in subsequent request")
		}

		if len(request.GetUpdates()) == 0 {
			return structerr.NewInvalidArgument("no updates specified")
		}

		for _, update := range request.GetUpdates() {
			reference := string(update.GetReference())
			if err := git.ValidateReference(reference); err != nil {
				return structerr.NewInvalidArgument("validating reference: %w", err).
					WithMetadata("reference", reference).
					WithDetail(&gitalypb.UpdateReferencesError{
						Error: &gitalypb.UpdateReferencesError_InvalidFormat{
							InvalidFormat: &gitalypb.InvalidRefFormatError{
								Refs: [][]byte{[]byte(reference)},
							},
						},
					})
			}

			// The old object ID may be empty, in which case we don't care about the current value of the
			// reference but instead do a force update of it.
			oldObjectID := string(update.GetOldObjectId())
			if len(oldObjectID) > 0 {
				if err := objectHash.ValidateHex(oldObjectID); err != nil {
					return structerr.NewInvalidArgument("validating old object ID: %w", err).WithMetadata("old_object_id", oldObjectID)
				}
			}

			newObjectID := string(update.GetNewObjectId())
			if err := objectHash.ValidateHex(newObjectID); err != nil {
				return structerr.NewInvalidArgument("validating new object ID: %w", err).WithMetadata("new_object_id", newObjectID)
			}

			if err := updater.Update(git.ReferenceName(reference), git.ObjectID(newObjectID), git.ObjectID(oldObjectID)); err != nil {
				return structerr.NewInvalidArgument("queueing update: %w", err)
			}
		}

		if request, err = server.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return fmt.Errorf("receiving subsequent request: %w", err)
		}
	}

	if err := updater.Commit(); err != nil {
		var alreadyLockedErr updateref.AlreadyLockedError
		if errors.As(err, &alreadyLockedErr) {
			return structerr.NewAborted("%w", err).
				WithDetail(&gitalypb.UpdateReferencesError{
					Error: &gitalypb.UpdateReferencesError_ReferencesLocked{
						ReferencesLocked: &gitalypb.ReferencesLockedError{
							Refs: [][]byte{[]byte(alreadyLockedErr.ReferenceName)},
						},
					},
				})
		}

		var mismatchingStateErr updateref.MismatchingStateError
		if errors.As(err, &mismatchingStateErr) {
			return structerr.NewAborted("%w", err).
				WithDetail(&gitalypb.UpdateReferencesError{
					Error: &gitalypb.UpdateReferencesError_ReferenceStateMismatch{
						ReferenceStateMismatch: &gitalypb.ReferenceStateMismatchError{
							ReferenceName:    []byte(mismatchingStateErr.ReferenceName),
							ExpectedObjectId: []byte(mismatchingStateErr.ExpectedObjectID),
							ActualObjectId:   []byte(mismatchingStateErr.ActualObjectID),
						},
					},
				})
		}

		var nonExistentObjectErr updateref.NonExistentObjectError
		if errors.As(err, &nonExistentObjectErr) {
			return structerr.NewNotFound("%w", err)
		}

		return fmt.Errorf("committing update: %w", err)
	}

	if err := server.SendAndClose(&gitalypb.UpdateReferencesResponse{}); err != nil {
		return fmt.Errorf("sending response: %w", err)
	}

	return nil
}
