package operations

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/remoterepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

//nolint:revive // This is unintentionally missing documentation.
func (s *Server) UserRevert(ctx context.Context, req *gitalypb.UserRevertRequest) (*gitalypb.UserRevertResponse, error) {
	if err := validateCherryPickOrRevertRequest(s.locator, req); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	quarantineDir, quarantineRepo, err := s.quarantinedRepo(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	startRevision, err := s.fetchStartRevision(ctx, quarantineRepo, req)
	if err != nil {
		return nil, err
	}

	repoHadBranches, err := quarantineRepo.HasBranches(ctx)
	if err != nil {
		return nil, structerr.NewInternal("has branches: %w", err)
	}

	committerSignature, err := git.SignatureFromRequest(req)
	if err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	var newrev git.ObjectID

	oursCommit, err := quarantineRepo.ReadCommit(ctx, startRevision.Revision())
	if err != nil {
		if errors.Is(err, localrepo.ErrObjectNotFound) {
			return nil, structerr.NewNotFound("ours commit lookup: commit not found").
				WithMetadata("revision", startRevision)
		}

		return nil, structerr.NewInternal("read commit: %w", err)
	}
	revertCommit, err := quarantineRepo.ReadCommit(ctx, git.Revision(req.Commit.Id))
	if err != nil {
		if errors.Is(err, localrepo.ErrObjectNotFound) {
			return nil, structerr.NewNotFound("revert commit lookup: commit not found").
				WithMetadata("revision", req.Commit.Id)
		}

		return nil, structerr.NewInternal("read commit: %w", err)
	}

	var theirs git.ObjectID
	if len(revertCommit.ParentIds) > 0 {
		// Use the first parent as `theirs` to implement mainline = 1.
		theirs = git.ObjectID(revertCommit.ParentIds[0])
	} else {
		// Both "ours" and "theirs" must exist for git-merge-tree. When the commit
		// to be reverted has no parents, it is safe to assume its parent is a
		// commit with empty tree.
		theirs, err = s.writeCommitWithEmptyTree(ctx, quarantineRepo)
		if err != nil {
			return nil, structerr.NewInternal("write temporary commit: %w", err)
		}
	}

	// We "merge" in the "changes" from parent to child, which would apply the "opposite"
	// patch to "ours", thus reverting the commit.
	treeOID, err := quarantineRepo.MergeTree(
		ctx,
		oursCommit.Id,
		theirs.String(),
		localrepo.WithMergeBase(git.Revision(revertCommit.Id)),
		localrepo.WithConflictingFileNamesOnly(),
	)
	if err != nil {
		var conflictErr *localrepo.MergeTreeConflictError
		if errors.As(err, &conflictErr) {
			if featureflag.ReturnStructuredErrorsInUserRevert.IsEnabled(ctx) {
				conflictingFiles := make([][]byte, 0, len(conflictErr.ConflictingFileInfo))
				for _, conflictingFileInfo := range conflictErr.ConflictingFileInfo {
					conflictingFiles = append(conflictingFiles, []byte(conflictingFileInfo.FileName))
				}
				return nil, structerr.NewFailedPrecondition("revert: there are conflicting files").WithDetail(
					&gitalypb.UserRevertError{
						Error: &gitalypb.UserRevertError_MergeConflict{
							MergeConflict: &gitalypb.MergeConflictError{
								ConflictingFiles: conflictingFiles,
							},
						},
					})
			} else {
				return &gitalypb.UserRevertResponse{
					// it's better that this error matches the git2go for now
					CreateTreeError:     "revert: could not apply due to conflicts",
					CreateTreeErrorCode: gitalypb.UserRevertResponse_CONFLICT,
				}, nil
			}
		}

		return nil, structerr.NewInternal("merge-tree: %w", err)
	}

	if oursCommit.TreeId == treeOID.String() {
		if featureflag.ReturnStructuredErrorsInUserRevert.IsEnabled(ctx) {
			return nil, structerr.NewFailedPrecondition("revert: could not apply because the result was empty").WithDetail(
				&gitalypb.UserRevertError{
					Error: &gitalypb.UserRevertError_ChangesAlreadyApplied{
						ChangesAlreadyApplied: &gitalypb.ChangesAlreadyAppliedError{},
					},
				})
		} else {
			return &gitalypb.UserRevertResponse{
				// it's better that this error matches the git2go for now
				CreateTreeError:     "revert: could not apply because the result was empty",
				CreateTreeErrorCode: gitalypb.UserRevertResponse_EMPTY,
			}, nil
		}
	}

	newrev, err = quarantineRepo.WriteCommit(
		ctx,
		localrepo.WriteCommitConfig{
			TreeID:         treeOID,
			Message:        string(req.Message),
			Parents:        []git.ObjectID{startRevision},
			AuthorName:     committerSignature.Name,
			AuthorEmail:    committerSignature.Email,
			AuthorDate:     committerSignature.When,
			CommitterName:  committerSignature.Name,
			CommitterEmail: committerSignature.Email,
			CommitterDate:  committerSignature.When,
			GitConfig:      s.gitConfig,
		},
	)
	if err != nil {
		return nil, structerr.NewInternal("write commit: %w", err)
	}

	referenceName := git.NewReferenceNameFromBranchName(string(req.BranchName))
	branchCreated := false
	var oldrev git.ObjectID

	objectHash, err := quarantineRepo.ObjectHash(ctx)
	if err != nil {
		return nil, structerr.NewInternal("detecting object hash: %w", err)
	}

	if expectedOldOID := req.GetExpectedOldOid(); expectedOldOID != "" {
		oldrev, err = objectHash.FromHex(expectedOldOID)
		if err != nil {
			return nil, structerr.NewInvalidArgument("invalid expected old object ID: %w", err).WithMetadata("old_object_id", expectedOldOID)
		}

		oldrev, err = quarantineRepo.ResolveRevision(
			ctx, git.Revision(fmt.Sprintf("%s^{object}", oldrev)),
		)
		if err != nil {
			return nil, structerr.NewInvalidArgument("cannot resolve expected old object ID: %w", err).
				WithMetadata("old_object_id", expectedOldOID)
		}
	} else {
		oldrev, err = quarantineRepo.ResolveRevision(ctx, referenceName.Revision()+"^{commit}")
		if errors.Is(err, git.ErrReferenceNotFound) {
			branchCreated = true
			oldrev = objectHash.ZeroOID
		} else if err != nil {
			return nil, structerr.NewInvalidArgument("resolve ref: %w", err)
		}
	}

	if req.DryRun {
		newrev = startRevision
	}

	if !branchCreated {
		ancestor, err := quarantineRepo.IsAncestor(ctx, oldrev.Revision(), newrev.Revision())
		if err != nil {
			return nil, structerr.NewInternal("checking for ancestry: %w", err)
		}
		if !ancestor {
			if featureflag.ReturnStructuredErrorsInUserRevert.IsEnabled(ctx) {
				return nil, structerr.NewFailedPrecondition("revert: branch diverged").WithDetail(
					&gitalypb.UserRevertError{
						Error: &gitalypb.UserRevertError_NotAncestor{
							NotAncestor: &gitalypb.NotAncestorError{
								ParentRevision: []byte(oldrev.Revision()),
								ChildRevision:  []byte(newrev.Revision()),
							},
						},
					})
			} else {
				return &gitalypb.UserRevertResponse{
					CommitError: "Branch diverged",
				}, nil
			}
		}
	}

	if err := s.updateReferenceWithHooks(ctx, req.GetRepository(), req.User, quarantineDir, referenceName, newrev, oldrev); err != nil {
		var customHookErr updateref.CustomHookError
		if errors.As(err, &customHookErr) {
			if featureflag.ReturnStructuredErrorsInUserRevert.IsEnabled(ctx) {
				return nil, structerr.NewPermissionDenied("revert: custom hook error").WithDetail(
					&gitalypb.UserRevertError{
						Error: &gitalypb.UserRevertError_CustomHook{
							CustomHook: customHookErr.Proto(),
						},
					})
			} else {
				return &gitalypb.UserRevertResponse{
					PreReceiveError: customHookErr.Error(),
				}, nil
			}
		}

		return nil, fmt.Errorf("update reference with hooks: %w", err)
	}

	return &gitalypb.UserRevertResponse{
		BranchUpdate: &gitalypb.OperationBranchUpdate{
			CommitId:      newrev.String(),
			BranchCreated: branchCreated,
			RepoCreated:   !repoHadBranches,
		},
	}, nil
}

// writeCommitWithEmptyTree writes a dangling commit with empty tree. The commit would
// be used temporarily by git-merge-tree to handle special scenarios such as reverting
// commits with no parents.
// The dangling commit will not be used anywhere permanently and will get cleaned up by
// housekeeping.
func (s *Server) writeCommitWithEmptyTree(ctx context.Context, quarantineRepo *localrepo.Repo) (git.ObjectID, error) {
	const fakeName = "GitLab Bot"
	const fakeEmail = "gitlab-bot@gitlab.com"
	fakeDate := time.Unix(694540800, 0).UTC()

	hash, err := quarantineRepo.ObjectHash(ctx)
	if err != nil {
		return "", err
	}

	return quarantineRepo.WriteCommit(ctx, localrepo.WriteCommitConfig{
		AuthorName:     fakeName,
		AuthorEmail:    fakeEmail,
		AuthorDate:     fakeDate,
		CommitterName:  fakeName,
		CommitterEmail: fakeEmail,
		CommitterDate:  fakeDate,
		TreeID:         hash.EmptyTreeOID,
	})
}

type requestFetchingStartRevision interface {
	GetBranchName() []byte
	GetStartRepository() *gitalypb.Repository
	GetStartBranchName() []byte
}

func (s *Server) fetchStartRevision(
	ctx context.Context,
	localRepo *localrepo.Repo,
	req requestFetchingStartRevision,
) (git.ObjectID, error) {
	startBranchName := req.GetStartBranchName()
	if len(startBranchName) == 0 {
		startBranchName = req.GetBranchName()
	}

	var remoteRepo git.Repository = localRepo
	if startRepository := req.GetStartRepository(); startRepository != nil {
		var err error
		remoteRepo, err = remoterepo.New(ctx, startRepository, s.conns)
		if err != nil {
			return "", structerr.NewInternal("%w", err)
		}
	}

	startRevision, err := remoteRepo.ResolveRevision(ctx, git.Revision(fmt.Sprintf("%s^{commit}", startBranchName)))
	if err != nil {
		return "", structerr.NewInvalidArgument("resolve start ref: %w", err)
	}

	if req.GetStartRepository() == nil {
		return startRevision, nil
	}

	_, err = localRepo.ResolveRevision(ctx, startRevision.Revision()+"^{commit}")
	if errors.Is(err, git.ErrReferenceNotFound) {
		if err := localRepo.FetchInternal(
			ctx,
			req.GetStartRepository(),
			[]string{startRevision.String()},
			localrepo.FetchOpts{Tags: localrepo.FetchOptsTagsNone},
		); err != nil {
			return "", structerr.NewInternal("fetch start: %w", err)
		}
	} else if err != nil {
		return "", structerr.NewInvalidArgument("resolve start: %w", err)
	}

	return startRevision, nil
}
