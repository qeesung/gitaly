//go:build !gitaly_test_sha256

package safe_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestLockingDirectory(t *testing.T) {
	t.Parallel()

	t.Run("normal lifecycle", func(t *testing.T) {
		path := testhelper.TempDir(t)
		lockingDir, err := safe.NewLockingDirectory(path)
		require.NoError(t, err)
		require.NoError(t, lockingDir.Lock())
		secondLockingDir, err := safe.NewLockingDirectory(path)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(
			filepath.Join(path, "somefile"),
			[]byte("data"),
			0o644),
		)
		assert.ErrorIs(t, secondLockingDir.Lock(), safe.ErrFileAlreadyLocked)
		require.NoError(t, lockingDir.Unlock())
	})

	t.Run("multiple locks fail", func(t *testing.T) {
		path := testhelper.TempDir(t)
		lockingDir, err := safe.NewLockingDirectory(path)
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
		lockingDir, err := safe.NewLockingDirectory(path)
		require.NoError(t, err)
		assert.Equal(
			t,
			errors.New("locking directory not locked"),
			lockingDir.Unlock(),
		)
	})

	t.Run("multiple unlocks fail", func(t *testing.T) {
		path := testhelper.TempDir(t)
		lockingDir, err := safe.NewLockingDirectory(path)
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

		_, err := safe.NewLockingDirectory(path)
		assert.True(t, errors.Is(err, fs.ErrNotExist))
	})
}
