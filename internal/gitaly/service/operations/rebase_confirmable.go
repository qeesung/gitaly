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

// UserRebaseConfirmable rebases the given remote branch to the target branch.
func (s *Server) UserRebaseConfirmable(stream gitalypb.OperationService_UserRebaseConfirmableServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return err
	}

	header := firstRequest.GetHeader()
	if header == nil {
		return structerr.NewInvalidArgument("empty UserRebaseConfirmableRequest.Header")
	}

	ctx := stream.Context()
	if err := validateUserRebaseConfirmableHeader(ctx, s.locator, header); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	quarantineDir, quarantineRepo, err := s.quarantinedRepo(ctx, header.GetRepository())
	if err != nil {
		return structerr.NewInternal("creating repo quarantine: %w", err)
	}

	objectHash, err := quarantineRepo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object hash: %w", err)
	}

	branch := git.NewReferenceNameFromBranchName(string(header.Branch))
	oldrev, err := objectHash.FromHex(header.BranchSha)
	if err != nil {
		return structerr.NewNotFound("%w", err)
	}

	remoteFetch := rebaseRemoteFetch{header: header}
	startRevision, err := s.fetchStartRevision(ctx, quarantineRepo, remoteFetch)
	if err != nil {
		return structerr.NewInternal("%w", err)
	}

	committerSignature, err := git.SignatureFromRequest(header)
	if err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	newrev, err := quarantineRepo.Rebase(
		ctx,
		startRevision.String(),
		oldrev.String(),
		localrepo.RebaseWithCommitter(committerSignature),
	)
	if err != nil {
		var conflictErr *localrepo.RebaseConflictError
		if errors.As(err, &conflictErr) {
			conflictingFilesFromErr := conflictErr.ConflictError.ConflictedFiles()
			conflictingFiles := make([][]byte, 0, len(conflictingFilesFromErr))
			for _, conflictingFile := range conflictingFilesFromErr {
				conflictingFiles = append(conflictingFiles, []byte(conflictingFile))
			}
			return structerr.NewFailedPrecondition("rebasing commits: %w", conflictErr).WithDetail(
				&gitalypb.UserRebaseConfirmableError{
					Error: &gitalypb.UserRebaseConfirmableError_RebaseConflict{
						RebaseConflict: &gitalypb.MergeConflictError{
							ConflictingFiles: conflictingFiles,
							ConflictingCommitIds: []string{
								startRevision.String(),
								oldrev.String(),
							},
						},
					},
				},
			)
		}

		return structerr.NewInternal("rebasing commits: %w", err)
	}

	if err := stream.Send(&gitalypb.UserRebaseConfirmableResponse{
		UserRebaseConfirmableResponsePayload: &gitalypb.UserRebaseConfirmableResponse_RebaseSha{
			RebaseSha: newrev.String(),
		},
	}); err != nil {
		return structerr.NewInternal("send rebase sha: %w", err)
	}

	secondRequest, err := stream.Recv()
	if err != nil {
		return structerr.NewInternal("recv: %w", err)
	}

	if !secondRequest.GetApply() {
		return structerr.NewFailedPrecondition("rebase aborted by client")
	}

	if err := s.updateReferenceWithHooks(
		ctx,
		header.GetRepository(),
		header.User,
		quarantineDir,
		branch,
		newrev,
		oldrev,
		header.GitPushOptions...,
	); err != nil {
		var customHookErr updateref.CustomHookError
		switch {
		case errors.As(err, &customHookErr):
			//nolint:gitaly-linters
			return structerr.NewPermissionDenied("access check: %q", err).WithDetail(
				&gitalypb.UserRebaseConfirmableError{
					Error: &gitalypb.UserRebaseConfirmableError_AccessCheck{
						AccessCheck: &gitalypb.AccessCheckError{
							ErrorMessage: customHookErr.Error(),
						},
					},
				},
			)
		}

		return structerr.NewInternal("updating ref with hooks: %w", err)
	}

	return stream.Send(&gitalypb.UserRebaseConfirmableResponse{
		UserRebaseConfirmableResponsePayload: &gitalypb.UserRebaseConfirmableResponse_RebaseApplied{
			RebaseApplied: true,
		},
	})
}

// ErrInvalidBranch indicates a branch name is invalid
var ErrInvalidBranch = errors.New("invalid branch name")

func validateUserRebaseConfirmableHeader(ctx context.Context, locator storage.Locator, header *gitalypb.UserRebaseConfirmableRequest_Header) error {
	if err := locator.ValidateRepository(ctx, header.GetRepository()); err != nil {
		return err
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

func validateUserRebaseToRefRequest(ctx context.Context, locator storage.Locator, in *gitalypb.UserRebaseToRefRequest) error {
	if err := locator.ValidateRepository(ctx, in.GetRepository()); err != nil {
		return err
	}

	if len(in.FirstParentRef) == 0 {
		return errors.New("empty FirstParentRef")
	}

	if in.User == nil {
		return errors.New("empty User")
	}

	if in.SourceSha == "" {
		return errors.New("empty SourceSha")
	}

	if len(in.TargetRef) == 0 {
		return errors.New("empty TargetRef")
	}

	if !strings.HasPrefix(string(in.TargetRef), "refs/merge-requests") {
		return errors.New("invalid TargetRef")
	}

	return nil
}
