package gitpipe

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestRevlist(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	blobA := gittest.WriteBlob(t, cfg, repoPath, bytes.Repeat([]byte("a"), 133))
	blobB := gittest.WriteBlob(t, cfg, repoPath, bytes.Repeat([]byte("b"), 127))
	blobC := gittest.WriteBlob(t, cfg, repoPath, bytes.Repeat([]byte("c"), 127))
	blobD := gittest.WriteBlob(t, cfg, repoPath, bytes.Repeat([]byte("d"), 129))

	blob := gittest.WriteBlob(t, cfg, repoPath, []byte("a"))
	subblob := gittest.WriteBlob(t, cfg, repoPath, []byte("larger blob"))

	treeA := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "branch-test.txt", Mode: "100644", OID: blob},
	})
	commitA := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithTree(treeA),
		gittest.WithCommitterDate(time.Date(2000, 1, 1, 1, 1, 1, 1, time.UTC)),
	)

	subtree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "subblob", Mode: "100644", OID: subblob},
	})
	treeB := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "branch-test.txt", Mode: "100644", OID: blob},
		{Path: "subtree", Mode: "040000", OID: subtree},
	})
	commitB := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(commitA),
		gittest.WithTree(treeB),
		gittest.WithCommitterDate(time.Date(1999, 1, 1, 1, 1, 1, 1, time.UTC)),
		gittest.WithAuthorName("custom author"),
	)

	commitBParent := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(commitB),
		gittest.WithTree(treeB),
		gittest.WithCommitterDate(time.Date(2001, 1, 1, 1, 1, 1, 1, time.UTC)),
	)
	commitAParent := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(commitA),
		gittest.WithTree(treeA),
		gittest.WithCommitterDate(time.Date(2001, 1, 1, 1, 1, 1, 1, time.UTC)),
	)

	mergeCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(commitAParent, commitBParent))

	tag := gittest.WriteTag(t, cfg, repoPath, "v1.0.0", mergeCommit.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	for _, tc := range []struct {
		desc            string
		revisions       []string
		options         []RevlistOption
		expectedResults []RevisionResult
		expectedErr     error
	}{
		{
			desc: "single blob",
			revisions: []string{
				blobA.String(),
			},
			options: []RevlistOption{
				WithObjects(),
			},
			expectedResults: []RevisionResult{
				{OID: blobA},
			},
		},
		{
			desc: "multiple blobs",
			revisions: []string{
				blobA.String(),
				blobB.String(),
				blobC.String(),
				blobD.String(),
			},
			options: []RevlistOption{
				WithObjects(),
			},
			expectedResults: []RevisionResult{
				{OID: blobA},
				{OID: blobB},
				{OID: blobC},
				{OID: blobD},
			},
		},
		{
			desc: "multiple blobs without objects",
			revisions: []string{
				blobA.String(),
				blobB.String(),
				blobC.String(),
				blobD.String(),
			},
			expectedResults: nil,
		},
		{
			desc: "duplicated blob prints blob once only",
			revisions: []string{
				blobA.String(),
				blobA.String(),
			},
			options: []RevlistOption{
				WithObjects(),
			},
			expectedResults: []RevisionResult{
				{OID: blobA},
			},
		},
		{
			desc: "tree results in object names",
			revisions: []string{
				treeA.String(),
			},
			options: []RevlistOption{
				WithObjects(),
			},
			expectedResults: []RevisionResult{
				{OID: treeA},
				{OID: blob, ObjectName: []byte("branch-test.txt")},
			},
		},
		{
			desc: "tree without objects returns nothing",
			revisions: []string{
				treeA.String(),
			},
			expectedResults: nil,
		},
		{
			desc: "revision without disabled walk",
			revisions: []string{
				commitB.String(),
			},
			options: []RevlistOption{
				WithDisabledWalk(),
			},
			expectedResults: []RevisionResult{
				{OID: commitB},
			},
		},
		{
			desc: "revision range",
			revisions: []string{
				"^" + commitB.String() + "~",
				commitB.String(),
			},
			options: []RevlistOption{
				WithObjects(),
			},
			expectedResults: []RevisionResult{
				{OID: commitB},
				{OID: treeB},
				{OID: subtree, ObjectName: []byte("subtree")},
				{OID: subblob, ObjectName: []byte("subtree/subblob")},
			},
		},
		{
			desc: "revision range without objects",
			revisions: []string{
				"^" + commitB.String() + "~",
				commitB.String(),
			},
			expectedResults: []RevisionResult{
				{OID: commitB},
			},
		},
		{
			desc: "revision range without objects with at most one parent",
			revisions: []string{
				mergeCommit.String(),
			},
			options: []RevlistOption{
				WithMaxParents(1),
			},
			expectedResults: []RevisionResult{
				{OID: commitAParent},
				{OID: commitBParent},
				{OID: commitA},
				{OID: commitB},
			},
		},
		{
			desc: "reverse revision range without objects",
			revisions: []string{
				commitB.String(),
			},
			options: []RevlistOption{
				WithReverse(),
			},
			expectedResults: []RevisionResult{
				{OID: commitA},
				{OID: commitB},
			},
		},
		{
			desc: "reverse revision range with objects",
			revisions: []string{
				commitB.String(),
			},
			options: []RevlistOption{
				WithReverse(),
				WithObjects(),
			},
			expectedResults: []RevisionResult{
				// Note that only commits are listed in reverse,
				// their referenced objects stay in the same order.
				{OID: commitA},
				{OID: treeA},
				{OID: blob, ObjectName: []byte("branch-test.txt")},
				{OID: commitB},
				{OID: treeB},
				{OID: subtree, ObjectName: []byte("subtree")},
				{OID: subblob, ObjectName: []byte("subtree/subblob")},
			},
		},
		{
			desc: "revision range with topo order",
			revisions: []string{
				mergeCommit.String(),
			},
			options: []RevlistOption{
				WithOrder(OrderTopo),
			},
			expectedResults: []RevisionResult{
				// Note that the order here is different from the order in the next
				// testcase, where we use date-order.
				{OID: mergeCommit},
				{OID: commitBParent},
				{OID: commitB},
				{OID: commitAParent},
				{OID: commitA},
			},
		},
		{
			desc: "revision range with date order",
			revisions: []string{
				mergeCommit.String(),
			},
			options: []RevlistOption{
				WithOrder(OrderDate),
			},
			expectedResults: []RevisionResult{
				{OID: mergeCommit},
				{OID: commitAParent},
				{OID: commitBParent},
				{OID: commitB},
				{OID: commitA},
			},
		},
		{
			desc: "revision range with dates",
			revisions: []string{
				mergeCommit.String(),
			},
			options: []RevlistOption{
				WithBefore(time.Date(2000, 12, 1, 1, 1, 1, 1, time.UTC)),
				WithAfter(time.Date(1999, 12, 1, 1, 1, 1, 1, time.UTC)),
			},
			expectedResults: []RevisionResult{
				{OID: commitA},
			},
		},
		{
			desc: "revision range with author",
			revisions: []string{
				mergeCommit.String(),
			},
			options: []RevlistOption{
				WithAuthor([]byte("custom author")),
			},
			expectedResults: []RevisionResult{
				{OID: commitB},
			},
		},
		{
			desc: "first parent chain",
			revisions: []string{
				mergeCommit.String(),
			},
			options: []RevlistOption{
				WithFirstParent(),
			},
			expectedResults: []RevisionResult{
				{OID: mergeCommit},
				{OID: commitAParent},
				{OID: commitA},
			},
		},
		{
			// This is a tree object with multiple blobs. We cannot directly filter
			// blobs given that Git will always print whatever's been provided on the
			// command line. While we can already fix this with Git v2.32.0 via
			// the new `--filter-provided` option, let's defer this fix to a later
			// point. We demonstrate that this option is working by having the same test
			// twice, once without and once with limit.
			desc: "tree with multiple blobs without limit",
			revisions: []string{
				treeB.String(),
			},
			options: []RevlistOption{
				WithObjects(),
			},
			expectedResults: []RevisionResult{
				{OID: treeB},
				{OID: blob, ObjectName: []byte("branch-test.txt")},
				{OID: subtree, ObjectName: []byte("subtree")},
				{OID: subblob, ObjectName: []byte("subtree/subblob")},
			},
		},
		{
			// And now the second time we execute this test with a limit and see that we
			// get less blobs as result.
			desc: "tree with multiple blobs with limit",
			revisions: []string{
				treeB.String(),
			},
			options: []RevlistOption{
				WithObjects(),
				WithBlobLimit(5),
			},
			expectedResults: []RevisionResult{
				{OID: treeB},
				{OID: blob, ObjectName: []byte("branch-test.txt")},
				{OID: subtree, ObjectName: []byte("subtree")},
			},
		},
		{
			desc: "tree with blob object type filter",
			revisions: []string{
				treeB.String(),
			},
			options: []RevlistOption{
				WithObjects(),
				WithObjectTypeFilter(ObjectTypeBlob),
			},
			expectedResults: []RevisionResult{
				{OID: blob, ObjectName: []byte("branch-test.txt")},
				{OID: subblob, ObjectName: []byte("subtree/subblob")},
			},
		},
		{
			desc: "tree with tag object type filter",
			revisions: []string{
				"--all",
			},
			options: []RevlistOption{
				WithObjects(),
				WithObjectTypeFilter(ObjectTypeTag),
			},
			expectedResults: []RevisionResult{
				{OID: tag, ObjectName: []byte("v1.0.0")},
			},
		},
		{
			desc: "tree with tree object type filter",
			revisions: []string{
				commitA.String(),
			},
			options: []RevlistOption{
				WithObjects(),
				WithObjectTypeFilter(ObjectTypeTree),
			},
			expectedResults: []RevisionResult{
				{OID: treeA},
			},
		},
		{
			desc: "tree with commit object type filter",
			revisions: []string{
				commitB.String(),
			},
			options: []RevlistOption{
				WithObjects(),
				WithObjectTypeFilter(ObjectTypeCommit),
			},
			expectedResults: []RevisionResult{
				{OID: commitB},
				{OID: commitA},
			},
		},
		{
			desc: "tree with object type and blob size filter",
			revisions: []string{
				mergeCommit.String(),
			},
			options: []RevlistOption{
				WithObjects(),
				WithBlobLimit(5),
				WithObjectTypeFilter(ObjectTypeBlob),
			},
			expectedResults: []RevisionResult{
				{OID: blob, ObjectName: []byte("branch-test.txt")},
			},
		},
		{
			desc: "invalid revision",
			revisions: []string{
				"refs/heads/does-not-exist",
			},
			expectedErr: errors.New("rev-list pipeline command: exit status 128, stderr: " +
				"\"fatal: ambiguous argument 'refs/heads/does-not-exist': unknown revision or path not in the working tree.\\n" +
				"Use '--' to separate paths from revisions, like this:\\n" +
				"'git <command> [<revision>...] -- [<file>...]'\\n\""),
		},
		{
			desc: "mixed valid and invalid revision",
			revisions: []string{
				blobA.String(),
				"refs/heads/does-not-exist",
			},
			expectedErr: errors.New("rev-list pipeline command: exit status 128, stderr: " +
				"\"fatal: ambiguous argument 'refs/heads/does-not-exist': unknown revision or path not in the working tree.\\n" +
				"Use '--' to separate paths from revisions, like this:\\n" +
				"'git <command> [<revision>...] -- [<file>...]'\\n\""),
		},
		{
			desc: "skip everything",
			revisions: []string{
				mergeCommit.String(),
			},
			options: []RevlistOption{
				WithObjects(),
				WithObjectTypeFilter(ObjectTypeBlob),
				WithSkipRevlistResult(func(*RevisionResult) bool { return true }),
			},
		},
		{
			desc: "skip nothing",
			revisions: []string{
				mergeCommit.String(),
			},
			options: []RevlistOption{
				WithObjects(),
				WithObjectTypeFilter(ObjectTypeBlob),
				WithSkipRevlistResult(func(*RevisionResult) bool { return false }),
			},
			expectedResults: []RevisionResult{
				{OID: blob, ObjectName: []byte("branch-test.txt")},
				{OID: subblob, ObjectName: []byte("subtree/subblob")},
			},
		},
		{
			desc: "skip one",
			revisions: []string{
				mergeCommit.String(),
			},
			options: []RevlistOption{
				WithObjects(),
				WithObjectTypeFilter(ObjectTypeBlob),
				WithSkipRevlistResult(func(r *RevisionResult) bool {
					return bytes.Equal(r.ObjectName, []byte("branch-test.txt"))
				}),
			},
			expectedResults: []RevisionResult{
				{OID: subblob, ObjectName: []byte("subtree/subblob")},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			it := Revlist(ctx, repo, tc.revisions, tc.options...)

			var results []RevisionResult
			for it.Next() {
				results = append(results, it.Result())
			}

			// We're converting the error here to a plain un-nested error such that we
			// don't have to replicate the complete error's structure.
			err := it.Err()
			if err != nil {
				err = errors.New(err.Error())
			}

			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedResults, results)
		})
	}

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(testhelper.Context(t))

		it := Revlist(ctx, repo, []string{mergeCommit.String()})

		require.True(t, it.Next())
		require.NoError(t, it.Err())
		require.Equal(t, RevisionResult{
			OID: mergeCommit,
		}, it.Result())

		cancel()

		require.False(t, it.Next())
		require.Equal(t, context.Canceled, it.Err())
		require.Equal(t, RevisionResult{
			err: context.Canceled,
		}, it.Result())
	})
}

func TestForEachRef(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	readRefs := func(t *testing.T, repo *localrepo.Repo, patterns []string, opts ...ForEachRefOption) []RevisionResult {
		it := ForEachRef(ctx, repo, patterns, opts...)

		var results []RevisionResult
		for it.Next() {
			results = append(results, it.Result())
		}
		require.NoError(t, it.Err())

		return results
	}

	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	mainCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("main"))
	featureCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"), gittest.WithMessage("feature"))
	tag := gittest.WriteTag(t, cfg, repoPath, "v1.0.0", featureCommit.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	t.Run("single fully qualified branch", func(t *testing.T) {
		require.Equal(t, []RevisionResult{
			{
				ObjectName: []byte("refs/heads/main"),
				OID:        mainCommit,
			},
		}, readRefs(t, repo, []string{"refs/heads/main"}))
	})

	t.Run("unqualified branch name", func(t *testing.T) {
		require.Nil(t, readRefs(t, repo, []string{"main"}))
	})

	t.Run("multiple branches", func(t *testing.T) {
		require.Equal(t, []RevisionResult{
			{
				ObjectName: []byte("refs/heads/feature"),
				OID:        featureCommit,
			},
			{
				ObjectName: []byte("refs/heads/main"),
				OID:        mainCommit,
			},
		}, readRefs(t, repo, []string{"refs/heads/main", "refs/heads/feature"}))
	})

	t.Run("branches pattern", func(t *testing.T) {
		refs := readRefs(t, repo, []string{"refs/heads/*"})
		require.Equal(t, refs, []RevisionResult{
			{
				ObjectName: []byte("refs/heads/feature"),
				OID:        featureCommit,
			},
			{
				ObjectName: []byte("refs/heads/main"),
				OID:        mainCommit,
			},
		})
	})

	t.Run("tag with format", func(t *testing.T) {
		refs := readRefs(t, repo, []string{"refs/tags/v1.0.0"},
			WithForEachRefFormat("%(objectname) tag\n%(*objectname) peeled"),
		)

		require.Equal(t, refs, []RevisionResult{
			{
				ObjectName: []byte("tag"),
				OID:        tag,
			},
			{
				ObjectName: []byte("peeled"),
				OID:        featureCommit,
			},
		})
	})

	t.Run("multiple patterns", func(t *testing.T) {
		refs := readRefs(t, repo, []string{"refs/heads/*", "refs/tags/*"})
		require.Equal(t, refs, []RevisionResult{
			{
				ObjectName: []byte("refs/heads/feature"),
				OID:        featureCommit,
			},
			{
				ObjectName: []byte("refs/heads/main"),
				OID:        mainCommit,
			},
			{
				ObjectName: []byte("refs/tags/v1.0.0"),
				OID:        tag,
			},
		})
	})

	t.Run("nonexisting branch", func(t *testing.T) {
		require.Nil(t, readRefs(t, repo, []string{"refs/heads/idontexist"}))
	})

	t.Run("nonexisting pattern", func(t *testing.T) {
		require.Nil(t, readRefs(t, repo, []string{"refs/idontexist/*"}))
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(testhelper.Context(t))

		it := ForEachRef(ctx, repo, []string{"refs/heads/*"})

		require.True(t, it.Next())
		require.NoError(t, it.Err())
		require.Equal(t, RevisionResult{
			OID:        featureCommit,
			ObjectName: []byte("refs/heads/feature"),
		}, it.Result())

		cancel()

		require.False(t, it.Next())
		require.Equal(t, context.Canceled, it.Err())
		require.Equal(t, RevisionResult{
			err: context.Canceled,
		}, it.Result())
	})
}

func TestForEachRef_options(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		// prepare is a function that prepares a repository and returns an oid to match on
		prepare  func(repoPath string, cfg config.Cfg) string
		desc     string
		options  []ForEachRefOption
		refnames []string
	}{
		{
			desc: "with limit",
			prepare: func(repoPath string, cfg config.Cfg) string {
				oid := string(gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage(t.Name())))

				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-1", oid)
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-2", oid)
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-3", oid)
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-4", oid)

				return oid
			},
			options: []ForEachRefOption{
				WithCount(2),
			},
			refnames: []string{
				"refs/heads/branch-1",
				"refs/heads/branch-2",
			},
		},
		{
			desc: "with sort key",
			prepare: func(repoPath string, cfg config.Cfg) string {
				oid := string(gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage(t.Name())))

				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-b", oid)
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-a", oid)
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-d", oid)
				gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/branch-c", oid)

				return oid
			},
			options: []ForEachRefOption{
				WithSortField("refname"),
			},
			refnames: []string{
				"refs/heads/branch-a",
				"refs/heads/branch-b",
				"refs/heads/branch-c",
				"refs/heads/branch-d",
			},
		},
	} {
		cfg := testcfg.Build(t)

		repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)
		oid := tc.prepare(repoPath, cfg)

		forEachRef := ForEachRef(ctx, repo, nil, append(tc.options, WithPointsAt(oid))...)

		var i int
		for forEachRef.Next() {
			assert.Equal(t, tc.refnames[i], string(forEachRef.Result().ObjectName))
			i++
		}

		assert.Equal(t, i, len(tc.refnames))
	}
}
