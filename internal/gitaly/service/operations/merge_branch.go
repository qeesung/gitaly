package operations

import (
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// UserMergeBranch is a two stage streaming RPC that will merge two commits together and
// create a merge commit
func (s *Server) UserMergeBranch(stream gitalypb.OperationService_UserMergeBranchServer) error {
	ctx := stream.Context()

	firstRequest, err := stream.Recv()
	if err != nil {
		return err
	}

	if err := validateMergeBranchRequest(s.locator, firstRequest); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	quarantineDir, quarantineRepo, cleanup, err := s.quarantinedRepo(ctx, firstRequest.GetRepository())
	if err != nil {
		return err
	}
	defer cleanup()

	objectHash, err := quarantineRepo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object format: %w", err)
	}

	referenceName := git.NewReferenceNameFromBranchName(string(firstRequest.Branch))

	var revision git.ObjectID
	if expectedOldOID := firstRequest.GetExpectedOldOid(); expectedOldOID != "" {
		revision, err = objectHash.FromHex(expectedOldOID)
		if err != nil {
			return structerr.NewInvalidArgument("invalid expected old object ID: %w", err).WithMetadata("old_object_id", expectedOldOID)
		}
		revision, err = quarantineRepo.ResolveRevision(
			ctx, git.Revision(fmt.Sprintf("%s^{object}", revision)),
		)
		if err != nil {
			return structerr.NewInvalidArgument("cannot resolve expected old object ID: %w", err).
				WithMetadata("old_object_id", expectedOldOID)
		}
	} else {
		revision, err = quarantineRepo.ResolveRevision(ctx, referenceName.Revision())
		if err != nil {
			if errors.Is(err, git.ErrReferenceNotFound) {
				return structerr.NewNotFound("%w", err)
			}
			return structerr.NewInternal("branch resolution: %w", err)
		}
	}

	authorSignature, err := git.SignatureFromRequest(firstRequest)
	if err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	mergeCommitID, err := s.merge(ctx, quarantineRepo,
		authorSignature,
		authorSignature,
		string(firstRequest.Message),
		revision.String(),
		firstRequest.CommitId,
		false,
	)
	if err != nil {
		var conflictErr *localrepo.MergeTreeConflictError
		if errors.As(err, &conflictErr) {
			conflictingFiles := make([][]byte, 0, len(conflictErr.ConflictingFileInfo))
			for _, conflictingFileInfo := range conflictErr.ConflictingFileInfo {
				conflictingFiles = append(conflictingFiles, []byte(conflictingFileInfo.FileName))
			}

			return structerr.NewFailedPrecondition("merging commits: %w", err).
				WithDetail(
					&gitalypb.UserMergeBranchError{
						Error: &gitalypb.UserMergeBranchError_MergeConflict{
							MergeConflict: &gitalypb.MergeConflictError{
								ConflictingFiles: conflictingFiles,
								ConflictingCommitIds: []string{
									revision.String(),
									firstRequest.CommitId,
								},
							},
						},
					},
				)
		}

		return structerr.NewInternal("merge: %w", err)
	}

	mergeOID, err := objectHash.FromHex(mergeCommitID)
	if err != nil {
		return structerr.NewInternal("could not parse merge ID: %w", err)
	}

	if err := stream.Send(&gitalypb.UserMergeBranchResponse{
		CommitId: mergeOID.String(),
	}); err != nil {
		return err
	}

	secondRequest, err := stream.Recv()
	if err != nil {
		return err
	}
	if !secondRequest.Apply {
		return structerr.NewFailedPrecondition("merge aborted by client")
	}

	if err := s.updateReferenceWithHooks(ctx, firstRequest.GetRepository(), firstRequest.User, quarantineDir, referenceName, mergeOID, revision); err != nil {
		var notAllowedError hook.NotAllowedError
		var customHookErr updateref.CustomHookError
		var updateRefError updateref.Error
		var errReferenceVerification storagemgr.ReferenceVerificationError

		if errors.As(err, &notAllowedError) {
			return structerr.NewPermissionDenied("%w", notAllowedError).WithDetail(
				&gitalypb.UserMergeBranchError{
					Error: &gitalypb.UserMergeBranchError_AccessCheck{
						AccessCheck: &gitalypb.AccessCheckError{
							ErrorMessage: notAllowedError.Message,
							UserId:       notAllowedError.UserID,
							Protocol:     notAllowedError.Protocol,
							Changes:      notAllowedError.Changes,
						},
					},
				},
			)
		} else if errors.As(err, &customHookErr) {
			return structerr.NewPermissionDenied("%w", customHookErr).WithDetail(
				&gitalypb.UserMergeBranchError{
					Error: &gitalypb.UserMergeBranchError_CustomHook{
						CustomHook: customHookErr.Proto(),
					},
				},
			)
		} else if errors.As(err, &updateRefError) {
			// When an error happens updating the reference, e.g. because of a
			// race with another update, then we should tell the user that a
			// precondition failed. A retry may fix this.
			return structerr.NewFailedPrecondition("%w", updateRefError).WithDetail(
				&gitalypb.UserMergeBranchError{
					Error: &gitalypb.UserMergeBranchError_ReferenceUpdate{
						ReferenceUpdate: &gitalypb.ReferenceUpdateError{
							ReferenceName: []byte(updateRefError.Reference.String()),
							OldOid:        updateRefError.OldOID.String(),
							NewOid:        updateRefError.NewOID.String(),
						},
					},
				},
			)
		} else if errors.As(err, &errReferenceVerification) {
			return structerr.NewFailedPrecondition("%w", &errReferenceVerification).WithDetail(
				&gitalypb.UserMergeBranchError{
					Error: &gitalypb.UserMergeBranchError_ReferenceUpdate{
						ReferenceUpdate: &gitalypb.ReferenceUpdateError{
							ReferenceName: []byte(errReferenceVerification.ReferenceName),
							OldOid:        errReferenceVerification.ExpectedOldOID.String(),
							NewOid:        errReferenceVerification.NewOID.String(),
						},
					},
				},
			)
		}

		return structerr.NewInternal("target update: %w", err)
	}

	if err := stream.Send(&gitalypb.UserMergeBranchResponse{
		BranchUpdate: &gitalypb.OperationBranchUpdate{
			CommitId:      mergeOID.String(),
			RepoCreated:   false,
			BranchCreated: false,
		},
	}); err != nil {
		return err
	}

	return nil
}

func validateMergeBranchRequest(locator storage.Locator, request *gitalypb.UserMergeBranchRequest) error {
	if err := locator.ValidateRepository(request.GetRepository()); err != nil {
		return err
	}

	if request.User == nil {
		return errors.New("empty user")
	}

	if len(request.User.Email) == 0 {
		return errors.New("empty user email")
	}

	if len(request.User.Name) == 0 {
		return errors.New("empty user name")
	}

	if len(request.Branch) == 0 {
		return errors.New("empty branch name")
	}

	if request.CommitId == "" {
		return errors.New("empty commit ID")
	}

	if len(request.Message) == 0 {
		return errors.New("empty message")
	}

	return nil
}
