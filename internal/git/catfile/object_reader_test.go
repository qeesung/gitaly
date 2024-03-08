package catfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestObjectReader_reader(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	cmdExecutor := newRepoExecutor(t, cfg, repoProto)

	commitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch("main"),
		gittest.WithMessage("commit message"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "README", Mode: "100644", Content: "something"},
			gittest.TreeEntry{Path: "foo\nbar.txt", Mode: "100644", Content: "goo"},
		),
	)
	gittest.WriteTag(t, cfg, repoPath, "v1.1.1", commitID.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	oiByRevision := make(map[string]*ObjectInfo)
	contentByRevision := make(map[string][]byte)
	for _, revision := range []string{
		"refs/heads/main",
		"refs/heads/main^{tree}",
		"refs/heads/main:README",
		"refs/heads/main:foo\nbar.txt",
		"refs/tags/v1.1.1",
	} {
		revParseOutput := gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", revision)
		objectID, err := gittest.DefaultObjectHash.FromHex(text.ChompBytes(revParseOutput))
		require.NoError(t, err)

		objectType := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "cat-file", "-t", revision))
		objectContents := gittest.Exec(t, cfg, "-C", repoPath, "cat-file", objectType, revision)

		oiByRevision[revision] = &ObjectInfo{
			Oid:    objectID,
			Type:   objectType,
			Size:   int64(len(objectContents)),
			Format: gittest.DefaultObjectHash.Format,
		}
		contentByRevision[revision] = objectContents
	}

	for _, tc := range []struct {
		desc            string
		revision        git.Revision
		expectedErr     error
		expectedInfo    *ObjectInfo
		expectedContent []byte
	}{
		{
			desc:            "commit by ref",
			revision:        "refs/heads/main",
			expectedInfo:    oiByRevision["refs/heads/main"],
			expectedContent: contentByRevision["refs/heads/main"],
		},
		{
			desc:            "commit by ID",
			revision:        oiByRevision["refs/heads/main"].Oid.Revision(),
			expectedInfo:    oiByRevision["refs/heads/main"],
			expectedContent: contentByRevision["refs/heads/main"],
		},
		{
			desc:            "tree",
			revision:        oiByRevision["refs/heads/main^{tree}"].Oid.Revision(),
			expectedInfo:    oiByRevision["refs/heads/main^{tree}"],
			expectedContent: contentByRevision["refs/heads/main^{tree}"],
		},
		{
			desc:            "blob",
			revision:        oiByRevision["refs/heads/main:README"].Oid.Revision(),
			expectedInfo:    oiByRevision["refs/heads/main:README"],
			expectedContent: contentByRevision["refs/heads/main:README"],
		},
		{
			desc:            "tag",
			revision:        oiByRevision["refs/tags/v1.1.1"].Oid.Revision(),
			expectedInfo:    oiByRevision["refs/tags/v1.1.1"],
			expectedContent: contentByRevision["refs/tags/v1.1.1"],
		},
		{
			desc:            "existing blob with newline",
			revision:        oiByRevision["refs/heads/main:foo\nbar.txt"].Oid.Revision(),
			expectedInfo:    oiByRevision["refs/heads/main:foo\nbar.txt"],
			expectedContent: contentByRevision["refs/heads/main:foo\nbar.txt"],
		},
		{
			desc:        "blob with newline",
			revision:    oiByRevision["refs/tags/v1.1.1"].Oid.Revision() + git.Revision("foo\nbar.md"),
			expectedErr: NotFoundError{oiByRevision["refs/tags/v1.1.1"].Oid.String() + "foo\nbar.md"},
		},
		{
			desc:        "nonexistent ref",
			revision:    "refs/heads/does-not-exist",
			expectedErr: NotFoundError{"refs/heads/does-not-exist"},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			counter := prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"type"})

			reader, err := newObjectReader(ctx, cmdExecutor, counter)
			require.NoError(t, err)

			require.Equal(t, float64(0), testutil.ToFloat64(counter.WithLabelValues("info")))

			// Check for object info
			info, err := reader.Info(ctx, tc.revision)
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedInfo, info)

			// Check for object contents
			object, err := reader.Object(ctx, tc.revision)
			require.Equal(t, tc.expectedErr, err)
			if err == nil {
				data, err := io.ReadAll(object)
				require.NoError(t, err)
				require.Equal(t, tc.expectedContent, data)
			}

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

func TestObjectReader_object(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(featureflag.MailmapOptions).Run(t, testObjectReaderObject)
}

func testObjectReaderObject(t *testing.T, ctx context.Context) {
	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	cmdExecutor := newRepoExecutor(t, cfg, repoProto)

	commitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch("main"),
		gittest.WithMessage("commit message"),
		gittest.WithTreeEntries(gittest.TreeEntry{Path: "README", Mode: "100644", Content: "something"}),
	)
	gittest.WriteTag(t, cfg, repoPath, "v1.1.1", commitID.Revision(), gittest.WriteTagConfig{
		Message: "annotated tag",
	})

	t.Run("read fails when not consuming previous object", func(t *testing.T) {
		reader, err := newObjectReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		_, err = reader.Object(ctx, commitID.Revision())
		require.NoError(t, err)

		// We haven't yet consumed the previous object, so this must now fail.
		_, err = reader.Object(ctx, commitID.Revision())
		require.EqualError(t, err, "current object has not been fully read")
	})

	t.Run("read fails when partially consuming previous object", func(t *testing.T) {
		reader, err := newObjectReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		object, err := reader.Object(ctx, commitID.Revision())
		require.NoError(t, err)

		_, err = io.CopyN(io.Discard, object, 100)
		require.NoError(t, err)

		// We haven't yet consumed the previous object, so this must now fail.
		_, err = reader.Object(ctx, commitID.Revision())
		require.EqualError(t, err, "current object has not been fully read")
	})

	t.Run("read increments Prometheus counter", func(t *testing.T) {
		counter := prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"type"})

		reader, err := newObjectReader(ctx, cmdExecutor, counter)
		require.NoError(t, err)

		for objectType, revision := range map[string]git.Revision{
			"commit": "refs/heads/main",
			"tree":   "refs/heads/main^{tree}",
			"blob":   "refs/heads/main:README",
			"tag":    "refs/tags/v1.1.1",
		} {
			require.Equal(t, float64(0), testutil.ToFloat64(counter.WithLabelValues(objectType)))

			object, err := reader.Object(ctx, revision)
			require.NoError(t, err)

			require.Equal(t, float64(1), testutil.ToFloat64(counter.WithLabelValues(objectType)))

			_, err = io.Copy(io.Discard, object)
			require.NoError(t, err)
		}
	})

	t.Run("read existing object with mailmap", func(t *testing.T) {
		mailmapContents := "A U Thor <author@example.com> Scrooge McDuck <scrooge@mcduck.com>"

		commitID := gittest.WriteCommit(t, cfg, repoPath,
			gittest.WithTreeEntries(
				gittest.TreeEntry{Path: ".mailmap", Mode: "100644", Content: mailmapContents},
			),
			gittest.WithBranch("main"),
		)

		var commitContents []byte

		if featureflag.MailmapOptions.IsEnabled(ctx) {
			commitContents = gittest.Exec(t, cfg, "-C", repoPath, "cat-file",
				"--use-mailmap", "-p", commitID.String())
		} else {
			commitContents = gittest.Exec(t, cfg, "-C", repoPath, "cat-file",
				"-p", commitID.String())
		}

		reader, err := newObjectReader(ctx, newRepoExecutor(t, cfg, repoProto), nil)
		require.NoError(t, err)

		object, err := reader.Object(ctx, "refs/heads/main")
		require.NoError(t, err)

		data, err := io.ReadAll(object)
		require.NoError(t, err)
		require.Equal(t, commitContents, data)

		if featureflag.MailmapOptions.IsEnabled(ctx) {
			require.Contains(t, string(data), "A U Thor <author@example.com>")
		} else {
			require.Contains(t, string(data), "Scrooge McDuck <scrooge@mcduck.com>")
		}
	})
}

func TestObjectReader_queue(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	cmdExecutor := newRepoExecutor(t, cfg, repoProto)

	foobarBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("foobar"))
	barfooBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("barfoo"))

	t.Run("reader is dirty with acquired queue", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.False(t, queue.isDirty())
		require.True(t, reader.isDirty())
	})

	t.Run("read single object", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestObject(ctx, foobarBlob.Revision()))
		require.NoError(t, queue.Flush(ctx))

		object, err := queue.ReadObject(ctx)
		require.NoError(t, err)

		contents, err := io.ReadAll(object)
		require.NoError(t, err)
		require.Equal(t, "foobar", string(contents))
	})

	t.Run("read multiple objects", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		for blobID, blobContents := range map[git.ObjectID]string{
			foobarBlob: "foobar",
			barfooBlob: "barfoo",
		} {
			require.NoError(t, queue.RequestObject(ctx, blobID.Revision()))
			require.NoError(t, queue.Flush(ctx))

			object, err := queue.ReadObject(ctx)
			require.NoError(t, err)

			contents, err := io.ReadAll(object)
			require.NoError(t, err)
			require.Equal(t, blobContents, string(contents))
		}
	})

	t.Run("request multiple objects", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestObject(ctx, foobarBlob.Revision()))
		require.NoError(t, queue.RequestObject(ctx, barfooBlob.Revision()))
		require.NoError(t, queue.Flush(ctx))

		for _, expectedContents := range []string{"foobar", "barfoo"} {
			object, err := queue.ReadObject(ctx)
			require.NoError(t, err)

			contents, err := io.ReadAll(object)
			require.NoError(t, err)
			require.Equal(t, expectedContents, string(contents))
		}
	})

	t.Run("request multiple info", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestInfo(ctx, foobarBlob.Revision()))
		require.NoError(t, queue.RequestInfo(ctx, barfooBlob.Revision()))
		require.NoError(t, queue.Flush(ctx))

		for _, blob := range []git.ObjectID{foobarBlob, barfooBlob} {
			info, err := queue.ReadInfo(ctx)
			require.NoError(t, err)
			require.Equal(t, &ObjectInfo{
				Oid:    git.ObjectID(blob.Revision()),
				Type:   "blob",
				Size:   6,
				Format: gittest.DefaultObjectHash.Format,
			}, info)
		}
	})

	t.Run("request info and object together", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestObject(ctx, foobarBlob.Revision()))
		require.NoError(t, queue.RequestInfo(ctx, barfooBlob.Revision()))
		require.NoError(t, queue.Flush(ctx))

		object, err := queue.ReadObject(ctx)
		require.NoError(t, err)

		contents, err := io.ReadAll(object)
		require.NoError(t, err)
		require.Equal(t, "foobar", string(contents))

		info, err := queue.ReadInfo(ctx)
		require.NoError(t, err)
		require.Equal(t, &ObjectInfo{
			Oid:    git.ObjectID(barfooBlob.Revision()),
			Type:   "blob",
			Size:   int64(len("barfoo")),
			Format: gittest.DefaultObjectHash.Format,
		}, info)
	})

	t.Run("request info and object together, but swap the reading", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestObject(ctx, foobarBlob.Revision()))
		require.NoError(t, queue.RequestInfo(ctx, barfooBlob.Revision()))
		require.NoError(t, queue.Flush(ctx))

		// Info shouldn't fail since (info + content) = object, so we
		// should be allowed to read object from info.
		info, err := queue.ReadInfo(ctx)
		require.NoError(t, err)
		require.Equal(t, &ObjectInfo{
			Oid:    git.ObjectID(foobarBlob.Revision()),
			Type:   "blob",
			Size:   int64(len("foobar")),
			Format: gittest.DefaultObjectHash.Format,
		}, info)

		// But reading an object would fail, since we have leftover data from the
		// previous read (which was supposed to be an object but we only read the info
		// section).
		_, err = queue.ReadObject(ctx)
		require.Equal(t, errors.New("invalid info line: \"foobar\""), err)
	})

	t.Run("read without request", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		_, err = queue.ReadObject(ctx)
		require.Equal(t, errors.New("no outstanding request"), err)
	})

	t.Run("flush with single request", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		// We flush once before and once after requesting the object such that we can be
		// sure that it doesn't impact which objects we can read.
		require.NoError(t, queue.Flush(ctx))
		require.NoError(t, queue.RequestObject(ctx, foobarBlob.Revision()))
		require.NoError(t, queue.Flush(ctx))

		object, err := queue.ReadObject(ctx)
		require.NoError(t, err)

		contents, err := io.ReadAll(object)
		require.NoError(t, err)
		require.Equal(t, "foobar", string(contents))
	})

	t.Run("flush with multiple requests", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		for i := 0; i < 10; i++ {
			require.NoError(t, queue.RequestObject(ctx, foobarBlob.Revision()))
		}
		require.NoError(t, queue.Flush(ctx))

		for i := 0; i < 10; i++ {
			object, err := queue.ReadObject(ctx)
			require.NoError(t, err)

			contents, err := io.ReadAll(object)
			require.NoError(t, err)
			require.Equal(t, "foobar", string(contents))
		}
	})

	t.Run("flush without request", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.Flush(ctx))

		_, err = queue.ReadObject(ctx)
		require.Equal(t, errors.New("no outstanding request"), err)
	})

	t.Run("request invalid object", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestObject(ctx, "does-not-exist"))
		require.NoError(t, queue.Flush(ctx))

		_, err = queue.ReadObject(ctx)
		require.Equal(t, NotFoundError{"does-not-exist"}, err)
	})

	t.Run("can continue reading after NotFoundError", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestObject(ctx, "does-not-exist"))
		require.NoError(t, queue.Flush(ctx))

		_, err = queue.ReadObject(ctx)
		require.Equal(t, NotFoundError{"does-not-exist"}, err)

		// Requesting another object after the previous one has failed should continue to
		// work alright.
		require.NoError(t, queue.RequestObject(ctx, foobarBlob.Revision()))
		require.NoError(t, queue.Flush(ctx))
		object, err := queue.ReadObject(ctx)
		require.NoError(t, err)

		contents, err := io.ReadAll(object)
		require.NoError(t, err)
		require.Equal(t, "foobar", string(contents))
	})

	t.Run("requesting multiple queues fails", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		_, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		_, _, err = reader.objectQueue(ctx, "trace")
		require.Equal(t, errors.New("object queue already in use"), err)

		// After calling cleanup we should be able to create an object queue again.
		cleanup()

		_, cleanup, err = reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()
	})

	t.Run("requesting object dirties reader", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		require.False(t, reader.isDirty())

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.True(t, reader.isDirty())
		require.False(t, queue.isDirty())

		require.NoError(t, queue.RequestObject(ctx, foobarBlob.Revision()))
		require.NoError(t, queue.Flush(ctx))

		require.True(t, reader.isDirty())
		require.True(t, queue.isDirty())

		object, err := queue.ReadObject(ctx)
		require.NoError(t, err)

		// The object has not been consumed yet, so the reader must still be dirty.
		require.True(t, reader.isDirty())
		require.True(t, queue.isDirty())

		_, err = io.ReadAll(object)
		require.NoError(t, err)

		require.True(t, reader.isDirty())
		require.False(t, queue.isDirty())

		cleanup()

		require.False(t, reader.isDirty())
		require.False(t, queue.isDirty())
	})

	t.Run("closing queue blocks request", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		queue.close()

		require.True(t, reader.isClosed())
		require.True(t, queue.isClosed())

		require.Equal(t, fmt.Errorf("cannot request revision: %w", os.ErrClosed), queue.RequestObject(ctx, foobarBlob.Revision()))
	})

	t.Run("closing queue blocks read", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		// Request the object before we close the queue.
		require.NoError(t, queue.RequestObject(ctx, foobarBlob.Revision()))
		require.NoError(t, queue.Flush(ctx))

		queue.close()

		require.True(t, reader.isClosed())
		require.True(t, queue.isClosed())

		_, err = queue.ReadObject(ctx)
		require.Equal(t, fmt.Errorf("cannot read object info: %w", os.ErrClosed), err)
	})

	t.Run("closing queue blocks consuming", func(t *testing.T) {
		reader, err := newObjectReader(ctx, cmdExecutor, nil)
		require.NoError(t, err)

		queue, cleanup, err := reader.objectQueue(ctx, "trace")
		require.NoError(t, err)
		defer cleanup()

		require.NoError(t, queue.RequestObject(ctx, foobarBlob.Revision()))
		require.NoError(t, queue.Flush(ctx))

		// Read the object header before closing.
		object, err := queue.ReadObject(ctx)
		require.NoError(t, err)

		queue.close()

		require.True(t, reader.isClosed())
		require.True(t, queue.isClosed())

		_, err = io.ReadAll(object)
		require.Equal(t, os.ErrClosed, err)
	})
}
