package catfile

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestRequestQueue_ReadObject(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	oid := git.ObjectID(strings.Repeat("1", gittest.DefaultObjectHash.EncodedLen()))

	t.Run("ReadInfo on ReadObject queue", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, "#!/bin/sh\nread\n")

		require.PanicsWithValue(t, "object queue used to read object info", func() {
			_, _ = queue.ReadInfo()
		})
	})

	t.Run("read without request", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, "#!/bin/sh\nread\n")
		_, err := queue.ReadObject()
		require.Equal(t, fmt.Errorf("no outstanding request"), err)
	})

	t.Run("read on closed reader", func(t *testing.T) {
		reader, queue := newInterceptedQueue(ctx, t, "#!/bin/sh\nread\n")

		require.NoError(t, queue.RequestRevision("foo"))
		require.True(t, queue.isDirty())

		reader.close()
		require.True(t, queue.isDirty())

		_, err := queue.ReadObject()
		require.Equal(t, fmt.Errorf("cannot read object info: %w", fmt.Errorf("file already closed")), err)
	})

	t.Run("read with unconsumed object", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, fmt.Sprintf(`#!/bin/sh
			echo "%s commit 464"
		`, oid))

		// We queue two revisions...
		require.NoError(t, queue.RequestRevision("foo"))
		require.NoError(t, queue.RequestRevision("foo"))

		// .. and only unqueue one object. This object isn't read though, ...
		_, err := queue.ReadObject()
		require.NoError(t, err)

		// ... which means that trying to read the second object should fail now.
		_, err = queue.ReadObject()
		require.Equal(t, fmt.Errorf("current object has not been fully read"), err)

		require.True(t, queue.isDirty())
	})

	t.Run("read with invalid object header", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, `#!/bin/sh
			echo "something something"
		`)

		require.NoError(t, queue.RequestRevision("foo"))

		_, err := queue.ReadObject()
		require.Equal(t, fmt.Errorf("invalid info line: %q", "something something"), err)

		// The queue must be dirty when we failed due to an unexpected error.
		require.True(t, queue.isDirty())
	})

	t.Run("read with unexpected exit", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, `#!/bin/sh
			exit 1
		`)

		require.NoError(t, queue.RequestRevision("foo"))

		_, err := queue.ReadObject()
		require.Equal(t, fmt.Errorf("read info line: %w", io.EOF), err)

		// The queue must be dirty when we failed due to an unexpected error.
		require.True(t, queue.isDirty())
	})

	t.Run("read with missing object", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, fmt.Sprintf(`#!/bin/sh
			echo "%s missing"
		`, oid))

		require.NoError(t, queue.RequestRevision("foo"))

		_, err := queue.ReadObject()
		require.Equal(t, NotFoundError{error: fmt.Errorf("object not found")}, err)

		// The queue must be empty even if the object wasn't found: this is a graceful
		// failure that we should handle alright.
		require.False(t, queue.isDirty())
	})

	t.Run("read single object", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, fmt.Sprintf(`#!/bin/sh
			echo "%s blob 10"
			echo "1234567890"
		`, oid))

		require.NoError(t, queue.RequestRevision("foo"))
		require.True(t, queue.isDirty())

		object, err := queue.ReadObject()
		require.NoError(t, err)
		require.Equal(t, ObjectInfo{
			Oid:  oid,
			Type: "blob",
			Size: 10,
		}, object.ObjectInfo)

		data, err := io.ReadAll(object)
		require.NoError(t, err)
		require.Equal(t, "1234567890", string(data))

		require.False(t, queue.isDirty())
	})

	t.Run("read multiple objects", func(t *testing.T) {
		secondOID := git.ObjectID(strings.Repeat("2", gittest.DefaultObjectHash.EncodedLen()))

		_, queue := newInterceptedQueue(ctx, t, fmt.Sprintf(`#!/bin/sh
			echo "%s blob 10"
			echo "1234567890"
			echo "%s commit 10"
			echo "0987654321"
		`, oid, secondOID))

		require.NoError(t, queue.RequestRevision("foo"))
		require.NoError(t, queue.RequestRevision("foo"))
		require.True(t, queue.isDirty())

		for _, expectedObject := range []struct {
			info ObjectInfo
			data string
		}{
			{
				info: ObjectInfo{
					Oid:  oid,
					Type: "blob",
					Size: 10,
				},
				data: "1234567890",
			},
			{
				info: ObjectInfo{
					Oid:  secondOID,
					Type: "commit",
					Size: 10,
				},
				data: "0987654321",
			},
		} {
			require.True(t, queue.isDirty())

			object, err := queue.ReadObject()
			require.NoError(t, err)
			require.Equal(t, expectedObject.info, object.ObjectInfo)

			data, err := io.ReadAll(object)
			require.NoError(t, err)
			require.Equal(t, expectedObject.data, string(data))
		}

		require.False(t, queue.isDirty())
	})

	t.Run("truncated object", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, fmt.Sprintf(`#!/bin/sh
			echo "%s blob 10"
			printf "123"
		`, oid))

		require.NoError(t, queue.RequestRevision("foo"))
		require.True(t, queue.isDirty())

		object, err := queue.ReadObject()
		require.NoError(t, err)
		require.Equal(t, ObjectInfo{
			Oid:  oid,
			Type: "blob",
			Size: 10,
		}, object.ObjectInfo)

		// The first read will succeed and return the prefix.
		var buf [10]byte
		n, err := object.Read(buf[:])
		require.NoError(t, err)
		require.Equal(t, 3, n)
		require.Equal(t, "123", string(buf[:n]))

		// But the second read must fail and give us a hint that the read had been
		// truncated. Note that we explicitly expect to not see an io.EOF here,
		// which might indicate success to the caller.
		_, err = object.Read(buf[:])
		require.Equal(t, fmt.Errorf("discard newline: \"EOF\""), err)

		require.True(t, queue.isDirty())
	})
}

func TestRequestQueue_RequestRevision(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	oid := git.ObjectID(strings.Repeat("1", gittest.DefaultObjectHash.EncodedLen()))

	requireRevision := func(t *testing.T, queue *requestQueue, rev git.Revision) {
		object, err := queue.ReadObject()
		require.NoError(t, err)

		data, err := io.ReadAll(object)
		require.NoError(t, err)
		require.Equal(t, rev, git.Revision(data))
	}

	t.Run("requesting revision on closed queue", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, "#!/bin/sh")
		queue.close()

		require.Equal(t, fmt.Errorf("cannot request revision: %w", os.ErrClosed), queue.RequestRevision("foo"))
	})

	t.Run("requesting revision on closed process", func(t *testing.T) {
		process, queue := newInterceptedQueue(ctx, t, "#!/bin/sh")

		process.close()

		require.Equal(t, fmt.Errorf("cannot request revision: %w", os.ErrClosed), queue.RequestRevision("foo"))
	})

	t.Run("single request", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, fmt.Sprintf(`#!/bin/sh
			read revision
			echo "%s blob ${#revision}"
			echo "${revision}"
		`, oid))

		require.NoError(t, queue.RequestRevision("foo"))
		require.NoError(t, queue.Flush())

		requireRevision(t, queue, "foo")
	})

	t.Run("multiple request", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, fmt.Sprintf(`#!/bin/sh
			while read revision
			do
				echo "%s blob ${#revision}"
				echo "${revision}"
			done
		`, oid))

		require.NoError(t, queue.RequestRevision("foo"))
		require.NoError(t, queue.RequestRevision("bar"))
		require.NoError(t, queue.RequestRevision("baz"))
		require.NoError(t, queue.RequestRevision("qux"))
		require.NoError(t, queue.Flush())

		requireRevision(t, queue, "foo")
		requireRevision(t, queue, "bar")
		requireRevision(t, queue, "baz")
		requireRevision(t, queue, "qux")
	})

	t.Run("multiple request with intermediate flushing", func(t *testing.T) {
		_, queue := newInterceptedQueue(ctx, t, fmt.Sprintf(`#!/bin/sh
			while read revision
			do
				read flush
				if test "$flush" != "FLUSH"
				then
					echo "expected a flush"
					exit 1
				fi

				echo "%s blob ${#revision}"
				echo "${revision}"
			done
		`, oid))

		for _, revision := range []git.Revision{
			"foo",
			"bar",
			"foo",
			"qux",
		} {
			require.NoError(t, queue.RequestRevision(revision))
			require.NoError(t, queue.Flush())
			requireRevision(t, queue, revision)
		}
	})
}

func newInterceptedQueue(ctx context.Context, t *testing.T, script string) (ObjectReader, *requestQueue) {
	cfg := testcfg.Build(t)
	repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	commandFactory := gittest.NewInterceptingCommandFactory(ctx, t, cfg, func(execEnv git.ExecutionEnvironment) string {
		return script
	})
	repoExecutor := repoExecutor{
		GitRepo:       repo,
		gitCmdFactory: commandFactory,
	}

	reader, err := newObjectReader(ctx, &repoExecutor, nil)
	require.NoError(t, err)
	t.Cleanup(reader.close)

	queue, cleanup, err := reader.objectQueue(ctx, "trace")
	require.NoError(t, err)
	t.Cleanup(cleanup)

	return reader, queue
}
