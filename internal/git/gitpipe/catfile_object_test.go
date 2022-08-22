package gitpipe

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"google.golang.org/grpc/metadata"
)

func TestCatfileObject(t *testing.T) {
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
		desc              string
		catfileInfoInputs []CatfileInfoResult
		expectedResults   []CatfileObjectResult
		expectedErr       error
	}{
		{
			desc: "single blob",
			catfileInfoInputs: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 133}},
			},
			expectedResults: []CatfileObjectResult{
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 133}}},
			},
		},
		{
			desc: "multiple blobs",
			catfileInfoInputs: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 133}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 127}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobC, Type: "blob", Size: 127}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobD, Type: "blob", Size: 129}},
			},
			expectedResults: []CatfileObjectResult{
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 133}}},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 127}}},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobC, Type: "blob", Size: 127}}},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobD, Type: "blob", Size: 129}}},
			},
		},
		{
			desc: "revlist result with object names",
			catfileInfoInputs: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: treeID, Type: "tree", Size: hashDependentObjectSize(43, 55)}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: blobID, Type: "blob", Size: 59}, ObjectName: []byte("branch-test.txt")},
			},
			expectedResults: []CatfileObjectResult{
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: treeID, Type: "tree", Size: hashDependentObjectSize(43, 55)}}},
				{Object: &catfile.Object{ObjectInfo: catfile.ObjectInfo{Oid: blobID, Type: "blob", Size: 8}}, ObjectName: []byte("branch-test.txt")},
			},
		},
		{
			desc: "invalid object ID",
			catfileInfoInputs: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: "invalidobjectid", Type: "blob"}},
			},
			expectedErr: errors.New("requesting object: object not found"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			catfileCache := catfile.NewCache(cfg)
			defer catfileCache.Stop()

			objectReader, cancel, err := catfileCache.ObjectReader(ctx, repo)
			require.NoError(t, err)
			defer cancel()

			it, err := CatfileObject(ctx, objectReader, NewCatfileInfoIterator(ctx, tc.catfileInfoInputs))
			require.NoError(t, err)

			var results []CatfileObjectResult
			for it.Next() {
				result := it.Result()

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

		objectReader, objectReaderCancel, err := catfileCache.ObjectReader(ctx, repo)
		require.NoError(t, err)
		defer objectReaderCancel()

		it, err := CatfileObject(ctx, objectReader, NewCatfileInfoIterator(ctx, []CatfileInfoResult{
			{ObjectInfo: &catfile.ObjectInfo{Oid: blobA, Type: "blob", Size: 133}},
			{ObjectInfo: &catfile.ObjectInfo{Oid: blobB, Type: "blob", Size: 127}},
		}))
		require.NoError(t, err)

		require.True(t, it.Next())
		require.NoError(t, it.Err())
		require.Equal(t, blobA, it.Result().ObjectID())

		_, err = io.Copy(io.Discard, it.Result())
		require.NoError(t, err)

		cancel()

		require.False(t, it.Next())
		require.Equal(t, context.Canceled, it.Err())
		require.Equal(t, CatfileObjectResult{
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

		objectReader, objectReaderCancel, err := catfileCache.ObjectReader(ctx, repo)
		require.NoError(t, err)
		defer objectReaderCancel()

		inputIter, inputCh, nextCh := newChanObjectIterator()

		it, err := CatfileObject(ctx, objectReader, inputIter)
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
		_, ok := <-it.(*catfileObjectIterator).ch
		require.False(t, ok)

		// Sanity-check whether the iterator is in the expected state.
		require.False(t, it.Next())
		require.Equal(t, context.Canceled, it.Err())
	})

	t.Run("spawning two pipes fails", func(t *testing.T) {
		ctx := testhelper.Context(t)

		catfileCache := catfile.NewCache(cfg)
		defer catfileCache.Stop()

		objectReader, cancel, err := catfileCache.ObjectReader(ctx, repo)
		require.NoError(t, err)
		defer cancel()

		input := []RevisionResult{
			{OID: blobA},
		}

		it, err := CatfileObject(ctx, objectReader, NewRevisionIterator(ctx, input))
		require.NoError(t, err)

		// Reusing the queue is not allowed, so we should get an error here.
		_, err = CatfileObject(ctx, objectReader, NewRevisionIterator(ctx, input))
		require.Equal(t, fmt.Errorf("object queue already in use"), err)

		// We now consume all the input of the iterator.
		require.True(t, it.Next())
		_, err = io.Copy(io.Discard, it.Result())
		require.NoError(t, err)
		require.False(t, it.Next())

		// Which means that the queue should now be unused, so we can again use it.
		_, err = CatfileObject(ctx, objectReader, NewRevisionIterator(ctx, input))
		require.NoError(t, err)
	})
}
