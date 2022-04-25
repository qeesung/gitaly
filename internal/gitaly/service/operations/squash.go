package operations

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v14/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
)

const (
	gitlabWorktreesSubDir = "gitlab-worktree"
)

// UserSquash collapses a range of commits identified via a start and end revision into a single
// commit whose single parent is the start revision.
func (s *Server) UserSquash(ctx context.Context, req *gitalypb.UserSquashRequest) (*gitalypb.UserSquashResponse, error) {
	if err := validateUserSquashRequest(req); err != nil {
		return nil, helper.ErrInvalidArgumentf("UserSquash: %v", err)
	}

	sha, err := s.userSquash(ctx, req)
	if err != nil {
		return nil, helper.ErrInternal(err)
	}

	return &gitalypb.UserSquashResponse{SquashSha: sha}, nil
}

func validateUserSquashRequest(req *gitalypb.UserSquashRequest) error {
	if req.GetRepository() == nil {
		return errors.New("empty Repository")
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
		return errors.New("empty auithor email")
	}

	return nil
}

func (s *Server) userSquash(ctx context.Context, req *gitalypb.UserSquashRequest) (string, error) {
	// All new objects are staged into a quarantine directory first so that we can do
	// transactional voting before we commit data to disk.
	quarantineDir, quarantineRepo, err := s.quarantinedRepo(ctx, req.GetRepository())
	if err != nil {
		return "", helper.ErrInternalf("creating quarantine: %w", err)
	}

	quarantineRepoPath, err := quarantineRepo.Path()
	if err != nil {
		return "", helper.ErrInternalf("getting quarantine path: %w", err)
	}

	// We need to retrieve the start commit such that we can create the new commit with
	// all parents of the start commit.
	startCommit, err := quarantineRepo.ResolveRevision(ctx, git.Revision(req.GetStartSha()+"^{commit}"))
	if err != nil {
		detailedErr, err := helper.ErrWithDetails(
			helper.ErrInvalidArgumentf("resolving start revision: %w", err),
			&gitalypb.UserSquashError{
				Error: &gitalypb.UserSquashError_ResolveRevision{
					ResolveRevision: &gitalypb.ResolveRevisionError{
						Revision: []byte(req.GetStartSha()),
					},
				},
			},
		)
		if err != nil {
			return "", helper.ErrInternalf("error details: %w", err)
		}

		return "", detailedErr
	}

	// And we need to take the tree of the end commit. This tree already is the result
	endCommit, err := quarantineRepo.ResolveRevision(ctx, git.Revision(req.GetEndSha()+"^{commit}"))
	if err != nil {
		detailedErr, err := helper.ErrWithDetails(
			helper.ErrInvalidArgumentf("resolving end revision: %w", err),
			&gitalypb.UserSquashError{
				Error: &gitalypb.UserSquashError_ResolveRevision{
					ResolveRevision: &gitalypb.ResolveRevisionError{
						Revision: []byte(req.GetEndSha()),
					},
				},
			},
		)
		if err != nil {
			return "", helper.ErrInternalf("error details: %w", err)
		}

		return "", detailedErr
	}

	commitDate, err := dateFromProto(req)
	if err != nil {
		return "", helper.ErrInvalidArgument(err)
	}

	// We're now rebasing the end commit on top of the start commit. The resulting tree
	// is then going to be the tree of the squashed commit.
	rebasedCommitID, err := s.git2goExecutor.Rebase(ctx, quarantineRepo, git2go.RebaseCommand{
		Repository: quarantineRepoPath,
		Committer: git2go.NewSignature(
			string(req.GetUser().Name), string(req.GetUser().Email), commitDate,
		),
		CommitID:         endCommit,
		UpstreamCommitID: startCommit,
		SkipEmptyCommits: true,
	})
	if err != nil {
		var conflictErr git2go.ConflictingFilesError

		if errors.As(err, &conflictErr) {
			conflictingFiles := make([][]byte, 0, len(conflictErr.ConflictingFiles))
			for _, conflictingFile := range conflictErr.ConflictingFiles {
				conflictingFiles = append(conflictingFiles, []byte(conflictingFile))
			}

			detailedErr, err := helper.ErrWithDetails(
				helper.ErrFailedPreconditionf("rebasing commits: %w", err),
				&gitalypb.UserSquashError{
					Error: &gitalypb.UserSquashError_RebaseConflict{
						RebaseConflict: &gitalypb.MergeConflictError{
							ConflictingFiles: conflictingFiles,
						},
					},
				},
			)
			if err != nil {
				return "", helper.ErrInternalf("error details: %w", err)
			}

			return "", detailedErr
		}

		return "", helper.ErrInternalf("rebasing commits: %w", err)
	}

	treeID, err := quarantineRepo.ResolveRevision(ctx, rebasedCommitID.Revision()+"^{tree}")
	if err != nil {
		return "", fmt.Errorf("cannot resolve rebased tree: %w", err)
	}

	commitEnv := []string{
		"GIT_COMMITTER_NAME=" + string(req.GetUser().Name),
		"GIT_COMMITTER_EMAIL=" + string(req.GetUser().Email),
		fmt.Sprintf("GIT_COMMITTER_DATE=%d %s", commitDate.Unix(), commitDate.Format("-0700")),
		"GIT_AUTHOR_NAME=" + string(req.GetAuthor().Name),
		"GIT_AUTHOR_EMAIL=" + string(req.GetAuthor().Email),
		fmt.Sprintf("GIT_AUTHOR_DATE=%d %s", commitDate.Unix(), commitDate.Format("-0700")),
	}

	flags := []git.Option{
		git.ValueFlag{
			Name:  "-m",
			Value: string(req.GetCommitMessage()),
		},
		git.ValueFlag{
			Name:  "-p",
			Value: startCommit.String(),
		},
	}

	var stdout, stderr bytes.Buffer
	if err := quarantineRepo.ExecAndWait(ctx, git.SubCmd{
		Name:  "commit-tree",
		Flags: flags,
		Args: []string{
			treeID.String(),
		},
	}, git.WithStdout(&stdout), git.WithStderr(&stderr), git.WithEnv(commitEnv...)); err != nil {
		return "", helper.ErrInternalf("creating squashed commit: %w", err)
	}

	commitID := text.ChompBytes(stdout.Bytes())

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
		return "", helper.ErrAbortedf("preparatory vote on squashed commit: %w", err)
	}

	if err := quarantineDir.Migrate(); err != nil {
		return "", helper.ErrInternalf("migrating quarantine directory: %w", err)
	}

	if err := transaction.VoteOnContext(
		ctx,
		s.txManager,
		voting.VoteFromData([]byte(commitID)),
		voting.Committed,
	); err != nil {
		return "", helper.ErrAbortedf("committing vote on squashed commit: %w", err)
	}

	return commitID, nil
}
