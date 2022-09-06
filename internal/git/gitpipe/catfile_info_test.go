package gitpipe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"google.golang.org/grpc/metadata"
)

func TestCatfileInfo(t *testing.T) {
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

	blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("contents"))
	treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "branch-test.txt", Mode: "100644", OID: blobID},
	})

	for _, tc := range []struct {
		desc            string
		revlistInputs   []RevisionResult
		opts            []CatfileInfoOption
		expectedResults []CatfileInfoResult
		expectedErr     error
	}{
		{
			desc: "single blob",
			revlistInputs: []RevisionResult{
				{OID: blobA},
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 133}},
			},
		},
		{
			desc: "multiple blobs",
			revlistInputs: []RevisionResult{
				{OID: blobA},
				{OID: blobB},
				{OID: blobC},
				{OID: blobD},
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 133}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 127}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobC, Type: "blob", Size: 127}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobD, Type: "blob", Size: 129}},
			},
		},
		{
			desc: "object name",
			revlistInputs: []RevisionResult{
				{OID: treeID},
				{OID: blobID, ObjectName: []byte("branch-test.txt")},
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: treeID, Type: "tree", Size: hashDependentObjectSize(43, 55)}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobID, Type: "blob", Size: 8}, ObjectName: []byte("branch-test.txt")},
			},
		},
		{
			desc: "invalid object ID",
			revlistInputs: []RevisionResult{
				{OID: "invalidobjectid"},
			},
			expectedErr: errors.New("retrieving object info for \"invalidobjectid\": object not found"),
		},
		{
			desc: "mixed valid and invalid revision",
			revlistInputs: []RevisionResult{
				{OID: blobA},
				{OID: "invalidobjectid"},
				{OID: blobB},
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 133}},
			},
			expectedErr: errors.New("retrieving object info for \"invalidobjectid\": object not found"),
		},
		{
			desc: "skip everything",
			revlistInputs: []RevisionResult{
				{OID: blobA},
				{OID: blobB},
			},
			opts: []CatfileInfoOption{
				WithSkipCatfileInfoResult(func(*catfile.ObjectInfo) bool { return true }),
			},
		},
		{
			desc: "skip one",
			revlistInputs: []RevisionResult{
				{OID: blobA},
				{OID: blobB},
			},
			opts: []CatfileInfoOption{
				WithSkipCatfileInfoResult(func(objectInfo *catfile.ObjectInfo) bool {
					return objectInfo.Oid == blobA
				}),
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 127}},
			},
		},
		{
			desc: "skip nothing",
			revlistInputs: []RevisionResult{
				{OID: blobA},
				{OID: blobB},
			},
			opts: []CatfileInfoOption{
				WithSkipCatfileInfoResult(func(*catfile.ObjectInfo) bool { return false }),
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 133}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 127}},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			catfileCache := catfile.NewCache(cfg)
			defer catfileCache.Stop()

			objectInfoReader, cancel, err := catfileCache.ObjectInfoReader(ctx, repo)
			require.NoError(t, err)
			defer cancel()

			it, err := CatfileInfo(ctx, objectInfoReader, NewRevisionIterator(ctx, tc.revlistInputs), tc.opts...)
			require.NoError(t, err)

			var results []CatfileInfoResult
			for it.Next() {
				results = append(results, it.Result())
			}

			// We're converting the error here to a plain un-nested error such
			// that we don't have to replicate the complete error's structure.
			err = it.Err()
			if err != nil {
				err = errors.New(err.Error())
			}

			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedResults, results)
		})
	}

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(testhelper.Context(t))

		catfileCache := catfile.NewCache(cfg)
		defer catfileCache.Stop()

		objectInfoReader, objectInfoReaderCancel, err := catfileCache.ObjectInfoReader(ctx, repo)
		require.NoError(t, err)
		defer objectInfoReaderCancel()

		it, err := CatfileInfo(ctx, objectInfoReader, NewRevisionIterator(ctx, []RevisionResult{
			{OID: blobA},
			{OID: blobA},
		}))
		require.NoError(t, err)

		require.True(t, it.Next())
		require.NoError(t, it.Err())
		require.Equal(t, CatfileInfoResult{
			ObjectInfo: &catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 133},
		}, it.Result())

		cancel()

		require.False(t, it.Next())
		require.Equal(t, context.Canceled, it.Err())
		require.Equal(t, CatfileInfoResult{
			err: context.Canceled,
		}, it.Result())
	})

	t.Run("context cancellation with cached process", func(t *testing.T) {
		ctx, cancel := context.WithCancel(testhelper.Context(t))
		ctx = testhelper.MergeIncomingMetadata(ctx, metadata.Pairs(
			catfile.SessionIDField, "1",
		))

		catfileCache := catfile.NewCache(cfg)
		defer catfileCache.Stop()

		objectInfoReader, objectInfoReaderCancel, err := catfileCache.ObjectInfoReader(ctx, repo)
		require.NoError(t, err)
		defer objectInfoReaderCancel()

		inputIter, inputCh, nextCh := newChanObjectIterator()

		it, err := CatfileInfo(ctx, objectInfoReader, inputIter)
		require.NoError(t, err)

		// We request a single object from the catfile process. Because the request queue is
		// not flushed after every object this means that the request is currently
		// outstanding.
		<-nextCh
		inputCh <- blobA

		// Wait for the pipeline to request the next object.
		<-nextCh

		// We now cancel the context with the outstanding request. In the past, this used to
		// block the downstream consumer of the object data. This is because of two reasons:
		//
		// - When the process is being cached then cancellation of the context doesn't cause
		//   the process to get killed. So consequentially, the process would sit around
		//   waiting for input.
		// - We didn't flush the queue when the context was cancelled, so the buffered input
		//   never arrived at the process.
		cancel()

		// Now we queue another request that should cause the pipeline to fail.
		inputCh <- blobA

		// Verify whether we can receive any more objects via the iterator. This should
		// fail because the context got cancelled, but in any case it shouldn't block. Note
		// that we're forced to reach into the channel directly: `Next()` would return
		// `false` immediately because the context is cancelled.
		_, ok := <-it.(*catfileInfoIterator).ch
		require.False(t, ok)

		// Sanity-check whether the iterator is in the expected state.
		require.False(t, it.Next())
		require.Equal(t, context.Canceled, it.Err())
	})

	t.Run("spawning two pipes fails", func(t *testing.T) {
		ctx := testhelper.Context(t)

		catfileCache := catfile.NewCache(cfg)
		defer catfileCache.Stop()

		objectInfoReader, cancel, err := catfileCache.ObjectInfoReader(ctx, repo)
		require.NoError(t, err)
		defer cancel()

		input := []RevisionResult{
			{OID: blobA},
		}

		it, err := CatfileInfo(ctx, objectInfoReader, NewRevisionIterator(ctx, input))
		require.NoError(t, err)

		// Reusing the queue is not allowed, so we should get an error here.
		_, err = CatfileInfo(ctx, objectInfoReader, NewRevisionIterator(ctx, input))
		require.Equal(t, fmt.Errorf("object info queue already in use"), err)

		// We now consume all the input of the iterator.
		require.True(t, it.Next())
		require.False(t, it.Next())

		// Which means that the queue should now be unused, so we can again use it.
		_, err = CatfileInfo(ctx, objectInfoReader, NewRevisionIterator(ctx, input))
		require.NoError(t, err)
	})
}

func TestCatfileInfoAllObjects(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	blob1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foobar"))
	blob2 := gittest.WriteBlob(t, cfg, repoPath, []byte("barfoo"))
	tree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "foobar", Mode: "100644", OID: blob1},
	})
	commit := gittest.WriteCommit(t, cfg, repoPath)

	actualObjects := []CatfileInfoResult{
		{ObjectInfo: &catfile.ObjectInfo{Oid: blob1, Type: "blob", Size: 6}},
		{ObjectInfo: &catfile.ObjectInfo{Oid: blob2, Type: "blob", Size: 6}},
		{ObjectInfo: &catfile.ObjectInfo{Oid: tree, Type: "tree", Size: hashDependentObjectSize(34, 46)}},
		{ObjectInfo: &catfile.ObjectInfo{Oid: commit, Type: "commit", Size: hashDependentObjectSize(177, 201)}},
	}

	t.Run("successful", func(t *testing.T) {
		it := CatfileInfoAllObjects(ctx, repo)

		var results []CatfileInfoResult
		for it.Next() {
			results = append(results, it.Result())
		}
		require.NoError(t, it.Err())

		require.ElementsMatch(t, actualObjects, results)
	})

	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(testhelper.Context(t))

		it := CatfileInfoAllObjects(ctx, repo)

		require.True(t, it.Next())
		require.NoError(t, it.Err())
		require.Contains(t, actualObjects, it.Result())

		cancel()

		require.False(t, it.Next())
		require.Equal(t, context.Canceled, it.Err())
		require.Equal(t, CatfileInfoResult{
			err: context.Canceled,
		}, it.Result())
	})
}

func TestCatfileInfo_WithDiskUsageSize(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	tree1 := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{
			Path: "foobar",
			Mode: "100644",
			OID:  gittest.WriteBlob(t, cfg, repoPath, bytes.Repeat([]byte("a"), 100)),
		},
	})
	initialCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTree(tree1))

	tree2 := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{
			Path: "foobar",
			Mode: "100644",
			// take advantage of compression
			OID: gittest.WriteBlob(t, cfg, repoPath, append(bytes.Repeat([]byte("a"), 100),
				'\n',
				'b',
			)),
		},
	})
	gittest.WriteCommit(
		t,
		cfg,
		repoPath,
		gittest.WithTree(tree2),
		gittest.WithParents(initialCommitID),
		gittest.WithBranch("master"),
	)

	gittest.Exec(t, cfg, "-C", repoPath, "gc")

	itWithoutDiskSize := CatfileInfoAllObjects(ctx, repo)
	itWithDiskSize := CatfileInfoAllObjects(ctx, repo, WithDiskUsageSize())

	var totalWithoutDiskSize, totalWithDiskSize int64
	for itWithoutDiskSize.Next() {
		totalWithoutDiskSize += itWithoutDiskSize.Result().ObjectSize()
	}
	require.NoError(t, itWithoutDiskSize.Err())

	for itWithDiskSize.Next() {
		totalWithDiskSize += itWithDiskSize.Result().ObjectSize()
	}
	require.NoError(t, itWithDiskSize.Err())

	require.Less(t, totalWithDiskSize, totalWithoutDiskSize)
}
