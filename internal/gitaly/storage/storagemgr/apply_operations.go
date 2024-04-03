package storagemgr

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/keyvalue"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// applyOperations applies the operations from the log entry to the storage.
//
// ErrExists is ignored when creating directories and hard links. They could have been created
// during an earlier interrupted attempt to apply the log entry. Similarly ErrNotExist is ignored
// when removing directory entries. We can be stricter once log entry application becomes atomic
// through https://gitlab.com/gitlab-org/gitaly/-/issues/5765.
func applyOperations(sync func(string) error, storageRoot, walEntryDirectory string, entry *gitalypb.LogEntry, db keyvalue.Transactioner) error {
	wb := db.NewWriteBatch()
	defer wb.Cancel()

	// dirtyDirectories holds all directories that have been dirtied by the operations.
	// As files have already been synced to the disk when the log entry was written, we
	// only need to sync the operations on directories.
	dirtyDirectories := map[string]struct{}{}
	for _, wrappedOp := range entry.Operations {
		switch wrapper := wrappedOp.GetOperation().(type) {
		case *gitalypb.LogEntry_Operation_CreateHardLink_:
			op := wrapper.CreateHardLink

			basePath := walEntryDirectory
			if op.SourceInStorage {
				basePath = storageRoot
			}

			destinationPath := string(op.DestinationPath)
			if err := os.Link(
				filepath.Join(basePath, string(op.SourcePath)),
				filepath.Join(storageRoot, destinationPath),
			); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("link: %w", err)
			}

			// Sync the parent directory of the newly created directory entry.
			dirtyDirectories[filepath.Dir(destinationPath)] = struct{}{}
		case *gitalypb.LogEntry_Operation_CreateDirectory_:
			op := wrapper.CreateDirectory

			path := string(op.Path)
			if err := os.Mkdir(filepath.Join(storageRoot, path), fs.FileMode(op.Permissions)); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("mkdir: %w", err)
			}

			// Sync the newly created directory itself.
			dirtyDirectories[path] = struct{}{}
			// Sync the parent directory where the new directory's directory entry was created.
			dirtyDirectories[filepath.Dir(path)] = struct{}{}
		case *gitalypb.LogEntry_Operation_RemoveDirectoryEntry_:
			op := wrapper.RemoveDirectoryEntry

			path := string(op.Path)
			if err := os.Remove(filepath.Join(storageRoot, path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove: %w", err)
			}

			// Remove the dirty marker from the removed directory entry if it exists. There's
			// no need to sync it anymore as it doesn't exist.
			delete(dirtyDirectories, path)
			// Sync the parent directory where directory entry was removed from.
			dirtyDirectories[filepath.Dir(path)] = struct{}{}
		case *gitalypb.LogEntry_Operation_SetKey_:
			op := wrapper.SetKey

			if err := wb.Set(op.Key, op.Value); err != nil {
				return fmt.Errorf("set key: %w", err)
			}
		case *gitalypb.LogEntry_Operation_DeleteKey_:
			op := wrapper.DeleteKey

			if err := wb.Delete(op.Key); err != nil {
				return fmt.Errorf("delete key: %w", err)
			}
		default:
			return fmt.Errorf("unhandled operation type: %T", wrappedOp)
		}
	}

	// Sync all the dirty directories.
	for relativePath := range dirtyDirectories {
		if err := sync(filepath.Join(storageRoot, relativePath)); err != nil {
			return fmt.Errorf("sync: %w", err)
		}
	}

	if err := wb.Flush(); err != nil {
		return fmt.Errorf("flush write batch: %w", err)
	}

	return nil
}
