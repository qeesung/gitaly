package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// UserMergeToRef overwrites the given TargetRef to point to either Branch or
// FirstParentRef. Afterwards, it performs a merge of SourceSHA with either
// Branch or FirstParentRef and updates TargetRef to the merge commit.
func (s *Server) UserMergeToRef(ctx context.Context, request *gitalypb.UserMergeToRefRequest) (*gitalypb.UserMergeToRefResponse, error) {
	if err := validateUserMergeToRefRequest(s.locator, request); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(request.GetRepository())

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting object hash: %w", err)
	}

	revision := git.Revision(request.Branch)
	if request.FirstParentRef != nil {
		revision = git.Revision(request.FirstParentRef)
	}

	oid, err := repo.ResolveRevision(ctx, revision)
	if err != nil {
		return nil, structerr.NewInvalidArgument("Invalid merge source")
	}

	sourceOID, err := repo.ResolveRevision(ctx, git.Revision(request.SourceSha))
	if err != nil {
		return nil, structerr.NewInvalidArgument("Invalid merge source")
	}

	authorDate, err := dateFromProto(request)
	if err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	// Initialize oldTargetOID from expected_old_oid when provided, otherwise
	// resolve it from target_ref. This will be used as an optimistic lock when
	// we finally update target_ref, to ensure it hasn't changed in the
	// meantime.
	var oldTargetOID git.ObjectID
	if expectedOldOID := request.GetExpectedOldOid(); expectedOldOID != "" {
		objectHash, err := repo.ObjectHash(ctx)
		if err != nil {
			return nil, structerr.NewInternal("detecting object hash: %w", err)
		}

		oldTargetOID, err = objectHash.FromHex(expectedOldOID)
		if err != nil {
			return nil, structerr.NewInvalidArgument("invalid expected old object ID: %w", err).WithMetadata("old_object_id", expectedOldOID)
		}

		oldTargetOID, err = repo.ResolveRevision(
			ctx, git.Revision(fmt.Sprintf("%s^{object}", oldTargetOID)),
		)
		if err != nil {
			return nil, structerr.NewInvalidArgument("cannot resolve expected old object ID: %w", err).
				WithMetadata("old_object_id", expectedOldOID)
		}
	} else if targetRef, err := repo.GetReference(ctx, git.ReferenceName(request.TargetRef)); err == nil {
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

	mergeCommitID, err := s.merge(
		ctx,
		repo,
		string(request.User.Name),
		string(request.User.Email),
		authorDate,
		string(request.User.Name),
		string(request.User.Email),
		authorDate,
		string(request.Message),
		oid.String(),
		sourceOID.String(),
		false,
		false,
	)
	if err != nil {
		ctxlogrus.Extract(ctx).WithError(err).WithFields(
			logrus.Fields{
				"source_sha": sourceOID,
				"target_sha": oid,
				"target_ref": string(request.TargetRef),
			},
		).Error("unable to create merge commit")

		return nil, structerr.NewFailedPrecondition("Failed to create merge commit for source_sha %s and target_sha %s at %s",
			sourceOID, oid, string(request.TargetRef))
	}

	mergeOID, err := objectHash.FromHex(mergeCommitID)
	if err != nil {
		return nil, structerr.NewInternal("parsing merge commit SHA: %w", err)
	}

	// ... and move branch from target ref to the merge commit. The Ruby
	// implementation doesn't invoke hooks, so we don't either.
	if err := repo.UpdateRef(ctx, git.ReferenceName(request.TargetRef), mergeOID, oldTargetOID); err != nil {
		return nil, structerr.NewFailedPrecondition("Could not update %s. Please refresh and try again", string(request.TargetRef))
	}

	return &gitalypb.UserMergeToRefResponse{
		CommitId: mergeOID.String(),
	}, nil
}

func validateUserMergeToRefRequest(locator storage.Locator, in *gitalypb.UserMergeToRefRequest) error {
	if err := locator.ValidateRepository(in.GetRepository()); err != nil {
		return err
	}

	if len(in.FirstParentRef) == 0 && len(in.Branch) == 0 {
		return errors.New("empty first parent ref and branch name")
	}

	if in.User == nil {
		return errors.New("empty user")
	}

	if in.SourceSha == "" {
		return errors.New("empty source SHA")
	}

	if len(in.TargetRef) == 0 {
		return errors.New("empty target ref")
	}

	if !strings.HasPrefix(string(in.TargetRef), "refs/merge-requests") {
		return errors.New("invalid target ref")
	}

	return nil
}
