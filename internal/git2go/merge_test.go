//go:build static && system_libgit2 && !gitaly_test_sha256

package git2go

import (
	"fmt"
	"testing"
	"time"

	libgit2 "github.com/libgit2/git2go/v33"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/cmd/gitaly-git2go/git2goutil"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestMerge_missingArguments(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, repo, repoPath := testcfg.BuildWithRepo(t)
	executor := NewExecutor(cfg, gittest.NewCommandFactory(t, cfg), config.NewLocator(cfg))

	testcases := []struct {
		desc        string
		request     MergeCommand
		expectedErr string
	}{
		{
			desc:        "no arguments",
			expectedErr: "merge: invalid parameters: missing repository",
		},
		{
			desc:        "missing repository",
			request:     MergeCommand{AuthorName: "Foo", AuthorMail: "foo@example.com", Message: "Foo", Ours: "HEAD", Theirs: "HEAD"},
			expectedErr: "merge: invalid parameters: missing repository",
		},
		{
			desc:        "missing author name",
			request:     MergeCommand{Repository: repoPath, AuthorMail: "foo@example.com", Message: "Foo", Ours: "HEAD", Theirs: "HEAD"},
			expectedErr: "merge: invalid parameters: missing author name",
		},
		{
			desc:        "missing author mail",
			request:     MergeCommand{Repository: repoPath, AuthorName: "Foo", Message: "Foo", Ours: "HEAD", Theirs: "HEAD"},
			expectedErr: "merge: invalid parameters: missing author mail",
		},
		{
			desc:        "missing message",
			request:     MergeCommand{Repository: repoPath, AuthorName: "Foo", AuthorMail: "foo@example.com", Ours: "HEAD", Theirs: "HEAD"},
			expectedErr: "merge: invalid parameters: missing message",
		},
		{
			desc:        "missing ours",
			request:     MergeCommand{Repository: repoPath, AuthorName: "Foo", AuthorMail: "foo@example.com", Message: "Foo", Theirs: "HEAD"},
			expectedErr: "merge: invalid parameters: missing ours",
		},
		{
			desc:        "missing theirs",
			request:     MergeCommand{Repository: repoPath, AuthorName: "Foo", AuthorMail: "foo@example.com", Message: "Foo", Ours: "HEAD"},
			expectedErr: "merge: invalid parameters: missing theirs",
		},
		// Committer* arguments are required only when at least one of them is non-empty
		{
			desc:        "missing committer mail",
			request:     MergeCommand{Repository: repoPath, AuthorName: "Foo", AuthorMail: "foo@example.com", CommitterName: "Bar", Message: "Foo", Theirs: "HEAD", Ours: "HEAD"},
			expectedErr: "merge: invalid parameters: missing committer mail",
		},
		{
			desc:        "missing committer name",
			request:     MergeCommand{Repository: repoPath, AuthorName: "Foo", AuthorMail: "foo@example.com", CommitterMail: "bar@example.com", Message: "Foo", Theirs: "HEAD", Ours: "HEAD"},
			expectedErr: "merge: invalid parameters: missing committer name",
		},
		{
			desc:        "missing committer date",
			request:     MergeCommand{Repository: repoPath, AuthorName: "Foo", AuthorMail: "foo@example.com", CommitterName: "Bar", CommitterMail: "bar@example.com", Message: "Foo", Theirs: "HEAD", Ours: "HEAD"},
			expectedErr: "merge: invalid parameters: missing committer date",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := executor.Merge(ctx, repo, tc.request)
			require.Error(t, err)
			require.Equal(t, tc.expectedErr, err.Error())
		})
	}
}

func TestMerge_invalidRepositoryPath(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, repo, _ := testcfg.BuildWithRepo(t)
	testcfg.BuildGitalyGit2Go(t, cfg)
	executor := NewExecutor(cfg, gittest.NewCommandFactory(t, cfg), config.NewLocator(cfg))

	_, err := executor.Merge(ctx, repo, MergeCommand{
		Repository: "/does/not/exist", AuthorName: "Foo", AuthorMail: "foo@example.com", Message: "Foo", Ours: "HEAD", Theirs: "HEAD",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "merge: could not open repository")
}

func TestMerge_trees(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	testcases := []struct {
		desc             string
		base             []gittest.TreeEntry
		ours             []gittest.TreeEntry
		theirs           []gittest.TreeEntry
		expected         map[string]string
		withCommitter    bool
		squash           bool
		expectedResponse MergeResult
		expectedErr      error
	}{
		{
			desc: "trivial merge succeeds",
			base: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			expected: map[string]string{
				"file": "a",
			},
			expectedResponse: MergeResult{
				CommitID: "0db317551c49eddadde2b337550d8e57d9536886",
			},
		},
		{
			desc: "trivial merge with different committer succeeds",
			base: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			expected: map[string]string{
				"file": "a",
			},
			withCommitter: true,
			expectedResponse: MergeResult{
				CommitID: "38dcbe72d91ed5621286290f70df9a5dd08f5cb6",
			},
		},
		{
			desc: "trivial squash succeeds",
			base: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			expected: map[string]string{
				"file": "a",
			},
			squash: true,
			expectedResponse: MergeResult{
				CommitID: "a0781480ce3cbba80440e6c112c5ee7f718ed3c2",
			},
		},
		{
			desc: "non-trivial merge succeeds",
			base: []gittest.TreeEntry{
				{Path: "file", Content: "a\nb\nc\nd\ne\nf\n", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "file", Content: "0\na\nb\nc\nd\ne\nf\n", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "file", Content: "a\nb\nc\nd\ne\nf\n0\n", Mode: "100644"},
			},
			expected: map[string]string{
				"file": "0\na\nb\nc\nd\ne\nf\n0\n",
			},
			expectedResponse: MergeResult{
				CommitID: "3c030d1ee80bbb005666619375fa0629c86b9534",
			},
		},
		{
			desc: "non-trivial squash succeeds",
			base: []gittest.TreeEntry{
				{Path: "file", Content: "a\nb\nc\nd\ne\nf\n", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "file", Content: "0\na\nb\nc\nd\ne\nf\n", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "file", Content: "a\nb\nc\nd\ne\nf\n0\n", Mode: "100644"},
			},
			expected: map[string]string{
				"file": "0\na\nb\nc\nd\ne\nf\n0\n",
			},
			squash: true,
			expectedResponse: MergeResult{
				CommitID: "43853c4a027a67c7e39afa8e7ef0a34a1874ef26",
			},
		},
		{
			desc: "multiple files succeed",
			base: []gittest.TreeEntry{
				{Path: "1", Content: "foo", Mode: "100644"},
				{Path: "2", Content: "bar", Mode: "100644"},
				{Path: "3", Content: "qux", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "1", Content: "foo", Mode: "100644"},
				{Path: "2", Content: "modified", Mode: "100644"},
				{Path: "3", Content: "qux", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "1", Content: "modified", Mode: "100644"},
				{Path: "2", Content: "bar", Mode: "100644"},
				{Path: "3", Content: "qux", Mode: "100644"},
			},
			expected: map[string]string{
				"1": "modified",
				"2": "modified",
				"3": "qux",
			},
			expectedResponse: MergeResult{
				CommitID: "6be1fdb2c4116881c7a82575be41618e8a690ff4",
			},
		},
		{
			desc: "multiple files squash succeed",
			base: []gittest.TreeEntry{
				{Path: "1", Content: "foo", Mode: "100644"},
				{Path: "2", Content: "bar", Mode: "100644"},
				{Path: "3", Content: "qux", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "1", Content: "foo", Mode: "100644"},
				{Path: "2", Content: "modified", Mode: "100644"},
				{Path: "3", Content: "qux", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "1", Content: "modified", Mode: "100644"},
				{Path: "2", Content: "bar", Mode: "100644"},
				{Path: "3", Content: "qux", Mode: "100644"},
			},
			expected: map[string]string{
				"1": "modified",
				"2": "modified",
				"3": "qux",
			},
			squash: true,
			expectedResponse: MergeResult{
				CommitID: "fe094a98b22ac53e1da1a9eb16118ce49f01fdbe",
			},
		},
		{
			desc: "conflicting merge fails",
			base: []gittest.TreeEntry{
				{Path: "1", Content: "foo", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "1", Content: "bar", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "1", Content: "qux", Mode: "100644"},
			},
			expectedErr: fmt.Errorf("merge: %w", ConflictingFilesError{
				ConflictingFiles: []string{"1"},
			}),
		},
	}

	for _, tc := range testcases {
		cfg, repoProto, repoPath := testcfg.BuildWithRepo(t)
		testcfg.BuildGitalyGit2Go(t, cfg)
		executor := NewExecutor(cfg, gittest.NewCommandFactory(t, cfg), config.NewLocator(cfg))

		base := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(tc.base...))
		ours := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(base), gittest.WithTreeEntries(tc.ours...))
		theirs := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(base), gittest.WithTreeEntries(tc.theirs...))

		authorDate := time.Date(2020, 7, 30, 7, 45, 50, 0, time.FixedZone("UTC+2", +2*60*60))
		committerDate := time.Date(2021, 7, 30, 7, 45, 50, 0, time.FixedZone("UTC+2", +2*60*60))

		t.Run(tc.desc, func(t *testing.T) {
			mergeCommand := MergeCommand{
				Repository: repoPath,
				AuthorName: "John Doe",
				AuthorMail: "john.doe@example.com",
				AuthorDate: authorDate,
				Message:    "Merge message",
				Ours:       ours.String(),
				Theirs:     theirs.String(),
				Squash:     tc.squash,
			}
			if tc.withCommitter {
				mergeCommand.CommitterName = "Jane Doe"
				mergeCommand.CommitterMail = "jane.doe@example.com"
				mergeCommand.CommitterDate = committerDate
			}
			response, err := executor.Merge(ctx, repoProto, mergeCommand)

			if tc.expectedErr != nil {
				require.Equal(t, tc.expectedErr, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expectedResponse, response)

			repo, err := git2goutil.OpenRepository(repoPath)
			require.NoError(t, err)
			defer repo.Free()

			commitOid, err := libgit2.NewOid(response.CommitID)
			require.NoError(t, err)

			commit, err := repo.LookupCommit(commitOid)
			require.NoError(t, err)

			tree, err := commit.Tree()
			require.NoError(t, err)
			require.EqualValues(t, len(tc.expected), tree.EntryCount())

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

func TestMerge_squash(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repoProto, repoPath := testcfg.BuildWithRepo(t)
	testcfg.BuildGitalyGit2Go(t, cfg)
	executor := NewExecutor(cfg, gittest.NewCommandFactory(t, cfg), config.NewLocator(cfg))

	baseFile := gittest.TreeEntry{Path: "file.txt", Content: "b\nc", Mode: "100644"}
	ourFile := gittest.TreeEntry{Path: "file.txt", Content: "a\nb\nc", Mode: "100644"}
	theirFile1 := gittest.TreeEntry{Path: "file.txt", Content: "b\nc\nd", Mode: "100644"}
	theirFile2 := gittest.TreeEntry{Path: "file.txt", Content: "b\nc\nd\ne", Mode: "100644"}

	base := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(baseFile))
	ours := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(base), gittest.WithTreeEntries(ourFile))
	theirs1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(base), gittest.WithTreeEntries(theirFile1))
	theirs2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(theirs1), gittest.WithTreeEntries(theirFile2))

	date := time.Date(2020, 7, 30, 7, 45, 50, 0, time.FixedZone("UTC+2", +2*60*60))
	response, err := executor.Merge(ctx, repoProto, MergeCommand{
		Repository: repoPath,
		AuthorName: "John Doe",
		AuthorMail: "john.doe@example.com",
		AuthorDate: date,
		Message:    "Merge message",
		Ours:       ours.String(),
		Theirs:     theirs2.String(),
		Squash:     true,
	})
	require.NoError(t, err)
	assert.Equal(t, "882b43b68d160876e3833dc6bbabf7032058e837", response.CommitID)

	repo, err := git2goutil.OpenRepository(repoPath)
	require.NoError(t, err)

	commitOid, err := libgit2.NewOid(response.CommitID)
	require.NoError(t, err)

	theirs2Oid, err := libgit2.NewOid(theirs2.String())
	require.NoError(t, err)
	isDescendant, err := repo.DescendantOf(commitOid, theirs2Oid)
	require.NoError(t, err)
	require.False(t, isDescendant)

	commit, err := repo.LookupCommit(commitOid)
	require.NoError(t, err)

	require.Equal(t, uint(1), commit.ParentCount())
	require.Equal(t, ours.String(), commit.ParentId(0).String())

	tree, err := commit.Tree()
	require.NoError(t, err)

	entry := tree.EntryByName("file.txt")
	require.NotNil(t, entry)

	blob, err := repo.LookupBlob(entry.Id)
	require.NoError(t, err)
	require.Equal(t, "a\nb\nc\nd\ne", string(blob.Contents()))
}

func TestMerge_recursive(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyGit2Go(t, cfg)
	executor := NewExecutor(cfg, gittest.NewCommandFactory(t, cfg), config.NewLocator(cfg))

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	base := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "base", Content: "base\n", Mode: "100644"},
	))

	ours := make([]git.ObjectID, MergeRecursionLimit)
	ours[0] = gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(base), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "base", Content: "base\n", Mode: "100644"},
		gittest.TreeEntry{Path: "ours", Content: "ours-0\n", Mode: "100644"},
	))

	theirs := make([]git.ObjectID, MergeRecursionLimit)
	theirs[0] = gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(base), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "base", Content: "base\n", Mode: "100644"},
		gittest.TreeEntry{Path: "theirs", Content: "theirs-0\n", Mode: "100644"},
	))

	// We're now creating a set of criss-cross merges which look like the following graph:
	//
	//        o---o---o---o---o-   -o---o ours
	//       / \ / \ / \ / \ / \ . / \ /
	// base o   X   X   X   X    .    X
	//       \ / \ / \ / \ / \ / . \ / \
	//        o---o---o---o---o-   -o---o theirs
	//
	// We then merge ours with theirs. The peculiarity about this merge is that the merge base
	// is not unique, and as a result the merge will generate virtual merge bases for each of
	// the criss-cross merges. This operation may thus be heavily expensive to perform.
	for i := 1; i < MergeRecursionLimit; i++ {
		ours[i] = gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(ours[i-1], theirs[i-1]), gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "base", Content: "base\n", Mode: "100644"},
			gittest.TreeEntry{Path: "ours", Content: fmt.Sprintf("ours-%d\n", i), Mode: "100644"},
			gittest.TreeEntry{Path: "theirs", Content: fmt.Sprintf("theirs-%d\n", i-1), Mode: "100644"},
		))

		theirs[i] = gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(theirs[i-1], ours[i-1]), gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "base", Content: "base\n", Mode: "100644"},
			gittest.TreeEntry{Path: "ours", Content: fmt.Sprintf("ours-%d\n", i-1), Mode: "100644"},
			gittest.TreeEntry{Path: "theirs", Content: fmt.Sprintf("theirs-%d\n", i), Mode: "100644"},
		))
	}

	authorDate := time.Date(2020, 7, 30, 7, 45, 50, 0, time.FixedZone("UTC+2", +2*60*60))

	// When creating the criss-cross merges, we have been doing evil merges
	// as each merge has applied changes from the other side while at the
	// same time incrementing the own file contents. As we exceed the merge
	// limit, git will just pick one of both possible merge bases when
	// hitting that limit instead of computing another virtual merge base.
	// The result is thus a merge of the following three commits:
	//
	// merge base           ours                theirs
	// ----------           ----                ------
	//
	// base:   "base"       base:   "base"      base:   "base"
	// theirs: "theirs-1"   theirs: "theirs-1   theirs: "theirs-2"
	// ours:   "ours-0"     ours:   "ours-2"    ours:   "ours-1"
	//
	// This is a classical merge commit as "ours" differs in all three
	// cases. We thus expect a merge conflict, which unfortunately
	// demonstrates that restricting the recursion limit may cause us to
	// fail resolution.
	_, err := executor.Merge(ctx, repoProto, MergeCommand{
		Repository: repoPath,
		AuthorName: "John Doe",
		AuthorMail: "john.doe@example.com",
		AuthorDate: authorDate,
		Message:    "Merge message",
		Ours:       ours[len(ours)-1].String(),
		Theirs:     theirs[len(theirs)-1].String(),
	})
	require.Equal(t, fmt.Errorf("merge: %w", ConflictingFilesError{
		ConflictingFiles: []string{"theirs"},
	}), err)

	// Otherwise, if we're merging an earlier criss-cross merge which has
	// half of the limit many criss-cross patterns, we exactly hit the
	// recursion limit and thus succeed.
	response, err := executor.Merge(ctx, repoProto, MergeCommand{
		Repository: repoPath,
		AuthorName: "John Doe",
		AuthorMail: "john.doe@example.com",
		AuthorDate: authorDate,
		Message:    "Merge message",
		Ours:       ours[MergeRecursionLimit/2].String(),
		Theirs:     theirs[MergeRecursionLimit/2].String(),
	})
	require.NoError(t, err)

	repo, err := git2goutil.OpenRepository(repoPath)
	require.NoError(t, err)

	commitOid, err := libgit2.NewOid(response.CommitID)
	require.NoError(t, err)

	commit, err := repo.LookupCommit(commitOid)
	require.NoError(t, err)

	tree, err := commit.Tree()
	require.NoError(t, err)

	require.EqualValues(t, 3, tree.EntryCount())
	for name, contents := range map[string]string{
		"base":   "base\n",
		"ours":   "ours-10\n",
		"theirs": "theirs-10\n",
	} {
		entry := tree.EntryByName(name)
		require.NotNil(t, entry)

		blob, err := repo.LookupBlob(entry.Id)
		require.NoError(t, err)
		require.Equal(t, []byte(contents), blob.Contents())
	}
}
