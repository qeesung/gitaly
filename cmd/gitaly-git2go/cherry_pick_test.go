//go:build static && system_libgit2 && !gitaly_test_sha256

package main

import (
	"testing"
	"time"

	git "github.com/libgit2/git2go/v34"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/cmd/gitaly-git2go/git2goutil"
	gitalygit "gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestCherryPick_validation(t *testing.T) {
	cfg, repo, repoPath := testcfg.BuildWithRepo(t)
	testcfg.BuildGitalyGit2Go(t, cfg)
	executor := buildExecutor(t, cfg)

	testcases := []struct {
		desc        string
		request     git2go.CherryPickCommand
		expectedErr string
	}{
		{
			desc:        "no arguments",
			expectedErr: "cherry-pick: missing repository",
		},
		{
			desc:        "missing repository",
			request:     git2go.CherryPickCommand{CommitterName: "Foo", CommitterMail: "foo@example.com", CommitterDate: time.Now(), Message: "Foo", Ours: "HEAD", Commit: "HEAD"},
			expectedErr: "cherry-pick: missing repository",
		},
		{
			desc:        "missing committer name",
			request:     git2go.CherryPickCommand{Repository: repoPath, CommitterMail: "foo@example.com", CommitterDate: time.Now(), Message: "Foo", Ours: "HEAD", Commit: "HEAD"},
			expectedErr: "cherry-pick: missing committer name",
		},
		{
			desc:        "missing committer mail",
			request:     git2go.CherryPickCommand{Repository: repoPath, CommitterName: "Foo", CommitterDate: time.Now(), Message: "Foo", Ours: "HEAD", Commit: "HEAD"},
			expectedErr: "cherry-pick: missing committer mail",
		},
		{
			desc:        "missing committer date",
			request:     git2go.CherryPickCommand{Repository: repoPath, CommitterName: "Foo", CommitterMail: "foo@example.com", Message: "Foo", Ours: "HEAD", Commit: "HEAD"},
			expectedErr: "cherry-pick: missing committer date",
		},
		{
			desc:        "missing message",
			request:     git2go.CherryPickCommand{Repository: repoPath, CommitterName: "Foo", CommitterMail: "foo@example.com", CommitterDate: time.Now(), Ours: "HEAD", Commit: "HEAD"},
			expectedErr: "cherry-pick: missing message",
		},
		{
			desc:        "missing ours",
			request:     git2go.CherryPickCommand{Repository: repoPath, CommitterName: "Foo", CommitterMail: "foo@example.com", CommitterDate: time.Now(), Message: "Foo", Commit: "HEAD"},
			expectedErr: "cherry-pick: missing ours",
		},
		{
			desc:        "missing commit",
			request:     git2go.CherryPickCommand{Repository: repoPath, CommitterName: "Foo", CommitterMail: "foo@example.com", CommitterDate: time.Now(), Message: "Foo", Ours: "HEAD"},
			expectedErr: "cherry-pick: missing commit",
		},
	}
	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			_, err := executor.CherryPick(ctx, repo, tc.request)
			require.EqualError(t, err, tc.expectedErr)
		})
	}
}

func TestCherryPick(t *testing.T) {
	testcases := []struct {
		desc             string
		base             []gitalygit.TreeEntry
		ours             []gitalygit.TreeEntry
		commit           []gitalygit.TreeEntry
		expected         map[string]string
		expectedCommitID string
		expectedErrAs    interface{}
		expectedErrMsg   string
	}{
		{
			desc: "trivial cherry-pick succeeds",
			base: []gitalygit.TreeEntry{
				{Path: "file", Content: "foo", Mode: "100644"},
			},
			ours: []gitalygit.TreeEntry{
				{Path: "file", Content: "foo", Mode: "100644"},
			},
			commit: []gitalygit.TreeEntry{
				{Path: "file", Content: "foobar", Mode: "100644"},
			},
			expected: map[string]string{
				"file": "foobar",
			},
			expectedCommitID: "a54ea83118c363c34cc605a6e61fd7abc4795cc4",
		},
		{
			desc: "conflicting cherry-pick fails",
			base: []gitalygit.TreeEntry{
				{Path: "file", Content: "foo", Mode: "100644"},
			},
			ours: []gitalygit.TreeEntry{
				{Path: "file", Content: "fooqux", Mode: "100644"},
			},
			commit: []gitalygit.TreeEntry{
				{Path: "file", Content: "foobar", Mode: "100644"},
			},
			expectedErrAs:  &git2go.ConflictingFilesError{},
			expectedErrMsg: "cherry-pick: there are conflicting files",
		},
		{
			desc: "empty cherry-pick fails",
			base: []gitalygit.TreeEntry{
				{Path: "file", Content: "foo", Mode: "100644"},
			},
			ours: []gitalygit.TreeEntry{
				{Path: "file", Content: "fooqux", Mode: "100644"},
			},
			commit: []gitalygit.TreeEntry{
				{Path: "file", Content: "fooqux", Mode: "100644"},
			},
			expectedErrAs:  &git2go.EmptyError{},
			expectedErrMsg: "cherry-pick: could not apply because the result was empty",
		},
		{
			desc:           "fails on nonexistent ours commit",
			expectedErrAs:  &git2go.CommitNotFoundError{},
			expectedErrMsg: "cherry-pick: ours commit lookup: commit not found: \"nonexistent\"",
		},
		{
			desc: "fails on nonexistent cherry-pick commit",
			ours: []gitalygit.TreeEntry{
				{Path: "file", Content: "fooqux", Mode: "100644"},
			},
			expectedErrAs:  &git2go.CommitNotFoundError{},
			expectedErrMsg: "cherry-pick: commit lookup: commit not found: \"nonexistent\"",
		},
	}
	for _, tc := range testcases {
		cfg, repo, repoPath := testcfg.BuildWithRepo(t)
		testcfg.BuildGitalyGit2Go(t, cfg)
		executor := buildExecutor(t, cfg)

		base := gitalygit.WriteTestCommit(t, cfg, repoPath, gitalygit.WithTreeEntries(tc.base...))

		ours, commit := "nonexistent", "nonexistent"
		if len(tc.ours) > 0 {
			ours = gitalygit.WriteTestCommit(t, cfg, repoPath, gitalygit.WithParents(base), gitalygit.WithTreeEntries(tc.ours...)).String()
		}
		if len(tc.commit) > 0 {
			commit = gitalygit.WriteTestCommit(t, cfg, repoPath, gitalygit.WithParents(base), gitalygit.WithTreeEntries(tc.commit...)).String()
		}

		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			committer := git.Signature{
				Name:  "Baz",
				Email: "baz@example.com",
				When:  time.Date(2021, 1, 17, 14, 45, 51, 0, time.FixedZone("", +2*60*60)),
			}

			response, err := executor.CherryPick(ctx, repo, git2go.CherryPickCommand{
				Repository:    repoPath,
				CommitterName: committer.Name,
				CommitterMail: committer.Email,
				CommitterDate: committer.When,
				Message:       "Foo",
				Ours:          ours,
				Commit:        commit,
			})

			if tc.expectedErrMsg != "" {
				require.EqualError(t, err, tc.expectedErrMsg)

				if tc.expectedErrAs != nil {
					require.ErrorAs(t, err, tc.expectedErrAs)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expectedCommitID, response.String())

			repo, err := git2goutil.OpenRepository(repoPath)
			require.NoError(t, err)
			defer repo.Free()

			commitOid, err := git.NewOid(response.String())
			require.NoError(t, err)

			commit, err := repo.LookupCommit(commitOid)
			require.NoError(t, err)
			require.Equal(t, &DefaultAuthor, commit.Author())
			require.Equal(t, &committer, commit.Committer())

			tree, err := commit.Tree()
			require.NoError(t, err)
			require.Len(t, tc.expected, int(tree.EntryCount()))

			for name, contents := range tc.expected {
				entry := tree.EntryByName(name)
				require.NotNil(t, entry)

				blob, err := repo.LookupBlob(entry.Id)
				require.NoError(t, err)
				require.Equal(t, []byte(contents), blob.Contents())
			}
		})
	}
}

func TestCherryPickStructuredErrors(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, repo, repoPath := testcfg.BuildWithRepo(t)

	testcfg.BuildGitalyGit2Go(t, cfg)
	executor := buildExecutor(t, cfg)

	base := gitalygit.WriteTestCommit(
		t,
		cfg,
		repoPath,
		gitalygit.WithTreeEntries(gitalygit.TreeEntry{
			Path: "file", Content: "foo", Mode: "100644",
		}))

	ours := gitalygit.WriteTestCommit(
		t,
		cfg,
		repoPath,
		gitalygit.WithParents(base),
		gitalygit.WithTreeEntries(
			gitalygit.TreeEntry{Path: "file", Content: "fooqux", Mode: "100644"},
		)).String()

	commit := gitalygit.WriteTestCommit(
		t,
		cfg,
		repoPath,
		gitalygit.WithParents(base),
		gitalygit.WithTreeEntries(
			gitalygit.TreeEntry{Path: "file", Content: "foobar", Mode: "100644"},
		)).String()

	committer := git.Signature{
		Name:  "Baz",
		Email: "baz@example.com",
		When:  time.Date(2021, 1, 17, 14, 45, 51, 0, time.FixedZone("", +2*60*60)),
	}

	_, err := executor.CherryPick(ctx, repo, git2go.CherryPickCommand{
		Repository:    repoPath,
		CommitterName: committer.Name,
		CommitterMail: committer.Email,
		CommitterDate: committer.When,
		Message:       "Foo",
		Ours:          ours,
		Commit:        commit,
	})

	require.EqualError(t, err, "cherry-pick: there are conflicting files")
	require.ErrorAs(t, err, &git2go.ConflictingFilesError{})
}
