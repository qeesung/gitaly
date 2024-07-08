package safe_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestLockingDirectory(t *testing.T) {
	t.Parallel()

	lockname := "foo"

	t.Run("normal lifecycle", func(t *testing.T) {
		path := testhelper.TempDir(t)
		lockingDir, err := safe.NewLockingDirectory(path, lockname)
		require.NoError(t, err)
		require.NoError(t, lockingDir.Lock())
		secondLockingDir, err := safe.NewLockingDirectory(path, lockname)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			filepath.Join(path, "somefile"),
			[]byte("data"),
			perm.PrivateWriteOnceFile),
		)
		assert.ErrorIs(t, secondLockingDir.Lock(), safe.ErrFileAlreadyLocked)
		require.NoError(t, lockingDir.Unlock())
	})

	t.Run("multiple locks fail", func(t *testing.T) {
		path := testhelper.TempDir(t)
		lockingDir, err := safe.NewLockingDirectory(path, lockname)
		require.NoError(t, err)
		require.NoError(t, lockingDir.Lock())
		assert.Equal(
			t,
			errors.New("locking directory not lockable"),
			lockingDir.Lock(),
		)
	})

	t.Run("unlock without lock fails", func(t *testing.T) {
		path := testhelper.TempDir(t)
		lockingDir, err := safe.NewLockingDirectory(path, lockname)
		require.NoError(t, err)
		assert.Equal(
			t,
			errors.New("locking directory not locked"),
			lockingDir.Unlock(),
		)
	})

	t.Run("multiple unlocks fail", func(t *testing.T) {
		path := testhelper.TempDir(t)
		lockingDir, err := safe.NewLockingDirectory(path, lockname)
		require.NoError(t, err)
		require.NoError(t, lockingDir.Lock())
		require.NoError(t, lockingDir.Unlock())
		assert.Equal(
			t,
			errors.New("locking directory not locked"),
			lockingDir.Unlock(),
		)
	})

	t.Run("fails if directory is missing", func(t *testing.T) {
		path := testhelper.TempDir(t)
		require.NoError(t, os.RemoveAll(path))

		_, err := safe.NewLockingDirectory(path, lockname)
		assert.True(t, errors.Is(err, fs.ErrNotExist))
	})
}
