package git2go

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestExecutor_Apply(t *testing.T) {
	pbRepo, repoPath, clean := testhelper.InitBareRepo(t)
	defer clean()

	repo := localrepo.New(pbRepo, config.Config)
	executor := New(filepath.Join(config.Config.BinDir, "gitaly-git2go"), config.Config.Git.BinPath)

	ctx, cancel := testhelper.Context()
	defer cancel()

	oidBase, err := repo.WriteBlob(ctx, "file", strings.NewReader("base"))
	require.NoError(t, err)

	oidA, err := repo.WriteBlob(ctx, "file", strings.NewReader("a"))
	require.NoError(t, err)

	oidB, err := repo.WriteBlob(ctx, "file", strings.NewReader("b"))
	require.NoError(t, err)

	authorTime := time.Date(2020, 0, 0, 0, 0, 0, 0, time.UTC)
	committerTime := authorTime.Add(time.Hour)
	author := NewSignature("Test Author", "test.author@example.com", authorTime)
	committer := NewSignature("Test Committer", "test.committer@example.com", committerTime)

	parentCommitSHA, err := executor.Commit(ctx, CommitParams{
		Repository: repoPath,
		Author:     author,
		Committer:  committer,
		Message:    "base commit",
		Actions:    []Action{CreateFile{Path: "file", OID: oidBase}},
	})
	require.NoError(t, err)

	noCommonAncestor, err := executor.Commit(ctx, CommitParams{
		Repository: repoPath,
		Author:     author,
		Committer:  committer,
		Message:    "commit with ab",
		Actions:    []Action{CreateFile{Path: "file", OID: oidA}},
	})
	require.NoError(t, err)

	updateToA, err := executor.Commit(ctx, CommitParams{
		Repository: repoPath,
		Author:     author,
		Committer:  committer,
		Message:    "commit with a",
		Parent:     parentCommitSHA,
		Actions:    []Action{UpdateFile{Path: "file", OID: oidA}},
	})
	require.NoError(t, err)

	updateToB, err := executor.Commit(ctx, CommitParams{
		Repository: repoPath,
		Author:     author,
		Committer:  committer,
		Message:    "commit to b",
		Parent:     parentCommitSHA,
		Actions:    []Action{UpdateFile{Path: "file", OID: oidB}},
	})
	require.NoError(t, err)

	updateFromAToB, err := executor.Commit(ctx, CommitParams{
		Repository: repoPath,
		Author:     author,
		Committer:  committer,
		Message:    "commit a -> b",
		Parent:     updateToA,
		Actions:    []Action{UpdateFile{Path: "file", OID: oidB}},
	})
	require.NoError(t, err)

	otherFile, err := executor.Commit(ctx, CommitParams{
		Repository: repoPath,
		Author:     author,
		Committer:  committer,
		Message:    "commit with other-file",
		Actions:    []Action{CreateFile{Path: "other-file", OID: oidA}},
	})
	require.NoError(t, err)

	diffBetween := func(t testing.TB, fromCommit, toCommit string) []byte {
		t.Helper()
		return testhelper.MustRunCommand(t, nil,
			"git", "-C", repoPath, "format-patch", "--stdout", fromCommit+".."+toCommit)
	}

	for _, tc := range []struct {
		desc         string
		patches      []Patch
		parentCommit string
		message      string
		tree         []testhelper.TreeEntry
		error        error
	}{
		{
			desc: "patch applies cleanly",
			patches: []Patch{
				{
					Author:  author,
					Subject: "patch 1 subject",
					Message: "patch 1 message",
					Diff:    diffBetween(t, parentCommitSHA, updateToA),
				},
			},
			parentCommit: parentCommitSHA,
			message:      "patch 1 subject\n\npatch 1 message",
			tree: []testhelper.TreeEntry{
				{Path: "file", Mode: "100644", Content: "a"},
			},
		},
		{
			desc: "multiple patches apply cleanly",
			patches: []Patch{
				{
					Author:  author,
					Message: "commit with a",
					Diff:    diffBetween(t, parentCommitSHA, updateToA),
				},
				{
					Author:  author,
					Subject: "patch 2 subject",
					Diff:    diffBetween(t, updateToA, updateFromAToB),
				},
			},
			message:      "patch 2 subject\n",
			parentCommit: updateToA,
			tree: []testhelper.TreeEntry{
				{Path: "file", Mode: "100644", Content: "b"},
			},
		},
		{
			desc: "three way merged",
			patches: []Patch{
				{
					Author:  author,
					Message: "patch 1 message\n",
					Diff:    diffBetween(t, parentCommitSHA, otherFile),
				},
			},
			parentCommit: parentCommitSHA,
			message:      "patch 1 message\n",
			tree: []testhelper.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base"},
				{Path: "other-file", Mode: "100644", Content: "a"},
			},
		},
		{
			desc: "no common ancestor",
			patches: []Patch{
				{
					Author:  author,
					Subject: "patch 1 subject",
					Message: "patch 1 message",
					Diff:    diffBetween(t, parentCommitSHA, noCommonAncestor),
				},
			},
			parentCommit: parentCommitSHA,
			error:        ApplyConflictError{PatchNumber: 1, CommitSubject: "patch 1 subject"},
		},
		{
			desc: "merge conflict",
			patches: []Patch{
				{
					Author:  author,
					Subject: "patch 1 subject",
					Message: "patch 1 message",
					Diff:    diffBetween(t, parentCommitSHA, updateToA),
				},
				{
					Author:  author,
					Subject: "patch 2 subject",
					Message: "patch 2 message",
					Diff:    diffBetween(t, parentCommitSHA, updateToB),
				},
			},
			error: ApplyConflictError{PatchNumber: 2, CommitSubject: "patch 2 subject"},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			commitID, err := executor.Apply(ctx, ApplyParams{
				Repository:   repoPath,
				Committer:    committer,
				ParentCommit: parentCommitSHA,
				Patches:      NewSlicePatchIterator(tc.patches),
			})
			if tc.error != nil {
				require.True(t, errors.Is(err, tc.error), err)
				return
			}

			require.NoError(t, err)

			require.Equal(t, CommitAssertion{
				Parent:    tc.parentCommit,
				Author:    author,
				Committer: committer,
				Message:   tc.message,
			}, GetCommitAssertion(ctx, t, repo, commitID))
			testhelper.RequireTree(t, repoPath, commitID, tc.tree)
		})
	}
}
