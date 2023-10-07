package localrepo

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type rebaseConfig struct {
	committer *git.Signature
}

// RebaseOption is a function that sets a config in rebaseConfig.
type RebaseOption func(*rebaseConfig)

// RebaseWithCommitter provides a signature to be used as the committer for
// generated commits during rebase.
func RebaseWithCommitter(committer git.Signature) RebaseOption {
	return func(options *rebaseConfig) {
		options.committer = &committer
	}
}

// Rebase implements a basic support for rebase using git-merge-tree(1), it
// follows what git-rebase(1) does but omits the abundant options.
// Our rebase roughly follows the core logic of git-rebase itself. Specifically,
// we check if fast-forward is possible firstly, if not, we
// 1. generate a *pick* only todo_list using git-rev-list
// 2. process the todo_list using git-merge-tree based cherry-pick
func (repo *Repo) Rebase(ctx context.Context, upstream, branch string, options ...RebaseOption) (git.ObjectID, error) {
	var config rebaseConfig

	for _, option := range options {
		option(&config)
	}

	upstreamOID, err := repo.ResolveRevision(ctx, git.Revision(upstream+"^{commit}"))
	if err != nil {
		if errors.Is(err, git.ErrReferenceNotFound) {
			return "", structerr.NewInvalidArgument("resolving upstream commit: %w", err).WithMetadata("revision", upstream)
		}
		return "", fmt.Errorf("resolving upstream commit: %w", err).WithMetadata("revision", upstream)
	}

	branchOID, err := repo.ResolveRevision(ctx, git.Revision(branch+"^{commit}"))
	if err != nil {
		if errors.Is(err,git.ErrReferenceNotFound) {
			return "", structerr.NewInvalidArgument("resolving branch commit: %w", err).WithMetadata("revision", branch)
		}
		return "", fmt.Errorf("resolving branch commit: %w", err).WithMetadata("revision", branch)
	}

	var stdout bytes.Buffer
	if err := repo.ExecAndWait(ctx,
		git.Command{
			Name: "merge-base",
			Args: []string{upstreamOID.String(), branchOID.String()},
		},
		git.WithStdout(&stdout),
	); err != nil {
		return "", structerr.NewInternal("get merge-base: %w", err)
	}

	// fast-forward if possible
	mergeBase := text.ChompBytes(stdout.Bytes())
	if mergeBase == upstreamOID.String() {
		return branchOID, nil
	}
	if mergeBase == branchOID.String() {
		return upstreamOID, nil
	}

	newOID, err := repo.rebaseUsingMergeTree(ctx, config, upstreamOID, branchOID)
	if err != nil {
		return "", structerr.NewInternal("rebase using merge-tree: %w", err)
	}

	return newOID, nil
}

func (repo *Repo) rebaseUsingMergeTree(ctx context.Context, cfg rebaseConfig, upstreamOID, branchOID git.ObjectID) (git.ObjectID, error) {
	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return "", structerr.NewInternal("getting object hash %w", err)
	}

	// Flags of git-rev-list to get the pick-only todo_list for a rebase.
	// Currently we drop clean cherry-picks and merge commits, which is
	// also what git2go does.
	// The flags are inferred from https://github.com/git/git/blob/v2.41.0/sequencer.c#L5704-L5714
	flags := []git.Option{
		git.Flag{Name: "--cherry-pick"},
		git.Flag{Name: "--right-only"},
		git.Flag{Name: "--no-merges"},
		git.Flag{Name: "--topo-order"},
		git.Flag{Name: "--reverse"},
	}

	var stderr bytes.Buffer
	cmd, err := repo.Exec(ctx, git.Command{
		Name:  "rev-list",
		Flags: flags,
		// The notation "<upstream>...<branch>" is used to calculate the symmetric
		// difference between upstream and branch. It will return the commits that
		// are reachable exclusively from either side but not both. Combined with
		// the provided --right-only flag, the result should be only commits which
		// exist on the branch that is to be rebased.
		Args: []string{fmt.Sprintf("%s...%s", upstreamOID, branchOID)},
	}, git.WithStderr(&stderr), git.WithSetupStdout())
	if err != nil {
		return "", structerr.NewInternal("start git rev-list: %w", err)
	}

	var todoList []string
	scanner := bufio.NewScanner(cmd)
	for scanner.Scan() {
		todoList = append(todoList, strings.TrimSpace(scanner.Text()))
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scanning rev-list output: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return "", structerr.NewInternal("git rev-list: %w", err).WithMetadata("stderr", stderr.String())
	}

	upstreamCommit, err := repo.ReadCommit(ctx, git.Revision(upstreamOID))
	if err != nil {
		return "", fmt.Errorf("reading upstream commit: %w", err)
	}

	oursCommitOID := upstreamOID
	oursTreeOID := git.ObjectID(upstreamCommit.TreeId)
	for _, todoItem := range todoList {
		theirsCommit, err := repo.ReadCommit(ctx, git.Revision(todoItem))
		if err != nil {
			return "", fmt.Errorf("reading todo list commit: %w", err)
		}

		opts := []MergeTreeOption{WithAllowUnrelatedHistories()}
		if len(theirsCommit.ParentIds) > 0 {
			opts = append(opts, WithMergeBase(git.Revision(theirsCommit.ParentIds[0])))
		}

		newTreeOID, err := repo.MergeTree(ctx, oursCommitOID.String(), theirsCommit.Id, opts...)
		if err != nil {
			var conflictErr *MergeTreeConflictError
			if errors.As(err, &conflictErr) {
				return newTreeOID, &RebaseConflictError{
					Commit:        theirsCommit.Id,
					ConflictError: conflictErr,
				}
			}
			return "", fmt.Errorf("merging todo list commit: %w", err)
		}

		// When no tree changes detected, we need to further check
		// 1. if the commit itself introduces no changes, pick it anyway.
		// 2. if the commit is not empty to start and is not clean cherry-picks of any
		//    upstream commit, but become empty after rebasing, we just ignore it.
		// Refer to https://git-scm.com/docs/git-rebase#Documentation/git-rebase.txt---emptydropkeepask
		if newTreeOID == oursTreeOID {
			if len(theirsCommit.ParentIds) == 0 {
				if theirsCommit.TreeId != objectHash.EmptyTreeOID.String() {
					continue
				}
			} else {
				theirsParentCommit, err := repo.ReadCommit(ctx, git.Revision(theirsCommit.ParentIds[0]))
				if err != nil {
					return "", fmt.Errorf("reading parent commit: %w", err)
				}

				if theirsCommit.TreeId != theirsParentCommit.TreeId {
					continue
				}
			}
		}

		author := getSignatureFromCommitAuthor(theirsCommit.GetAuthor())
		committer := cfg.committer
		if committer == nil {
			committer = getSignatureFromCommitAuthor(theirsCommit.GetCommitter())
		}

		newCommitOID, err := repo.WriteCommit(ctx, WriteCommitConfig{
			Parents:        []git.ObjectID{oursCommitOID},
			AuthorName:     author.Name,
			AuthorEmail:    author.Email,
			AuthorDate:     author.When,
			CommitterName:  committer.Name,
			CommitterEmail: committer.Email,
			CommitterDate:  committer.When,
			Message:        string(theirsCommit.GetBody()),
			TreeID:         newTreeOID,
		})
		if err != nil {
			return "", fmt.Errorf("write commit: %w", err)
		}
		oursCommitOID = newCommitOID
		oursTreeOID = newTreeOID
	}

	return oursCommitOID, nil
}

// RebaseConflictError encapsulates any conflicting file info and messages that occur
// when a git-merge-tree based rebase fails.
type RebaseConflictError struct {
	Commit        string
	ConflictError *MergeTreeConflictError
}

// Error returns the error string for a rebase conflict error. It is especially designed
// to keep compatible with git2go rebase.
func (c *RebaseConflictError) Error() string {
	return fmt.Sprintf("rebase: commit %q: there are conflicting files", c.Commit)
}

// getSignatureFromCommitAuthor gets a Signature from gitalypb.CommitAuthor. It translates
// CommitAuthor.Timezone into time offsets which can applied to time.Time so that we won't
// lose any timezone info.
func getSignatureFromCommitAuthor(author *gitalypb.CommitAuthor) (signature *git.Signature) {
	signature = &git.Signature{
		Name:  string(author.GetName()),
		Email: string(author.GetEmail()),
		When:  time.Now().In(time.UTC),
	}

	if timestamp := author.GetDate(); timestamp != nil {
		signature.When = timestamp.AsTime().In(time.UTC)
	}

	// care about timezone
	timezone := author.GetTimezone()
	if len(timezone) != 5 {
		return
	}

	loc, err := time.Parse("-0700", string(timezone))
	if err != nil {
		return
	}

	signature.When = signature.When.In(loc.Location())

	return
}
