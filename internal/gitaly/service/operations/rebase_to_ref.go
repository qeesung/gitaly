package operations

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// UserRebaseToRef overwrites the given TargetRef with the result of rebasing
// SourceSHA on top of FirstParentRef, and returns the SHA of the HEAD of
// TargetRef.
func (s *Server) UserRebaseToRef(ctx context.Context, request *gitalypb.UserRebaseToRefRequest) (*gitalypb.UserRebaseToRefResponse, error) {
	if err := validateUserRebaseToRefRequest(s.locator, request); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	quarantineDir, quarantineRepo, err := s.quarantinedRepo(ctx, request.Repository)
	if err != nil {
		return nil, structerr.NewInternal("creating repo quarantine: %w", err)
	}

	repoPath, err := quarantineRepo.Path()
	if err != nil {
		return nil, err
	}

	oid, err := quarantineRepo.ResolveRevision(ctx, git.Revision(request.FirstParentRef))
	if err != nil {
		return nil, structerr.NewInvalidArgument("invalid FirstParentRef")
	}

	sourceOID, err := quarantineRepo.ResolveRevision(ctx, git.Revision(request.SourceSha))
	if err != nil {
		return nil, structerr.NewInvalidArgument("invalid SourceSha")
	}

	// Resolve the current state of the target reference. We do not care whether it
	// exists or not, but what we do want to assert is that the target reference doesn't
	// change while we compute the merge commit as a small protection against races.
	objectHash, err := quarantineRepo.ObjectHash(ctx)
	if err != nil {
		return nil, structerr.NewInternal("detecting object hash: %w", err)
	}

	var oldTargetOID git.ObjectID
	if expectedOldOID := request.GetExpectedOldOid(); expectedOldOID != "" {
		oldTargetOID, err = objectHash.FromHex(expectedOldOID)
		if err != nil {
			return nil, structerr.NewInvalidArgument("invalid expected old object ID: %w", err).WithMetadata("old_object_id", expectedOldOID)
		}

		oldTargetOID, err = quarantineRepo.ResolveRevision(
			ctx, git.Revision(fmt.Sprintf("%s^{object}", oldTargetOID)),
		)
		if err != nil {
			return nil, structerr.NewInvalidArgument("cannot resolve expected old object ID: %w", err).
				WithMetadata("old_object_id", expectedOldOID)
		}
	} else if targetRef, err := quarantineRepo.GetReference(ctx, git.ReferenceName(request.TargetRef)); err == nil {
		if targetRef.IsSymbolic {
			return nil, structerr.NewFailedPrecondition("target reference is symbolic: %q", request.TargetRef)
		}

		oid, err := objectHash.FromHex(targetRef.Target)
		if err != nil {
			return nil, structerr.NewInternal("invalid target revision: %w", err)
		}

		oldTargetOID = oid
	} else if errors.Is(err, git.ErrReferenceNotFound) {
		oldTargetOID = objectHash.ZeroOID
	} else {
		return nil, structerr.NewInternal("could not read target reference: %w", err)
	}

	committer := git.NewSignature(string(request.User.Name), string(request.User.Email), time.Now())
	if request.Timestamp != nil {
		committer.When = request.Timestamp.AsTime()
	}

	var rebasedOID git.ObjectID
	if featureflag.UserRebaseToRefPureGit.IsEnabled(ctx) {
		rebasedOID, err = quarantineRepo.Rebase(
			ctx,
			oid.String(),
			sourceOID.String(),
			localrepo.RebaseWithCommitter(committer),
		)
		if err != nil {
			var conflictErr *localrepo.RebaseConflictError
			if errors.As(err, &conflictErr) {
				return nil, structerr.NewFailedPrecondition("failed to rebase %s on %s while preparing %s due to conflict",
					sourceOID, oid, string(request.TargetRef))
			}

			return nil, structerr.NewInternal("rebasing commits: %w", err)
		}
	} else {
		rebasedOID, err = s.git2goExecutor.Rebase(ctx, quarantineRepo, git2go.RebaseCommand{
			Repository:       repoPath,
			Committer:        committer,
			CommitID:         sourceOID,
			UpstreamCommitID: oid,
			SkipEmptyCommits: true,
		})
		if err != nil {
			var conflictErr git2go.ConflictingFilesError
			if errors.As(err, &conflictErr) {
				return nil, structerr.NewFailedPrecondition("failed to rebase %s on %s while preparing %s due to conflict",
					sourceOID, oid, string(request.TargetRef))
			}

			return nil, structerr.NewInternal("rebasing commits: %w", err)
		}
	}

	if err := quarantineDir.Migrate(); err != nil {
		return nil, structerr.NewInternal("migrating quarantined objects: %w", err)
	}

	repo := s.localrepo(request.GetRepository())
	if err := repo.UpdateRef(ctx, git.ReferenceName(request.TargetRef), rebasedOID, oldTargetOID); err != nil {
		return nil, structerr.NewFailedPrecondition("could not update %s. Please refresh and try again", string(request.TargetRef))
	}

	return &gitalypb.UserRebaseToRefResponse{
		CommitId: rebasedOID.String(),
	}, nil
}
