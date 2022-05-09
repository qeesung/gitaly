package operations

import (
	"errors"
	"fmt"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

//nolint: revive,stylecheck // This is unintentionally missing documentation.
func (s *Server) UserRebaseConfirmable(stream gitalypb.OperationService_UserRebaseConfirmableServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return err
	}

	header := firstRequest.GetHeader()
	if header == nil {
		return helper.ErrInvalidArgument(errors.New("UserRebaseConfirmable: empty UserRebaseConfirmableRequest.Header"))
	}

	if err := validateUserRebaseConfirmableHeader(header); err != nil {
		return helper.ErrInvalidArgumentf("UserRebaseConfirmable: %v", err)
	}

	ctx := stream.Context()

	quarantineDir, quarantineRepo, err := s.quarantinedRepo(ctx, header.GetRepository())
	if err != nil {
		return helper.ErrInternalf("creating repo quarantine: %w", err)
	}

	repoPath, err := quarantineRepo.Path()
	if err != nil {
		return err
	}

	branch := git.NewReferenceNameFromBranchName(string(header.Branch))
	oldrev, err := git.NewObjectIDFromHex(header.BranchSha)
	if err != nil {
		return helper.ErrNotFound(err)
	}

	remoteFetch := rebaseRemoteFetch{header: header}
	startRevision, err := s.fetchStartRevision(ctx, quarantineRepo, remoteFetch)
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}

	committer := git2go.NewSignature(string(header.User.Name), string(header.User.Email), time.Now())
	if header.Timestamp != nil {
		committer.When = header.Timestamp.AsTime()
	}

	newrev, err := s.git2goExecutor.Rebase(ctx, quarantineRepo, git2go.RebaseCommand{
		Repository:       repoPath,
		Committer:        committer,
		CommitID:         oldrev,
		UpstreamCommitID: startRevision,
		SkipEmptyCommits: true,
	})
	if err != nil {
		if featureflag.UserRebaseConfirmableImprovedErrorHandling.IsEnabled(ctx) {
			var conflictErr git2go.ConflictingFilesError
			if errors.As(err, &conflictErr) {
				conflictingFiles := make([][]byte, 0, len(conflictErr.ConflictingFiles))
				for _, conflictingFile := range conflictErr.ConflictingFiles {
					conflictingFiles = append(conflictingFiles, []byte(conflictingFile))
				}

				detailedErr, err := helper.ErrWithDetails(
					helper.ErrFailedPreconditionf("rebasing commits: %w", err),
					&gitalypb.UserRebaseConfirmableError{
						Error: &gitalypb.UserRebaseConfirmableError_RebaseConflict{
							RebaseConflict: &gitalypb.MergeConflictError{
								ConflictingFiles: conflictingFiles,
							},
						},
					},
				)
				if err != nil {
					return helper.ErrInternalf("error details: %w", err)
				}

				return detailedErr
			}

			return helper.ErrInternalf("rebasing commits: %w", err)
		}

		return stream.Send(&gitalypb.UserRebaseConfirmableResponse{
			GitError: err.Error(),
		})
	}

	if err := stream.Send(&gitalypb.UserRebaseConfirmableResponse{
		UserRebaseConfirmableResponsePayload: &gitalypb.UserRebaseConfirmableResponse_RebaseSha{
			RebaseSha: newrev.String(),
		},
	}); err != nil {
		return fmt.Errorf("send rebase sha: %w", err)
	}

	secondRequest, err := stream.Recv()
	if err != nil {
		return helper.ErrInternalf("recv: %w", err)
	}

	if !secondRequest.GetApply() {
		return helper.ErrFailedPreconditionf("rebase aborted by client")
	}

	if err := s.updateReferenceWithHooks(
		ctx,
		header.GetRepository(),
		header.User,
		quarantineDir,
		branch,
		newrev,
		oldrev,
		header.GitPushOptions...); err != nil {
		switch {
		case errors.As(err, &updateref.HookError{}):
			if featureflag.UserRebaseConfirmableImprovedErrorHandling.IsEnabled(ctx) {
				detailedErr, err := helper.ErrWithDetails(
					helper.ErrPermissionDeniedf("access check: %q", err),
					&gitalypb.UserRebaseConfirmableError{
						Error: &gitalypb.UserRebaseConfirmableError_AccessCheck{
							AccessCheck: &gitalypb.AccessCheckError{
								ErrorMessage: err.Error(),
							},
						},
					},
				)
				if err != nil {
					return helper.ErrInternalf("error details: %w", err)
				}
				return detailedErr
			}
			return stream.Send(&gitalypb.UserRebaseConfirmableResponse{
				PreReceiveError: err.Error(),
			})
		case errors.Is(err, git2go.ErrInvalidArgument):
			return fmt.Errorf("update ref: %w", err)
		}

		return err
	}

	return stream.Send(&gitalypb.UserRebaseConfirmableResponse{
		UserRebaseConfirmableResponsePayload: &gitalypb.UserRebaseConfirmableResponse_RebaseApplied{
			RebaseApplied: true,
		},
	})
}

// ErrInvalidBranch indicates a branch name is invalid
var ErrInvalidBranch = errors.New("invalid branch name")

func validateUserRebaseConfirmableHeader(header *gitalypb.UserRebaseConfirmableRequest_Header) error {
	if header.GetRepository() == nil {
		return errors.New("empty Repository")
	}

	if header.GetUser() == nil {
		return errors.New("empty User")
	}

	if header.GetBranch() == nil {
		return errors.New("empty Branch")
	}

	if header.GetBranchSha() == "" {
		return errors.New("empty BranchSha")
	}

	if header.GetRemoteRepository() == nil {
		return errors.New("empty RemoteRepository")
	}

	if header.GetRemoteBranch() == nil {
		return errors.New("empty RemoteBranch")
	}

	if err := git.ValidateRevision(header.GetRemoteBranch()); err != nil {
		return ErrInvalidBranch
	}

	return nil
}

// rebaseRemoteFetch is an intermediate type that implements the
// `requestFetchingStartRevision` interface. This allows us to use
// `fetchStartRevision` to get the revision to rebase onto.
type rebaseRemoteFetch struct {
	header *gitalypb.UserRebaseConfirmableRequest_Header
}

func (r rebaseRemoteFetch) GetRepository() *gitalypb.Repository {
	return r.header.GetRepository()
}

func (r rebaseRemoteFetch) GetBranchName() []byte {
	return r.header.GetBranch()
}

func (r rebaseRemoteFetch) GetStartRepository() *gitalypb.Repository {
	return r.header.GetRemoteRepository()
}

func (r rebaseRemoteFetch) GetStartBranchName() []byte {
	return r.header.GetRemoteBranch()
}
