package catfile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestParseObjectInfo_success(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc               string
		input              string
		expectedErr        error
		expectedObjectInfo *ObjectInfo
	}{
		{
			desc:  "existing object",
			input: fmt.Sprintf("%s commit 222\n", gittest.DefaultObjectHash.EmptyTreeOID),
			expectedObjectInfo: &ObjectInfo{
				Oid:  gittest.DefaultObjectHash.EmptyTreeOID,
				Type: "commit",
				Size: 222,
			},
		},
		{
			desc:        "non existing object",
			input:       "bla missing\n",
			expectedErr: NotFoundError{fmt.Errorf("object not found")},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			reader := bufio.NewReader(strings.NewReader(tc.input))

			objectInfo, err := ParseObjectInfo(gittest.DefaultObjectHash, reader)
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedObjectInfo, objectInfo)
		})
	}
}

func TestParseObjectInfo_errors(t *testing.T) {
	t.Parallel()

	oid := gittest.DefaultObjectHash.EmptyTreeOID

	for _, tc := range []struct {
		desc        string
		input       string
		expectedErr error
	}{
		{
			desc:        "missing newline",
			input:       fmt.Sprintf("%s commit 222", oid),
			expectedErr: fmt.Errorf("read info line: %w", io.EOF),
		},
		{
			desc:        "too few words",
			input:       fmt.Sprintf("%s commit\n", oid),
			expectedErr: fmt.Errorf("invalid info line: %q", oid+" commit"),
		},
		{
			desc:        "too many words",
			input:       fmt.Sprintf("%s commit 222 bla\n", oid),
			expectedErr: fmt.Errorf("invalid info line: %q", oid+" commit 222 bla"),
		},
		{
			desc:        "invalid object hash",
			input:       "7c9373883988204e5a9f72c4 commit 222 bla\n",
			expectedErr: fmt.Errorf("invalid info line: %q", "7c9373883988204e5a9f72c4 commit 222 bla"),
		},
		{
			desc:  "parse object size",
			input: fmt.Sprintf("%s commit bla\n", oid),
			expectedErr: fmt.Errorf("parse object size: %w", &strconv.NumError{
				Func: "ParseInt",
				Num:  "bla",
				Err:  strconv.ErrSyntax,
			}),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			reader := bufio.NewReader(strings.NewReader(tc.input))

			_, err := ParseObjectInfo(gittest.DefaultObjectHash, reader)
			require.Equal(t, tc.expectedErr, err)
		})
	}
}

func TestObjectInfoReader(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

	commitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch("main"),
		gittest.WithMessage("commit message"),
		gittest.WithTreeEntries(gittest.TreeEntry{Path: "README", Mode: "100644", Content: "something"}),
	)
	gittest.WriteTag(t, cfg, repoPath, "v1.1.1", commitID.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	oiByRevision := make(map[string]*ObjectInfo)
	for _, revision := range []string{
		"refs/heads/main",
		"refs/heads/main^{tree}",
		"refs/heads/main:README",
		"refs/tags/v1.1.1",
	} {
		revParseOutput := gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", revision)
		objectID, err := gittest.DefaultObjectHash.FromHex(text.ChompBytes(revParseOutput))
		require.NoError(t, err)

		objectType := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "cat-file", "-t", revision))
		objectContents := gittest.Exec(t, cfg, "-C", repoPath, "cat-file", objectType, revision)

		oiByRevision[revision] = &ObjectInfo{
			Oid:  objectID,
			Type: objectType,
			Size: int64(len(objectContents)),
		}
	}

	for _, tc := range []struct {
		desc         string
		revision     git.Revision
		expectedErr  error
		expectedInfo *ObjectInfo
	}{
		{
			desc:         "commit by ref",
			revision:     "refs/heads/main",
			expectedInfo: oiByRevision["refs/heads/main"],
		},
		{
			desc:         "commit by ID",
			revision:     oiByRevision["refs/heads/main"].Oid.Revision(),
			expectedInfo: oiByRevision["refs/heads/main"],
		},
		{
			desc:         "tree",
			revision:     oiByRevision["refs/heads/main^{tree}"].Oid.Revision(),
			expectedInfo: oiByRevision["refs/heads/main^{tree}"],
		},
		{
			desc:         "blob",
			revision:     oiByRevision["refs/heads/main:README"].Oid.Revision(),
			expectedInfo: oiByRevision["refs/heads/main:README"],
		},
		{
			desc:         "tag",
			revision:     oiByRevision["refs/tags/v1.1.1"].Oid.Revision(),
			expectedInfo: oiByRevision["refs/tags/v1.1.1"],
		},
		{
			desc:        "nonexistent ref",
			revision:    "refs/heads/does-not-exist",
			expectedErr: NotFoundError{fmt.Errorf("object not found")},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			counter := prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"type"})

			reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), counter)
			require.NoError(t, err)

			require.Equal(t, float64(0), testutil.ToFloat64(counter.WithLabelValues("info")))

			info, err := reader.Info(ctx, tc.revision)
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedInfo, info)

			expectedRequests := 0
			if tc.expectedErr == nil {
				expectedRequests = 1
			}
			require.Equal(t, float64(expectedRequests), testutil.ToFloat64(counter.WithLabelValues("info")))

			// Verify that we do another request no matter whether the previous call
			// succeeded or failed.
			_, err = reader.Info(ctx, "refs/heads/main")
			require.NoError(t, err)

			require.Equal(t, float64(expectedRequests+1), testutil.ToFloat64(counter.WithLabelValues("info")))
		})
	}
}

func TestObjectInfoReader_queue(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

	blobOID := gittest.WriteBlob(t, cfg, repoPath, []byte("foobar"))
	blobInfo := ObjectInfo{
		Oid:  blobOID,
		Type: "blob",
		Size: int64(len("foobar")),
	}

	commitOID := gittest.WriteCommit(t, cfg, repoPath)
	commitInfo := ObjectInfo{
		Oid:  commitOID,
		Type: "commit",
		Size: func() int64 {
			if gittest.ObjectHashIsSHA256() {
				return 201
			}
			return 177
		}(),
	}

	t.Run("read single info", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestRevision(blobOID.Revision()))
		require.NoError(t, queue.Flush())

		info, err := queue.ReadInfo()
		require.NoError(t, err)
		require.Equal(t, &blobInfo, info)
	})

	t.Run("read multiple object infos", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		for oid, objectInfo := range map[git.ObjectID]ObjectInfo{
			blobOID:   blobInfo,
			commitOID: commitInfo,
		} {
			require.NoError(t, queue.RequestRevision(oid.Revision()))
			require.NoError(t, queue.Flush())

			info, err := queue.ReadInfo()
			require.NoError(t, err)
			require.Equal(t, &objectInfo, info)
		}
	})

	t.Run("request multiple object infos", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestRevision(blobOID.Revision()))
		require.NoError(t, queue.RequestRevision(commitOID.Revision()))
		require.NoError(t, queue.Flush())

		for _, expectedInfo := range []ObjectInfo{blobInfo, commitInfo} {
			info, err := queue.ReadInfo()
			require.NoError(t, err)
			require.Equal(t, &expectedInfo, info)
		}
	})

	t.Run("read without request", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		_, err = queue.ReadInfo()
		require.Equal(t, errors.New("no outstanding request"), err)
	})

	t.Run("flush with single request", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		// We flush once before and once after requesting the object such that we can be
		// sure that it doesn't impact which objects we can read.
		require.NoError(t, queue.Flush())
		require.NoError(t, queue.RequestRevision(blobOID.Revision()))
		require.NoError(t, queue.Flush())

		info, err := queue.ReadInfo()
		require.NoError(t, err)
		require.Equal(t, &blobInfo, info)
	})

	t.Run("flush with multiple requests", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		for i := 0; i < 10; i++ {
			require.NoError(t, queue.RequestRevision(blobOID.Revision()))
		}
		require.NoError(t, queue.Flush())

		for i := 0; i < 10; i++ {
			info, err := queue.ReadInfo()
			require.NoError(t, err)
			require.Equal(t, &blobInfo, info)
		}
	})

	t.Run("flush without request", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.Flush())

		_, err = queue.ReadInfo()
		require.Equal(t, errors.New("no outstanding request"), err)
	})

	t.Run("request invalid object info", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestRevision("does-not-exist"))
		require.NoError(t, queue.Flush())

		_, err = queue.ReadInfo()
		require.Equal(t, NotFoundError{errors.New("object not found")}, err)
	})

	t.Run("can continue reading after NotFoundError", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestRevision("does-not-exist"))
		require.NoError(t, queue.Flush())

		_, err = queue.ReadInfo()
		require.Equal(t, NotFoundError{errors.New("object not found")}, err)

		// Requesting another object info after the previous one has failed should continue
		// to work alright.
		require.NoError(t, queue.RequestRevision(blobOID.Revision()))
		require.NoError(t, queue.Flush())
		info, err := queue.ReadInfo()
		require.NoError(t, err)
		require.Equal(t, &blobInfo, info)
	})

	t.Run("requesting multiple queues fails", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		_, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		_, _, err = reader.infoQueue(ctx, "trace")
		require.Equal(t, errors.New("object info queue already in use"), err)

		// After calling cleanup we should be able to create an object queue again.
		cleanup()

		_, cleanup, err = reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()
	})

	t.Run("requesting object dirties reader", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.False(t, reader.isDirty())
		require.False(t, queue.isDirty())

		require.NoError(t, queue.RequestRevision(blobOID.Revision()))
		require.NoError(t, queue.Flush())

		require.True(t, reader.isDirty())
		require.True(t, queue.isDirty())

		_, err = queue.ReadInfo()
		require.NoError(t, err)

		require.False(t, reader.isDirty())
		require.False(t, queue.isDirty())
	})

	t.Run("closing queue blocks request", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		queue.close()

		require.True(t, reader.isClosed())
		require.True(t, queue.isClosed())

		require.Equal(t, fmt.Errorf("cannot request revision: %w", os.ErrClosed), queue.RequestRevision(blobOID.Revision()))
	})

	t.Run("closing queue blocks read", func(t *testing.T) {
		reader, err := newObjectInfoReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.infoQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		// Request the object before we close the queue.
		require.NoError(t, queue.RequestRevision(blobOID.Revision()))
		require.NoError(t, queue.Flush())

		queue.close()

		require.True(t, reader.isClosed())
		require.True(t, queue.isClosed())

		_, err = queue.ReadInfo()
		require.Equal(t, fmt.Errorf("cannot read object info: %w", os.ErrClosed), err)
	})
}
