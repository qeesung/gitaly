package operations

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"path/filepath"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

var errNoDefaultBranch = errors.New("no default branch")

type gitError struct {
	// ErrMsg error message from 'git' executable if any.
	ErrMsg string
	// Err is an error that happened during rebase process.
	Err error
}

func (er gitError) Error() string {
	return er.ErrMsg + ": " + er.Err.Error()
}

// UserApplyPatch applies patches to a given branch.
func (s *Server) UserApplyPatch(stream gitalypb.OperationService_UserApplyPatchServer) error {
	firstRequest, err := stream.Recv()
	if err != nil {
		return err
	}

	header := firstRequest.GetHeader()
	if header == nil {
		return structerr.NewInvalidArgument("empty UserApplyPatch_Header")
	}

	if err := validateUserApplyPatchHeader(stream.Context(), s.locator, header); err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	if err := s.userApplyPatch(stream.Context(), header, stream); err != nil {
		return structerr.NewInternal("%w", err)
	}

	return nil
}

func (s *Server) userApplyPatch(ctx context.Context, header *gitalypb.UserApplyPatchRequest_Header, stream gitalypb.OperationService_UserApplyPatchServer) error {
	path, err := s.locator.GetRepoPath(ctx, header.Repository)
	if err != nil {
		return err
	}

	branchCreated := false
	targetBranch := git.NewReferenceNameFromBranchName(string(header.TargetBranch))

	repo := s.localrepo(header.Repository)

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("detecting object hash: %w", err)
	}

	parentCommitID, err := repo.ResolveRevision(ctx, targetBranch.Revision()+"^{commit}")
	if err != nil {
		if !errors.Is(err, git.ErrReferenceNotFound) {
			return fmt.Errorf("resolve target branch: %w", err)
		}

		defaultBranch, err := repo.HeadReference(ctx)
		if err != nil {
			return fmt.Errorf("default branch name: %w", err)
		}

		parentCommitID, err = repo.ResolveRevision(ctx, defaultBranch.Revision()+"^{commit}")
		if errors.Is(err, git.ErrReferenceNotFound) {
			return errNoDefaultBranch
		} else if err != nil {
			return fmt.Errorf("resolve default branch commit: %w", err)
		}

		branchCreated = true
	}

	committerSignature, err := git.SignatureFromRequest(header)
	if err != nil {
		return structerr.NewInvalidArgument("%w", err)
	}

	worktreePath := newWorktreePath(path, "am-")
	if err := s.addWorktree(ctx, repo, worktreePath, parentCommitID.String()); err != nil {
		return fmt.Errorf("add worktree: %w", err)
	}

	defer func() {
		ctx, cancel := context.WithTimeout(helper.SuppressCancellation(ctx), 30*time.Second)
		defer cancel()

		worktreeName := filepath.Base(worktreePath)
		if err := s.removeWorktree(ctx, header.Repository, worktreeName); err != nil {
			s.logger.WithField("worktree_name", worktreeName).WithError(err).ErrorContext(ctx, "failed to remove worktree")
		}
	}()

	var stdout, stderr bytes.Buffer
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name: "am",
			Flags: []git.Option{
				git.Flag{Name: "--quiet"},
				git.Flag{Name: "--3way"},
			},
		},
		git.WithEnv(
			"GIT_COMMITTER_NAME="+committerSignature.Name,
			"GIT_COMMITTER_EMAIL="+committerSignature.Email,
			"GIT_COMMITTER_DATE="+git.FormatTime(committerSignature.When),
		),
		git.WithStdin(streamio.NewReader(func() ([]byte, error) {
			req, err := stream.Recv()
			return req.GetPatches(), err
		})),
		git.WithStdout(&stdout),
		git.WithStderr(&stderr),
		git.WithRefTxHook(header.Repository),
		git.WithWorktree(worktreePath),
	); err != nil {
		// The Ruby implementation doesn't include stderr in errors, which makes
		// it difficult to determine the cause of an error. This special cases the
		// user facing patching error which is returned usually to maintain test
		// compatibility but returns the error and stderr otherwise. Once the Ruby
		// implementation is removed, this should probably be dropped.
		if bytes.HasPrefix(stdout.Bytes(), []byte("Patch failed at")) {
			return structerr.NewFailedPrecondition(stdout.String())
		}

		return fmt.Errorf("apply patch: %w, stderr: %q", err, &stderr)
	}

	var revParseStdout, revParseStderr bytes.Buffer
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name: "rev-parse",
			Flags: []git.Option{
				git.Flag{Name: "--quiet"},
				git.Flag{Name: "--verify"},
			},
			Args: []string{"HEAD^{commit}"},
		},
		git.WithStdout(&revParseStdout),
		git.WithStderr(&revParseStderr),
		git.WithWorktree(worktreePath),
	); err != nil {
		return fmt.Errorf("get patched commit: %w", gitError{ErrMsg: revParseStderr.String(), Err: err})
	}

	patchedCommit, err := objectHash.FromHex(text.ChompBytes(revParseStdout.Bytes()))
	if err != nil {
		return fmt.Errorf("parse patched commit oid: %w", err)
	}

	currentCommit := parentCommitID
	if branchCreated {
		currentCommit = objectHash.ZeroOID
	}

	// If the client provides an expected old object ID, we should use that to prevent any race
	// conditions wherein the ref was concurrently updated by different processes.
	if expectedOldOID := header.GetExpectedOldOid(); expectedOldOID != "" {
		objectHash, err := repo.ObjectHash(ctx)
		if err != nil {
			return fmt.Errorf("detecting object hash: %w", err)
		}

		currentCommit, err = objectHash.FromHex(expectedOldOID)
		if err != nil {
			return fmt.Errorf("expected old object id not expected SHA format: %w", err)
		}

		currentCommit, err = repo.ResolveRevision(
			ctx, git.Revision(fmt.Sprintf("%s^{object}", currentCommit)),
		)
		if err != nil {
			return fmt.Errorf("expected old object cannot be resolved: %w", err)
		}
	}

	if err := s.updateReferenceWithHooks(ctx, header.Repository, header.User, nil, targetBranch, patchedCommit, currentCommit); err != nil {
		return fmt.Errorf("update reference: %w", err)
	}

	if err := stream.SendAndClose(&gitalypb.UserApplyPatchResponse{
		BranchUpdate: &gitalypb.OperationBranchUpdate{
			CommitId:      patchedCommit.String(),
			BranchCreated: branchCreated,
		},
	}); err != nil {
		return fmt.Errorf("send and close: %w", err)
	}

	return nil
}

func validateUserApplyPatchHeader(ctx context.Context, locator storage.Locator, header *gitalypb.UserApplyPatchRequest_Header) error {
	if err := locator.ValidateRepository(ctx, header.GetRepository()); err != nil {
		return err
	}

	if header.GetUser() == nil {
		return errors.New("missing User")
	}

	if len(header.GetTargetBranch()) == 0 {
		return errors.New("missing Branch")
	}

	return nil
}

func (s *Server) addWorktree(ctx context.Context, repo *localrepo.Repo, worktreePath string, committish string) error {
	args := []string{worktreePath}
	flags := []git.Option{git.Flag{Name: "--detach"}}
	if committish != "" {
		args = append(args, committish)
	} else {
		flags = append(flags, git.Flag{Name: "--no-checkout"})
	}

	var stderr bytes.Buffer
	if err := repo.ExecAndWait(ctx, git.Command{
		Name:   "worktree",
		Action: "add",
		Flags:  flags,
		Args:   args,
	}, git.WithStderr(&stderr), git.WithRefTxHook(repo)); err != nil {
		return fmt.Errorf("adding worktree: %w", gitError{ErrMsg: stderr.String(), Err: err})
	}

	return nil
}

func (s *Server) removeWorktree(ctx context.Context, repo *gitalypb.Repository, worktreeName string) error {
	cmd, err := s.gitCmdFactory.New(ctx, repo,
		git.Command{
			Name:   "worktree",
			Action: "remove",
			Flags:  []git.Option{git.Flag{Name: "--force"}},
			Args:   []string{worktreeName},
		},
		git.WithRefTxHook(repo),
	)
	if err != nil {
		return fmt.Errorf("creation of 'worktree remove': %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("wait for 'worktree remove': %w", err)
	}

	return nil
}

func newWorktreePath(repoPath, prefix string) string {
	chars := []byte("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	rand.Shuffle(len(chars), func(i, j int) { chars[i], chars[j] = chars[j], chars[i] })
	return filepath.Join(repoPath, housekeeping.GitlabWorktreePrefix, prefix+string(chars[:32]))
}
