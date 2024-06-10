package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// Error strings present in the legacy Ruby implementation.
const (
	legacyErrPrefixInvalidSubmodulePath = "Invalid submodule path"
)

// UserUpdateSubmodule updates a submodule to point to a new commit.
func (s *Server) UserUpdateSubmodule(ctx context.Context, req *gitalypb.UserUpdateSubmoduleRequest) (*gitalypb.UserUpdateSubmoduleResponse, error) {
	if err := s.locator.ValidateRepository(ctx, req.GetRepository()); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	quarantineDir, quarantineRepo, err := s.quarantinedRepo(ctx, req.GetRepository())
	if err != nil {
		return nil, err
	}

	objectHash, err := quarantineRepo.ObjectHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting object hash: %w", err)
	}

	if err := validateUserUpdateSubmoduleRequest(s.locator, objectHash, req); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	branches, err := quarantineRepo.GetBranches(ctx)
	if err != nil {
		return nil, structerr.NewInternal("get branches: %w", err)
	}
	if len(branches) == 0 {
		return &gitalypb.UserUpdateSubmoduleResponse{
			CommitError: "Repository is empty",
		}, nil
	}

	referenceName := git.NewReferenceNameFromBranchName(string(req.GetBranch()))

	var oldOID git.ObjectID
	if expectedOldOID := req.GetExpectedOldOid(); expectedOldOID != "" {
		objectHash, err := quarantineRepo.ObjectHash(ctx)
		if err != nil {
			return nil, structerr.NewInternal("detecting object hash: %w", err)
		}

		oldOID, err = objectHash.FromHex(expectedOldOID)
		if err != nil {
			return nil, structerr.NewInvalidArgument("invalid expected old object ID: %w", err).WithMetadata("old_object_id", expectedOldOID)
		}

		oldOID, err = quarantineRepo.ResolveRevision(ctx, git.Revision(fmt.Sprintf("%s^{object}", oldOID)))
		if err != nil {
			return nil, structerr.NewInvalidArgument("cannot resolve expected old object ID: %w", err).
				WithMetadata("old_object_id", expectedOldOID)
		}
	} else {
		oldOID, err = quarantineRepo.ResolveRevision(ctx, referenceName.Revision())
		if err != nil {
			if errors.Is(err, git.ErrReferenceNotFound) {
				return nil, structerr.NewInvalidArgument("Cannot find branch")
			}
			return nil, structerr.NewInternal("resolving revision: %w", err)
		}
	}

	commitID, err := s.updateSubmodule(ctx, quarantineRepo, req)
	if err != nil {
		errStr := strings.TrimSpace(err.Error())

		var resp *gitalypb.UserUpdateSubmoduleResponse
		if strings.Contains(errStr, legacyErrPrefixInvalidSubmodulePath) {
			resp = &gitalypb.UserUpdateSubmoduleResponse{
				CommitError: legacyErrPrefixInvalidSubmodulePath,
			}
			s.logger.
				WithError(err).
				ErrorContext(ctx, "UserUpdateSubmodule: git2go subcommand failure")
		}
		if strings.Contains(errStr, "is already at") {
			resp = &gitalypb.UserUpdateSubmoduleResponse{
				CommitError: errStr,
			}
		}
		if resp != nil {
			return resp, nil
		}

		return nil, structerr.NewInternal("submodule subcommand: %w", err)
	}

	commitOID, err := objectHash.FromHex(commitID)
	if err != nil {
		return nil, structerr.NewInvalidArgument("cannot parse commit ID: %w", err)
	}

	if err := s.updateReferenceWithHooks(
		ctx,
		req.GetRepository(),
		req.GetUser(),
		quarantineDir,
		referenceName,
		commitOID,
		oldOID,
	); err != nil {
		var customHookErr updateref.CustomHookError
		if errors.As(err, &customHookErr) {
			return &gitalypb.UserUpdateSubmoduleResponse{
				PreReceiveError: customHookErr.Error(),
			}, nil
		}

		var updateRefError updateref.Error
		if errors.As(err, &updateRefError) {
			return &gitalypb.UserUpdateSubmoduleResponse{
				// TODO: this needs to be converted to a structured error, and once done we should stop
				// returning this Ruby-esque error message in favor of the actual error that was
				// returned by `updateReferenceWithHooks()`.
				CommitError: fmt.Sprintf("Could not update %s. Please refresh and try again.", updateRefError.Reference),
			}, nil
		}

		return nil, structerr.NewInternal("updating ref with hooks: %w", err)
	}

	return &gitalypb.UserUpdateSubmoduleResponse{
		BranchUpdate: &gitalypb.OperationBranchUpdate{
			CommitId:      commitID,
			BranchCreated: false,
			RepoCreated:   false,
		},
	}, nil
}

func validateUserUpdateSubmoduleRequest(locator storage.Locator, objectHash git.ObjectHash, req *gitalypb.UserUpdateSubmoduleRequest) error {
	if req.GetUser() == nil {
		return errors.New("empty User")
	}

	if req.GetCommitSha() == "" {
		return errors.New("empty CommitSha")
	}

	if err := objectHash.ValidateHex(req.GetCommitSha()); err != nil {
		return errors.New("invalid CommitSha")
	}

	if len(req.GetBranch()) == 0 {
		return errors.New("empty Branch")
	}

	if len(req.GetSubmodule()) == 0 {
		return errors.New("empty Submodule")
	}

	if len(req.GetCommitMessage()) == 0 {
		return errors.New("empty CommitMessage")
	}

	return nil
}

// legacyGit2GoSubmoduleAlreadyAtShaErr is used to maintain backwards
// compatibility with the git2go error.
type legacyGit2GoSubmoduleAlreadyAtShaError struct {
	submodulePath string
	commitSha     string
}

func (l *legacyGit2GoSubmoduleAlreadyAtShaError) Error() string {
	return fmt.Sprintf("The submodule %s is already at %s", l.submodulePath, l.commitSha)
}

func (s *Server) updateSubmodule(ctx context.Context, quarantineRepo *localrepo.Repo, req *gitalypb.UserUpdateSubmoduleRequest) (string, error) {
	var treeID git.ObjectID

	fullTree, err := quarantineRepo.ReadTree(
		ctx,
		git.NewReferenceNameFromBranchName(string(req.GetBranch())).Revision(),
		localrepo.WithRecursive(),
	)
	if err != nil {
		if errors.Is(err, git.ErrReferenceNotFound) {
			return "", fmt.Errorf("submodule: %s", legacyErrPrefixInvalidSubmodulePath)
		}

		return "", fmt.Errorf("error reading tree: %w", err)
	}

	if err := fullTree.Modify(
		string(req.GetSubmodule()),
		func(t *localrepo.TreeEntry) error {
			replaceWith := git.ObjectID(req.GetCommitSha())

			if t.Type != localrepo.Submodule {
				return fmt.Errorf("submodule: %s", legacyErrPrefixInvalidSubmodulePath)
			}

			if replaceWith == t.OID {
				return &legacyGit2GoSubmoduleAlreadyAtShaError{
					submodulePath: string(req.GetSubmodule()),
					commitSha:     string(replaceWith),
				}
			}

			t.OID = replaceWith

			return nil
		},
	); err != nil {
		if errors.Is(err, localrepo.ErrEntryNotFound) {
			return "", fmt.Errorf("submodule: %s", legacyErrPrefixInvalidSubmodulePath)
		}

		var git2GoErr *legacyGit2GoSubmoduleAlreadyAtShaError
		if errors.As(err, &git2GoErr) {
			return "", err
		}

		return "", fmt.Errorf("modifying tree: %w", err)
	}

	if err := fullTree.Write(ctx, quarantineRepo); err != nil {
		return "", fmt.Errorf("writing tree: %w", err)
	}

	treeID = fullTree.OID
	currentBranchCommit, err := quarantineRepo.ResolveRevision(ctx, git.Revision(req.GetBranch()))
	if err != nil {
		return "", fmt.Errorf("resolving submodule branch: %w", err)
	}

	authorSignature, err := git.SignatureFromRequest(req)
	if err != nil {
		return "", structerr.NewInvalidArgument("%w", err)
	}

	newCommitID, err := quarantineRepo.WriteCommit(ctx, localrepo.WriteCommitConfig{
		Parents:        []git.ObjectID{currentBranchCommit},
		AuthorDate:     authorSignature.When,
		AuthorName:     authorSignature.Name,
		AuthorEmail:    authorSignature.Email,
		CommitterName:  authorSignature.Name,
		CommitterEmail: authorSignature.Email,
		CommitterDate:  authorSignature.When,
		Message:        string(req.GetCommitMessage()),
		TreeID:         treeID,
	})
	if err != nil {
		return "", fmt.Errorf("creating commit %w", err)
	}

	return string(newCommitID), nil
}
