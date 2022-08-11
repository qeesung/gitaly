package operations

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

//nolint: stylecheck // This is unintentionally missing documentation.
func (s *Server) UserCherryPick(ctx context.Context, req *gitalypb.UserCherryPickRequest) (*gitalypb.UserCherryPickResponse, error) {
	if err := validateCherryPickOrRevertRequest(req); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "UserCherryPick: %v", err)
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
		return nil, err
	}

	repoPath, err := quarantineRepo.Path()
	if err != nil {
		return nil, err
	}

	var mainline uint
	if len(req.Commit.ParentIds) > 1 {
		mainline = 1
	}

	committerDate := time.Now()
	if req.Timestamp != nil {
		committerDate = req.Timestamp.AsTime()
	}

	newrev, err := s.git2goExecutor.CherryPick(ctx, quarantineRepo, git2go.CherryPickCommand{
		Repository:    repoPath,
		CommitterName: string(req.User.Name),
		CommitterMail: string(req.User.Email),
		CommitterDate: committerDate,
		Message:       string(req.Message),
		Commit:        req.Commit.Id,
		Ours:          startRevision.String(),
		Mainline:      mainline,
	})
	if err != nil {
		var conflictErr git2go.ConflictingFilesError
		var emptyErr git2go.EmptyError

		switch {
		case errors.As(err, &conflictErr):
			conflictingFiles := make([][]byte, 0, len(conflictErr.ConflictingFiles))
			for _, conflictingFile := range conflictErr.ConflictingFiles {
				conflictingFiles = append(conflictingFiles, []byte(conflictingFile))
			}

			detailedErr, errGeneratingDetailedErr := helper.ErrWithDetails(
				helper.ErrFailedPreconditionf("cherry pick: %w", err),
				&gitalypb.UserCherryPickError{
					Error: &gitalypb.UserCherryPickError_CherryPickConflict{
						CherryPickConflict: &gitalypb.MergeConflictError{
							ConflictingFiles: conflictingFiles,
						},
					},
				},
			)
			if errGeneratingDetailedErr != nil {
				return nil, helper.ErrInternalf("error details: %w", err)
			}

			return nil, detailedErr
		case errors.As(err, &emptyErr):
			detailedErr, errGeneratingDetailedErr := helper.ErrWithDetails(
				helper.ErrFailedPrecondition(err),
				&gitalypb.UserCherryPickError{
					Error: &gitalypb.UserCherryPickError_ChangesAlreadyApplied{},
				},
			)
			if errGeneratingDetailedErr != nil {
				return nil, helper.ErrInternalf("error details: %w", err)
			}

			return nil, detailedErr
		case errors.Is(err, git2go.ErrInvalidArgument):
			return nil, helper.ErrInvalidArgument(err)
		default:
			return nil, helper.ErrInternalf("cherry-pick command: %w", err)
		}
	}

	referenceName := git.NewReferenceNameFromBranchName(string(req.BranchName))

	branchCreated := false
	oldrev, err := quarantineRepo.ResolveRevision(ctx, referenceName.Revision()+"^{commit}")
	if errors.Is(err, git.ErrReferenceNotFound) {
		branchCreated = true
		oldrev = git.ObjectHashSHA1.ZeroOID
	} else if err != nil {
		return nil, helper.ErrInvalidArgumentf("resolve ref: %w", err)
	}

	if req.DryRun {
		newrev = startRevision
	}

	if !branchCreated {
		ancestor, err := quarantineRepo.IsAncestor(ctx, oldrev.Revision(), newrev.Revision())
		if err != nil {
			return nil, err
		}
		if !ancestor {
			detailedErr, errGeneratingDetailedErr := helper.ErrWithDetails(
				helper.ErrFailedPrecondition(errors.New("cherry-pick: branch diverged")),
				&gitalypb.UserCherryPickError{
					Error: &gitalypb.UserCherryPickError_TargetBranchDiverged{
						TargetBranchDiverged: &gitalypb.NotAncestorError{
							ParentRevision: []byte(oldrev.Revision()),
							ChildRevision:  []byte(newrev),
						},
					},
				})

			if errGeneratingDetailedErr != nil {
				return nil, helper.ErrInternalf("error details: %w", err)
			}

			return nil, detailedErr
		}
	}

	if err := s.updateReferenceWithHooks(ctx, req.GetRepository(), req.User, quarantineDir, referenceName, newrev, oldrev); err != nil {
		var customHookErr updateref.CustomHookError

		if errors.As(err, &customHookErr) {
			detailedErr, errGeneratingDetailedErr := helper.ErrWithDetails(
				helper.ErrFailedPrecondition(errors.New("access check failed")),
				&gitalypb.UserCherryPickError{
					Error: &gitalypb.UserCherryPickError_AccessCheck{
						AccessCheck: &gitalypb.AccessCheckError{
							ErrorMessage: strings.TrimSuffix(customHookErr.Error(), "\n"),
						},
					},
				})

			if errGeneratingDetailedErr != nil {
				return nil, helper.ErrInternalf("error details: %w", err)
			}

			return nil, detailedErr
		}

		if errors.As(err, &customHookErr) {
			return &gitalypb.UserCherryPickResponse{
				PreReceiveError: customHookErr.Error(),
			}, nil
		}

		return nil, fmt.Errorf("update reference with hooks: %w", err)
	}

	return &gitalypb.UserCherryPickResponse{
		BranchUpdate: &gitalypb.OperationBranchUpdate{
			CommitId:      newrev.String(),
			BranchCreated: branchCreated,
			RepoCreated:   !repoHadBranches,
		},
	}, nil
}
