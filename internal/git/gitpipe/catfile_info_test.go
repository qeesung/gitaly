//go:build !gitaly_test_sha256

package gitpipe

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"google.golang.org/grpc/metadata"
)

const (
	lfsPointer1 = "0c304a93cb8430108629bbbcaa27db3343299bc0"
	lfsPointer2 = "f78df813119a79bfbe0442ab92540a61d3ab7ff3"
	lfsPointer3 = "bab31d249f78fba464d1b75799aad496cc07fa3b"
	lfsPointer4 = "125fcc9f6e33175cb278b9b2809154d2535fe19f"
)

func TestCatfileInfo(t *testing.T) {
	cfg := testcfg.Build(t)

	repoProto, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

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
				{OID: lfsPointer1},
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: lfsPointer1, Type: "blob", Size: 133}},
			},
		},
		{
			desc: "multiple blobs",
			revlistInputs: []RevisionResult{
				{OID: lfsPointer1},
				{OID: lfsPointer2},
				{OID: lfsPointer3},
				{OID: lfsPointer4},
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: lfsPointer1, Type: "blob", Size: 133}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: lfsPointer2, Type: "blob", Size: 127}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: lfsPointer3, Type: "blob", Size: 127}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: lfsPointer4, Type: "blob", Size: 129}},
			},
		},
		{
			desc: "object name",
			revlistInputs: []RevisionResult{
				{OID: "b95c0fad32f4361845f91d9ce4c1721b52b82793"},
				{OID: "93e123ac8a3e6a0b600953d7598af629dec7b735", ObjectName: []byte("branch-test.txt")},
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: "b95c0fad32f4361845f91d9ce4c1721b52b82793", Type: "tree", Size: 43}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: "93e123ac8a3e6a0b600953d7598af629dec7b735", Type: "blob", Size: 59}, ObjectName: []byte("branch-test.txt")},
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
				{OID: lfsPointer1},
				{OID: "invalidobjectid"},
				{OID: lfsPointer2},
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: lfsPointer1, Type: "blob", Size: 133}},
			},
			expectedErr: errors.New("retrieving object info for \"invalidobjectid\": object not found"),
		},
		{
			desc: "skip everything",
			revlistInputs: []RevisionResult{
				{OID: lfsPointer1},
				{OID: lfsPointer2},
			},
			opts: []CatfileInfoOption{
				WithSkipCatfileInfoResult(func(*catfile.ObjectInfo) bool { return true }),
			},
		},
		{
			desc: "skip one",
			revlistInputs: []RevisionResult{
				{OID: lfsPointer1},
				{OID: lfsPointer2},
			},
			opts: []CatfileInfoOption{
				WithSkipCatfileInfoResult(func(objectInfo *catfile.ObjectInfo) bool {
					return objectInfo.Oid == lfsPointer1
				}),
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: lfsPointer2, Type: "blob", Size: 127}},
			},
		},
		{
			desc: "skip nothing",
			revlistInputs: []RevisionResult{
				{OID: lfsPointer1},
				{OID: lfsPointer2},
			},
			opts: []CatfileInfoOption{
				WithSkipCatfileInfoResult(func(*catfile.ObjectInfo) bool { return false }),
			},
			expectedResults: []CatfileInfoResult{
				{ObjectInfo: &catfile.ObjectInfo{Oid: lfsPointer1, Type: "blob", Size: 133}},
				{ObjectInfo: &catfile.ObjectInfo{Oid: lfsPointer2, Type: "blob", Size: 127}},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

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
			{OID: lfsPointer1},
			{OID: lfsPointer1},
		}))
		require.NoError(t, err)

		require.True(t, it.Next())
		require.NoError(t, it.Err())
		require.Equal(t, CatfileInfoResult{
			ObjectInfo: &catfile.ObjectInfo{Oid: lfsPointer1, Type: "blob", Size: 133},
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
		inputCh <- git.ObjectID(lfsPointer1)

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
		inputCh <- git.ObjectID(lfsPointer1)

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
			{OID: lfsPointer1},
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
	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)

	repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	blob1 := gittest.WriteBlob(t, cfg, repoPath, []byte("foobar"))
	blob2 := gittest.WriteBlob(t, cfg, repoPath, []byte("barfoo"))
	tree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "foobar", Mode: "100644", OID: blob1},
	})
	commit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents())

	actualObjects := []CatfileInfoResult{
		{ObjectInfo: &catfile.ObjectInfo{Oid: blob1, Type: "blob", Size: 6}},
		{ObjectInfo: &catfile.ObjectInfo{Oid: blob2, Type: "blob", Size: 6}},
		{ObjectInfo: &catfile.ObjectInfo{Oid: tree, Type: "tree", Size: 34}},
		{ObjectInfo: &catfile.ObjectInfo{Oid: commit, Type: "commit", Size: 177}},
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
