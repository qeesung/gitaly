package gitpipe

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestPipeline_revlist(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	blobA := gittest.WriteBlob(t, cfg, repoPath, []byte("blob a"))
	blobB := gittest.WriteBlob(t, cfg, repoPath, []byte("b"))
	blobC := gittest.WriteBlob(t, cfg, repoPath, []byte("longer blob c"))

	subtree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "subblob", Mode: "100644", OID: blobA},
	})
	tree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "blob", Mode: "100644", OID: blobB},
		{Path: "subtree", Mode: "040000", OID: subtree},
	})

	commitA := gittest.WriteCommit(t, cfg, repoPath)
	commitB := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(commitA), gittest.WithTree(tree), gittest.WithBranch("main"))

	for _, tc := range []struct {
		desc               string
		revisions          []string
		revlistOptions     []RevlistOption
		catfileInfoOptions []CatfileInfoOption
		expectedResults    []CatfileObjectResult
		expectedErr        error
	}{
		{
			desc: "single blob",
			revisions: []string{
				blobA.String(),
			},
			revlistOptions: []RevlistOption{
				WithObjects(),
			},
			expectedResults: []CatfileObjectResult{
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 6}}},
			},
		},
		{
			desc: "single blob without objects",
			revisions: []string{
				blobA.String(),
			},
			expectedResults: nil,
		},
		{
			desc: "multiple blobs",
			revisions: []string{
				blobA.String(),
				blobB.String(),
				blobC.String(),
			},
			revlistOptions: []RevlistOption{
				WithObjects(),
			},
			expectedResults: []CatfileObjectResult{
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 6}}},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 1}}},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobC, Type: "blob", Size: 13}}},
			},
		},
		{
			desc: "multiple blobs with filter",
			revisions: []string{
				blobA.String(),
				blobB.String(),
				blobC.String(),
			},
			revlistOptions: []RevlistOption{
				WithObjects(),
				WithSkipRevlistResult(func(r *RevisionResult) bool {
					return r.OID != blobB
				}),
			},
			expectedResults: []CatfileObjectResult{
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 1}}},
			},
		},
		{
			desc: "tree",
			revisions: []string{
				tree.String(),
			},
			revlistOptions: []RevlistOption{
				WithObjects(),
			},
			expectedResults: []CatfileObjectResult{
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: tree, Type: "tree", Size: hashDependentObjectSize(66, 90)}}},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 1}}, ObjectName: []byte("blob")},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: subtree, Type: "tree", Size: hashDependentObjectSize(35, 47)}}, ObjectName: []byte("subtree")},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 6}}, ObjectName: []byte("subtree/subblob")},
			},
		},
		{
			desc: "tree without objects",
			revisions: []string{
				tree.String(),
			},
			expectedResults: nil,
		},
		{
			desc: "tree with blob filter",
			revisions: []string{
				tree.String(),
			},
			revlistOptions: []RevlistOption{
				WithObjects(),
			},
			catfileInfoOptions: []CatfileInfoOption{
				WithSkipCatfileInfoResult(func(objectInfo *catfile.ObjectInfo) bool {
					return objectInfo.Type != "blob"
				}),
			},
			expectedResults: []CatfileObjectResult{
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 1}}, ObjectName: []byte("blob")},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 6}}, ObjectName: []byte("subtree/subblob")},
			},
		},
		{
			desc: "revision range",
			revisions: []string{
				"^" + commitB.String() + "~",
				commitB.String(),
			},
			revlistOptions: []RevlistOption{
				WithObjects(),
			},
			expectedResults: []CatfileObjectResult{
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: commitB, Type: "commit", Size: hashDependentObjectSize(225, 273)}}},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: tree, Type: "tree", Size: hashDependentObjectSize(66, 90)}}},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 1}}, ObjectName: []byte("blob")},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: subtree, Type: "tree", Size: hashDependentObjectSize(35, 47)}}, ObjectName: []byte("subtree")},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 6}}, ObjectName: []byte("subtree/subblob")},
			},
		},
		{
			desc: "revision range without objects",
			revisions: []string{
				"^" + commitB.String() + "~",
				commitB.String(),
			},
			expectedResults: []CatfileObjectResult{
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: commitB, Type: "commit", Size: hashDependentObjectSize(225, 273)}}},
			},
		},
		{
			desc: "--all with all filters",
			revisions: []string{
				"--all",
			},
			revlistOptions: []RevlistOption{
				WithObjects(),
				WithSkipRevlistResult(func(r *RevisionResult) bool {
					// Let through two blobs and a tree.
					return r.OID != tree && r.OID != blobA && r.OID != blobB
				}),
			},
			catfileInfoOptions: []CatfileInfoOption{
				WithSkipCatfileInfoResult(func(objectInfo *catfile.ObjectInfo) bool {
					// Only let through blobs, so only the two blobs remain.
					return objectInfo.Type != "blob"
				}),
			},
			expectedResults: []CatfileObjectResult{
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 1}}, ObjectName: []byte("blob")},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 6}}, ObjectName: []byte("subtree/subblob")},
			},
		},
		{
			desc: "invalid revision",
			revisions: []string{
				"doesnotexist",
			},
			expectedErr: errors.New("rev-list pipeline command: exit status 128, stderr: " +
				"\"fatal: ambiguous argument 'doesnotexist': unknown revision or path not in the working tree.\\n" +
				"Use '--' to separate paths from revisions, like this:\\n" +
				"'git <command> [<revision>...] -- [<file>...]'\\n\""),
		},
		{
			desc: "mixed valid and invalid revision",
			revisions: []string{
				blobA.String(),
				"doesnotexist",
				blobB.String(),
			},
			expectedErr: errors.New("rev-list pipeline command: exit status 128, stderr: " +
				"\"fatal: ambiguous argument 'doesnotexist': unknown revision or path not in the working tree.\\n" +
				"Use '--' to separate paths from revisions, like this:\\n" +
				"'git <command> [<revision>...] -- [<file>...]'\\n\""),
		},
		{
			desc: "invalid revision with all filters",
			revisions: []string{
				"doesnotexist",
			},
			revlistOptions: []RevlistOption{
				WithSkipRevlistResult(func(r *RevisionResult) bool {
					require.Fail(t, "filter should not be invoked on errors")
					return true
				}),
			},
			catfileInfoOptions: []CatfileInfoOption{
				WithSkipCatfileInfoResult(func(r *catfile.ObjectInfo) bool {
					require.Fail(t, "filter should not be invoked on errors")
					return true
				}),
			},
			expectedErr: errors.New("rev-list pipeline command: exit status 128, stderr: " +
				"\"fatal: ambiguous argument 'doesnotexist': unknown revision or path not in the working tree.\\n" +
				"Use '--' to separate paths from revisions, like this:\\n" +
				"'git <command> [<revision>...] -- [<file>...]'\\n\""),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			catfileCache := catfile.NewCache(cfg)
			defer catfileCache.Stop()

			objectInfoReader, cancel, err := catfileCache.ObjectInfoReader(ctx, repo)
			require.NoError(t, err)
			defer cancel()

			objectReader, cancel, err := catfileCache.ObjectReader(ctx, repo)
			require.NoError(t, err)
			defer cancel()

			revlistIter := Revlist(ctx, repo, tc.revisions, tc.revlistOptions...)

			catfileInfoIter, err := CatfileInfo(ctx, objectInfoReader, revlistIter, tc.catfileInfoOptions...)
			require.NoError(t, err)

			catfileObjectIter, err := CatfileObject(ctx, objectReader, catfileInfoIter)
			require.NoError(t, err)

			var results []CatfileObjectResult
			for catfileObjectIter.Next() {
				result := catfileObjectIter.Result()

				objectData, err := io.ReadAll(result)
				require.NoError(t, err)
				require.Len(t, objectData, int(result.ObjectSize()))

				// We only really want to compare the publicly visible fields
				// containing info about the object itself, and not the object's
				// private state. We thus need to reconstruct the objects here.
				results = append(results, CatfileObjectResult{
					Object: &catfile.Object{
						ObjectInfo: catfile.ObjectInfo{
							Oid:  result.ObjectID(),
							Type: result.ObjectType(),
							Size: result.ObjectSize(),
						},
					},
					ObjectName: result.ObjectName,
				})
			}

			// We're converting the error here to a plain un-nested error such that we
			// don't have to replicate the complete error's structure.
			err = catfileObjectIter.Err()
			if err != nil {
				err = errors.New(err.Error())
			}

			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedResults, results)
		})
	}

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(testhelper.Context(t))
		defer cancel()

		catfileCache := catfile.NewCache(cfg)
		defer catfileCache.Stop()

		objectInfoReader, cancelReader, err := catfileCache.ObjectInfoReader(ctx, repo)
		require.NoError(t, err)
		defer cancelReader()

		objectReader, cancelReader, err := catfileCache.ObjectReader(ctx, repo)
		require.NoError(t, err)
		defer cancelReader()

		revlistIter := Revlist(ctx, repo, []string{"--all"})

		catfileInfoIter, err := CatfileInfo(ctx, objectInfoReader, revlistIter)
		require.NoError(t, err)

		catfileObjectIter, err := CatfileObject(ctx, objectReader, catfileInfoIter)
		require.NoError(t, err)

		i := 0
		for catfileObjectIter.Next() {
			i++

			_, err := io.Copy(io.Discard, catfileObjectIter.Result())
			require.NoError(t, err)

			if i == 1 {
				cancel()
			}
		}

		require.Equal(t, context.Canceled, catfileObjectIter.Err())

		// Context cancellation is timing sensitive: at the point of cancelling the context,
		// the last pipeline step may already have queued up an additional result. We thus
		// cannot assert the exact number of requests, but we know that it's bounded.
		require.LessOrEqual(t, i, 4)
	})

	t.Run("interleaving object reads", func(t *testing.T) {
		ctx := testhelper.Context(t)

		catfileCache := catfile.NewCache(cfg)
		defer catfileCache.Stop()

		objectInfoReader, cancel, err := catfileCache.ObjectInfoReader(ctx, repo)
		require.NoError(t, err)
		defer cancel()

		objectReader, cancel, err := catfileCache.ObjectReader(ctx, repo)
		require.NoError(t, err)
		defer cancel()

		revlistIter := Revlist(ctx, repo, []string{"--all"}, WithObjects())

		catfileInfoIter, err := CatfileInfo(ctx, objectInfoReader, revlistIter)
		require.NoError(t, err)

		catfileObjectIter, err := CatfileObject(ctx, objectReader, catfileInfoIter)
		require.NoError(t, err)

		i := 0
		var wg sync.WaitGroup
		for catfileObjectIter.Next() {
			wg.Add(1)
			i++

			// With the catfile package, one mustn't ever request a new object before
			// the old object's reader was completely consumed. We cannot reliably test
			// this given that the object channel, if it behaves correctly, will block
			// until we've read the old object. Chances are high though that we'd
			// eventually hit the race here in case we didn't correctly synchronize on
			// the object reader.
			go func(object CatfileObjectResult) {
				defer wg.Done()
				_, err := io.Copy(io.Discard, object)
				require.NoError(t, err)
			}(catfileObjectIter.Result())
		}

		require.NoError(t, catfileObjectIter.Err())
		wg.Wait()

		// We could in theory assert the exact amount of objects, but this would make it
		// harder than necessary to change the test repo's contents.
		require.Equal(t, i, 7)
	})
}

func TestPipeline_forEachRef(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	keepaliveCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithReference("refs/keep-alive/a"), gittest.WithMessage("keepalive"))
	mainCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("main"))
	featureCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("feature"), gittest.WithMessage("feature"))
	tag := gittest.WriteTag(t, cfg, repoPath, "v1.0.0", mainCommit.Revision(), gittest.WriteTagConfig{
		Message: "annotated",
	})

	catfileCache := catfile.NewCache(cfg)
	defer catfileCache.Stop()

	objectInfoReader, cancel, err := catfileCache.ObjectInfoReader(ctx, repo)
	require.NoError(t, err)
	defer cancel()

	objectReader, cancel, err := catfileCache.ObjectReader(ctx, repo)
	require.NoError(t, err)
	defer cancel()

	forEachRefIter := ForEachRef(ctx, repo, nil)

	catfileInfoIter, err := CatfileInfo(ctx, objectInfoReader, forEachRefIter)
	require.NoError(t, err)

	catfileObjectIter, err := CatfileObject(ctx, objectReader, catfileInfoIter)
	require.NoError(t, err)

	type object struct {
		oid     git.ObjectID
		content []byte
	}

	expectedRefs := map[git.ReferenceName]object{
		"refs/keep-alive/a":  {oid: keepaliveCommit, content: []byte("xx")},
		"refs/heads/main":    {oid: mainCommit, content: []byte("xx")},
		"refs/heads/feature": {oid: featureCommit, content: []byte("xx")},
		"refs/tags/v1.0.0":   {oid: tag, content: []byte("xx")},
	}
	for expectedRef, expectedObject := range expectedRefs {
		content, err := repo.ReadObject(ctx, expectedObject.oid)
		require.NoError(t, err)

		expectedRefs[expectedRef] = object{
			oid:     expectedObject.oid,
			content: content,
		}
	}

	objectsByRef := make(map[git.ReferenceName]object)
	for catfileObjectIter.Next() {
		result := catfileObjectIter.Result()

		objectData, err := io.ReadAll(result)
		require.NoError(t, err)
		require.Len(t, objectData, int(result.ObjectSize()))

		objectsByRef[git.ReferenceName(result.ObjectName)] = object{
			oid:     result.ObjectID(),
			content: objectData,
		}
	}
	require.NoError(t, catfileObjectIter.Err())
	require.Equal(t, expectedRefs, objectsByRef)
}
