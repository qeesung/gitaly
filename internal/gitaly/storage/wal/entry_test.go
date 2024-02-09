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
	require.NoError(t, os.WriteFile(filepath.Join(storageRoot, "root-file"), []byte("root file"), perm.PrivateFile))
	setupTestDirectory(t, filepath.Join(storageRoot, firstLevelDir))
	setupTestDirectory(t, filepath.Join(storageRoot, secondLevelDir))

	for _, tc := range []struct {
		desc               string
		run                func(*testing.T, *Entry)
		expectedOperations operations
		expectedFiles      testhelper.DirectoryState
	}{
		{
			desc: "RecordFileCreation",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordFileCreation(
					filepath.Join(storageRoot, "root-file"),
					"test-dir/file-1",
				))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.flush("sentinel-op")
				ops.createHardLink("1", "test-dir/file-1", false)
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/":  {Mode: fs.ModeDir | perm.SharedDir},
				"/1": {Mode: perm.PrivateFile, Content: []byte("root file")},
			},
		},
		{
			desc: "RecordFlush",
			run: func(t *testing.T, entry *Entry) {
				entry.RecordFlush("test-dir")
				entry.RecordFlush("second-level/test-dir/file-1")
			},
			expectedOperations: func() operations {
				var ops operations
				ops.flush("sentinel-op")
				ops.flush("test-dir")
				ops.flush("second-level/test-dir/file-1")
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/": {Mode: fs.ModeDir | perm.SharedDir},
			},
		},
		{
			desc: "RecordFileUpdate on root level file",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordFileUpdate(storageRoot, "root-file"))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.flush("sentinel-op")
				ops.removeDirectoryEntry("root-file")
				ops.createHardLink("1", "root-file", false)
				ops.flush(".")
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/":  {Mode: fs.ModeDir | perm.SharedDir},
				"/1": {Mode: perm.PrivateFile, Content: []byte("root file")},
			},
		},
		{
			desc: "RecordFileUpdate on first level file",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordFileUpdate(storageRoot, filepath.Join(firstLevelDir, "file-1")))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.flush("sentinel-op")
				ops.removeDirectoryEntry("test-dir/file-1")
				ops.createHardLink("1", "test-dir/file-1", false)
				ops.flush("test-dir")
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/":  {Mode: fs.ModeDir | perm.SharedDir},
				"/1": {Mode: perm.PrivateExecutable, Content: []byte("file-1")},
			},
		},
		{
			desc: "RecordDirectoryCreation on first level directory",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordDirectoryCreation(storageRoot, firstLevelDir))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.flush("sentinel-op")
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
			expectedFiles: testhelper.DirectoryState{
				"/":  {Mode: fs.ModeDir | perm.SharedDir},
				"/1": {Mode: perm.PrivateExecutable, Content: []byte("file-1")},
				"/2": {Mode: perm.SharedFile, Content: []byte("file-2")},
				"/3": {Mode: perm.PrivateFile, Content: []byte("file-3")},
			},
		},
		{
			desc: "RecordDirectoryCreation on second level directory",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordDirectoryCreation(storageRoot, secondLevelDir))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.flush("sentinel-op")
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
			expectedFiles: testhelper.DirectoryState{
				"/":  {Mode: fs.ModeDir | perm.SharedDir},
				"/1": {Mode: perm.PrivateExecutable, Content: []byte("file-1")},
				"/2": {Mode: perm.SharedFile, Content: []byte("file-2")},
				"/3": {Mode: perm.PrivateFile, Content: []byte("file-3")},
			},
		},
		{
			desc: "RecordDirectoryRemoval on first level directory",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordDirectoryRemoval(storageRoot, firstLevelDir))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.removeDirectoryEntry("test-dir/file-1")
				ops.removeDirectoryEntry("test-dir/subdir-private/file-2")
				ops.removeDirectoryEntry("test-dir/subdir-private")
				ops.removeDirectoryEntry("test-dir/subdir-shared/file-3")
				ops.removeDirectoryEntry("test-dir/subdir-shared")
				ops.removeDirectoryEntry("test-dir")
				ops.flush(".")
				ops.flush("sentinel-op")
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/": {Mode: fs.ModeDir | perm.SharedDir},
			},
		},
		{
			desc: "RecordDirectoryRemoval on second level directory",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordDirectoryRemoval(storageRoot, secondLevelDir))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.removeDirectoryEntry("second-level/test-dir/file-1")
				ops.removeDirectoryEntry("second-level/test-dir/subdir-private/file-2")
				ops.removeDirectoryEntry("second-level/test-dir/subdir-private")
				ops.removeDirectoryEntry("second-level/test-dir/subdir-shared/file-3")
				ops.removeDirectoryEntry("second-level/test-dir/subdir-shared")
				ops.removeDirectoryEntry("second-level/test-dir")
				ops.flush("second-level")
				ops.flush("sentinel-op")
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/": {Mode: fs.ModeDir | perm.SharedDir},
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			stateDir := t.TempDir()
			entry := NewEntry(stateDir)
			entry.operations.flush("sentinel-op")

			tc.run(t, entry)

			testhelper.ProtoEqual(t, tc.expectedOperations, entry.operations)
			testhelper.RequireDirectoryState(t, stateDir, "", tc.expectedFiles)
		})
	}
}
