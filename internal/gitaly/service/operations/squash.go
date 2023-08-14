package operations

import (
	"context"
	"errors"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

const (
	gitlabWorktreesSubDir = "gitlab-worktree"
)

// UserSquash collapses a range of commits identified via a start and end revision into a single
// commit whose single parent is the start revision.
func (s *Server) UserSquash(ctx context.Context, req *gitalypb.UserSquashRequest) (*gitalypb.UserSquashResponse, error) {
	if err := validateUserSquashRequest(s.locator, req); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	sha, err := s.userSquash(ctx, req)
	if err != nil {
		return nil, structerr.NewInternal("%w", err)
	}

	return &gitalypb.UserSquashResponse{SquashSha: sha}, nil
}

func validateUserSquashRequest(locator storage.Locator, req *gitalypb.UserSquashRequest) error {
	if err := locator.ValidateRepository(req.GetRepository()); err != nil {
		return err
	}

	if req.GetUser() == nil {
		return errors.New("empty User")
	}

	if len(req.GetUser().GetName()) == 0 {
		return errors.New("empty user name")
	}

	if len(req.GetUser().GetEmail()) == 0 {
		return errors.New("empty user email")
	}

	if req.GetStartSha() == "" {
		return errors.New("empty StartSha")
	}

	if req.GetEndSha() == "" {
		return errors.New("empty EndSha")
	}

	if len(req.GetCommitMessage()) == 0 {
		return errors.New("empty CommitMessage")
	}

	if req.GetAuthor() == nil {
		return errors.New("empty Author")
	}

	if len(req.GetAuthor().GetName()) == 0 {
		return errors.New("empty author name")
	}

	if len(req.GetAuthor().GetEmail()) == 0 {
		return errors.New("empty author email")
	}

	return nil
}

func (s *Server) userSquash(ctx context.Context, req *gitalypb.UserSquashRequest) (string, error) {
	// All new objects are staged into a quarantine directory first so that we can do
	// transactional voting before we commit data to disk.
	quarantineDir, quarantineRepo, err := s.quarantinedRepo(ctx, req.GetRepository())
	if err != nil {
		return "", structerr.NewInternal("creating quarantine: %w", err)
	}

	// We need to retrieve the start commit such that we can create the new commit with
	// all parents of the start commit.
	startCommit, err := quarantineRepo.ResolveRevision(ctx, git.Revision(req.GetStartSha()+"^{commit}"))
	if err != nil {
		return "", structerr.NewInvalidArgument("resolving start revision: %w", err).WithDetail(
			&gitalypb.UserSquashError{
				Error: &gitalypb.UserSquashError_ResolveRevision{
					ResolveRevision: &gitalypb.ResolveRevisionError{
						Revision: []byte(req.GetStartSha()),
					},
				},
			},
		)
	}

	// And we need to take the tree of the end commit. This tree already is the result
	endCommit, err := quarantineRepo.ResolveRevision(ctx, git.Revision(req.GetEndSha()+"^{commit}"))
	if err != nil {
		return "", structerr.NewInvalidArgument("resolving end revision: %w", err).WithDetail(
			&gitalypb.UserSquashError{
				Error: &gitalypb.UserSquashError_ResolveRevision{
					ResolveRevision: &gitalypb.ResolveRevisionError{
						Revision: []byte(req.GetEndSha()),
					},
				},
			},
		)
	}

	commitDate, err := dateFromProto(req)
	if err != nil {
		return "", structerr.NewInvalidArgument("%w", err)
	}

	message := string(req.GetCommitMessage())
	// In previous implementation, we've used git commit-tree to create commit.
	// When message wasn't empty and didn't end in a new line,
	// git commit-tree would add a trailing new line to the commit message.
	// Let's keep that behaviour for compatibility.
	if len(message) > 0 && !strings.HasSuffix(message, "\n") {
		message += "\n"
	}

	commitID, err := s.merge(
		ctx,
		quarantineRepo,
		string(req.GetAuthor().GetName()),
		string(req.GetAuthor().GetEmail()),
		commitDate,
		string(req.GetUser().GetName()),
		string(req.GetUser().GetEmail()),
		commitDate,
		message,
		startCommit.String(),
		endCommit.String(),
		true,
		true,
	)
	if err != nil {
		var conflictErr git2go.ConflictingFilesError

		if errors.As(err, &conflictErr) {
			conflictingFiles := make([][]byte, 0, len(conflictErr.ConflictingFiles))
			for _, conflictingFile := range conflictErr.ConflictingFiles {
				conflictingFiles = append(conflictingFiles, []byte(conflictingFile))
			}

			return "", structerr.NewFailedPrecondition("squashing commits: %w", err).WithDetail(
				&gitalypb.UserSquashError{
					// Note: this is actually a merge conflict, but we've kept
					// the old "rebase" name for compatibility reasons.
					Error: &gitalypb.UserSquashError_RebaseConflict{
						RebaseConflict: &gitalypb.MergeConflictError{
							ConflictingFiles: conflictingFiles,
							ConflictingCommitIds: []string{
								startCommit.String(),
								endCommit.String(),
							},
						},
					},
				},
			)
		}

		var mergeConflictErr *localrepo.MergeTreeConflictError
		if errors.As(err, &mergeConflictErr) {
			conflictingFiles := make([][]byte, 0, len(mergeConflictErr.ConflictingFileInfo))
			for _, conflictingFile := range mergeConflictErr.ConflictingFileInfo {
				conflictingFiles = append(conflictingFiles, []byte(conflictingFile.FileName))
			}

			return "", structerr.NewFailedPrecondition("squashing commits: %w", err).WithDetail(
				&gitalypb.UserSquashError{
					// Note: this is actually a merge conflict, but we've kept
					// the old "rebase" name for compatibility reasons.
					Error: &gitalypb.UserSquashError_RebaseConflict{
						RebaseConflict: &gitalypb.MergeConflictError{
							ConflictingFiles: conflictingFiles,
							ConflictingCommitIds: []string{
								startCommit.String(),
								endCommit.String(),
							},
						},
					},
				},
			)
		}
	}

	// The RPC is badly designed in that it never updates any references, but only creates the
	// objects and writes them to disk. We still use a quarantine directory to stage the new
	// objects, vote on them and migrate them into the main directory if quorum was reached so
	// that we don't pollute the object directory with objects we don't want to have in the
	// first place.
	if err := transaction.VoteOnContext(
		ctx,
		s.txManager,
		voting.VoteFromData([]byte(commitID)),
		voting.Prepared,
	); err != nil {
		return "", structerr.NewAborted("preparatory vote on squashed commit: %w", err)
	}

	if err := quarantineDir.Migrate(); err != nil {
		return "", structerr.NewInternal("migrating quarantine directory: %w", err)
	}

	if err := transaction.VoteOnContext(
		ctx,
		s.txManager,
		voting.VoteFromData([]byte(commitID)),
		voting.Committed,
	); err != nil {
		return "", structerr.NewAborted("committing vote on squashed commit: %w", err)
	}

	return commitID, nil
}
