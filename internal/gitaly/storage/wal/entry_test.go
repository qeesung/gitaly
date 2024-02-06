package wal

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func setupTestDirectory(t *testing.T, path string) {
	require.NoError(t, os.MkdirAll(path, perm.PrivateDir))
	require.NoError(t, os.WriteFile(filepath.Join(path, "file-1"), []byte("file-1"), perm.PrivateExecutable))
	privateSubDir := filepath.Join(filepath.Join(path, "subdir-private"))
	require.NoError(t, os.Mkdir(privateSubDir, perm.PrivateDir))
	require.NoError(t, os.WriteFile(filepath.Join(privateSubDir, "file-2"), []byte("file-2"), perm.SharedFile))
	sharedSubDir := filepath.Join(path, "subdir-shared")
	require.NoError(t, os.Mkdir(sharedSubDir, perm.SharedDir))
	require.NoError(t, os.WriteFile(filepath.Join(sharedSubDir, "file-3"), []byte("file-3"), perm.PrivateFile))
}

func TestEntry(t *testing.T) {
	t.Parallel()

	storageRoot := t.TempDir()

	firstLevelDir := "test-dir"
	secondLevelDir := "second-level/test-dir"
	setupTestDirectory(t, filepath.Join(storageRoot, firstLevelDir))
	setupTestDirectory(t, filepath.Join(storageRoot, secondLevelDir))

	t.Run("RecordDirectoryCreation", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			desc               string
			directory          string
			expectedOperations operations
		}{
			{
				desc:      "first level",
				directory: firstLevelDir,
				expectedOperations: func() operations {
					var ops operations
					ops.flush("first-op")
					ops.createDirectory("test-dir", perm.PrivateDir)
					ops.createHardLink("1", "test-dir/file-1", false)
					ops.createDirectory("test-dir/subdir-private", perm.PrivateDir)
					ops.createHardLink("2", "test-dir/subdir-private/file-2", false)
					ops.flush("test-dir/subdir-private")
					ops.createDirectory("test-dir/subdir-shared", perm.SharedDir)
					ops.createHardLink("3", "test-dir/subdir-shared/file-3", false)
					ops.flush("test-dir/subdir-shared")
					ops.flush("test-dir")
					ops.flush(".")
					return ops
				}(),
			},
			{
				desc:      "second level",
				directory: secondLevelDir,
				expectedOperations: func() operations {
					var ops operations
					ops.flush("first-op")
					ops.createDirectory("second-level/test-dir", perm.PrivateDir)
					ops.createHardLink("1", "second-level/test-dir/file-1", false)
					ops.createDirectory("second-level/test-dir/subdir-private", perm.PrivateDir)
					ops.createHardLink("2", "second-level/test-dir/subdir-private/file-2", false)
					ops.flush("second-level/test-dir/subdir-private")
					ops.createDirectory("second-level/test-dir/subdir-shared", perm.SharedDir)
					ops.createHardLink("3", "second-level/test-dir/subdir-shared/file-3", false)
					ops.flush("second-level/test-dir/subdir-shared")
					ops.flush("second-level/test-dir")
					ops.flush("second-level")
					return ops
				}(),
			},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()

				stateDir := t.TempDir()
				entry := NewEntry(stateDir)
				entry.operations.flush("first-op")

				require.NoError(t, entry.RecordDirectoryCreation(storageRoot, tc.directory))

				testhelper.ProtoEqual(t, tc.expectedOperations, entry.operations)
				testhelper.RequireDirectoryState(t, stateDir, "", testhelper.DirectoryState{
					"/":  {Mode: fs.ModeDir | perm.SharedDir},
					"/1": {Mode: perm.PrivateExecutable, Content: []byte("file-1")},
					"/2": {Mode: perm.SharedFile, Content: []byte("file-2")},
					"/3": {Mode: perm.PrivateFile, Content: []byte("file-3")},
				})
			})
		}
	})

	t.Run("RecordDirectoryDeletion", func(t *testing.T) {
		t.Parallel()

		for _, tc := range []struct {
			desc               string
			directory          string
			expectedOperations operations
		}{
			{
				desc:      "first level",
				directory: firstLevelDir,
				expectedOperations: func() operations {
					var ops operations
					ops.removeDirectoryEntry("test-dir/file-1")
					ops.removeDirectoryEntry("test-dir/subdir-private/file-2")
					ops.removeDirectoryEntry("test-dir/subdir-private")
					ops.removeDirectoryEntry("test-dir/subdir-shared/file-3")
					ops.removeDirectoryEntry("test-dir/subdir-shared")
					ops.removeDirectoryEntry("test-dir")
					ops.flush(".")
					ops.flush("first-op")
					return ops
				}(),
			},
			{
				desc:      "second level",
				directory: secondLevelDir,
				expectedOperations: func() operations {
					var ops operations
					ops.removeDirectoryEntry("second-level/test-dir/file-1")
					ops.removeDirectoryEntry("second-level/test-dir/subdir-private/file-2")
					ops.removeDirectoryEntry("second-level/test-dir/subdir-private")
					ops.removeDirectoryEntry("second-level/test-dir/subdir-shared/file-3")
					ops.removeDirectoryEntry("second-level/test-dir/subdir-shared")
					ops.removeDirectoryEntry("second-level/test-dir")
					ops.flush("second-level")
					ops.flush("first-op")
					return ops
				}(),
			},
		} {
			tc := tc
			t.Run(tc.desc, func(t *testing.T) {
				t.Parallel()

				stateDir := t.TempDir()
				entry := NewEntry(stateDir)
				entry.operations.flush("first-op")

				require.NoError(t, entry.RecordDirectoryRemoval(storageRoot, tc.directory))

				testhelper.ProtoEqual(t, tc.expectedOperations, entry.operations)
				testhelper.RequireDirectoryState(t, stateDir, "", testhelper.DirectoryState{
					"/": {Mode: fs.ModeDir | perm.SharedDir},
				})
			})
		}
	})
}
