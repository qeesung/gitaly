package storagemgr

import (
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/keyvalue"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/wal"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestApplyOperations(t *testing.T) {
	ctx := testhelper.Context(t)

	db, err := keyvalue.NewBadgerStore(testhelper.SharedLogger(t), t.TempDir())
	require.NoError(t, err)
	defer testhelper.MustClose(t, db)

	require.NoError(t, db.Update(func(tx keyvalue.ReadWriter) error {
		require.NoError(t, tx.Set([]byte("key-1"), []byte("value-1")))
		require.NoError(t, tx.Set([]byte("key-2"), []byte("value-2")))
		require.NoError(t, tx.Set([]byte("key-3"), []byte("value-3")))
		return nil
	}))

	snapshotRoot := filepath.Join(t.TempDir(), "snapshot")
	testhelper.CreateFS(t, snapshotRoot, fstest.MapFS{
		".":                                          {Mode: fs.ModeDir | perm.SharedDir},
		"parent":                                     {Mode: fs.ModeDir | perm.PrivateDir},
		"parent/relative-path":                       {Mode: fs.ModeDir | perm.SharedDir},
		"parent/relative-path/private-file":          {Mode: perm.PrivateFile, Data: []byte("private")},
		"parent/relative-path/shared-file":           {Mode: perm.SharedFile, Data: []byte("shared")},
		"parent/relative-path/empty-dir":             {Mode: fs.ModeDir | perm.PrivateDir},
		"parent/relative-path/removed-dir":           {Mode: fs.ModeDir | perm.PrivateDir},
		"parent/relative-path/dir-with-removed-file": {Mode: fs.ModeDir | perm.PrivateDir},
		"parent/relative-path/dir-with-removed-file/removed-file": {Mode: perm.PrivateFile, Data: []byte("removed")},
	})
	umask := testhelper.Umask()

	walEntryDirectory := t.TempDir()
	walEntry := wal.NewEntry(walEntryDirectory)
	require.NoError(t, walEntry.RecordRepositoryCreation(snapshotRoot, "parent/relative-path"))
	walEntry.RecordDirectoryEntryRemoval("parent/relative-path/dir-with-removed-file/removed-file")
	walEntry.RecordDirectoryEntryRemoval("parent/relative-path/removed-dir")
	walEntry.DeleteKey([]byte("key-2"))
	walEntry.SetKey([]byte("key-3"), []byte("value-3-updated"))
	walEntry.SetKey([]byte("key-4"), []byte("value-4"))

	storageRoot := t.TempDir()
	var syncedPaths []string
	require.NoError(t,
		applyOperations(
			func(path string) error {
				syncedPaths = append(syncedPaths, path)
				return nil
			},
			storageRoot,
			walEntryDirectory,
			&gitalypb.LogEntry{Operations: walEntry.Operations()},
			db,
		),
	)

	require.ElementsMatch(t, []string{
		storageRoot,
		filepath.Join(storageRoot, "parent"),
		filepath.Join(storageRoot, "parent/relative-path"),
		filepath.Join(storageRoot, "parent/relative-path/empty-dir"),
		filepath.Join(storageRoot, "parent/relative-path/dir-with-removed-file"),
	}, syncedPaths)
	testhelper.RequireDirectoryState(t, storageRoot, "", testhelper.DirectoryState{
		"/":                                  {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
		"/parent":                            {Mode: fs.ModeDir | perm.PrivateDir},
		"/parent/relative-path":              {Mode: fs.ModeDir | perm.SharedDir},
		"/parent/relative-path/private-file": {Mode: perm.PrivateFile, Content: []byte("private")},
		"/parent/relative-path/shared-file":  {Mode: perm.SharedFile, Content: []byte("shared")},
		"/parent/relative-path/empty-dir":    {Mode: fs.ModeDir | perm.PrivateDir},
		"/parent/relative-path/dir-with-removed-file": {Mode: fs.ModeDir | perm.PrivateDir},
	})

	RequireDatabase(t, ctx, db, DatabaseState{
		"key-1": "value-1",
		"key-3": "value-3-updated",
		"key-4": "value-4",
	})
}
