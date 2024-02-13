package wal

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// walkCallback is a callback that that is invoked for each directory entry.
type walkCallbackFunc func(path string, dirEntry fs.DirEntry) error

// dirEntryFileInfo is an adapter that converts an `fs.FileMode` to conform
// to the `fs.DirEntry` interface.
type dirEntryFileInfo struct{ fs.FileInfo }

func (d dirEntryFileInfo) Info() (fs.FileInfo, error) {
	return d.FileInfo, nil
}

func (d dirEntryFileInfo) Type() fs.FileMode {
	return d.FileInfo.Mode().Type()
}

// walkDirectory recursively walks a directory at a given and invokes callback for each directory
// entry encountered. beforeChildren is invoked on directories before descending into their children. afterChildren
// is invoked on directories after having walked all their children. onFile is invoked immediately on encountering a
// file.
func walkDirectory(storageRoot, relativePath string, beforeChildren, onFile, afterChildren walkCallbackFunc) error {
	info, err := os.Stat(filepath.Join(storageRoot, relativePath))
	if err != nil {
		return fmt.Errorf("stat start point: %w", err)
	}

	return walkDirectoryRecursive(storageRoot, relativePath, dirEntryFileInfo{info}, beforeChildren, onFile, afterChildren)
}

func walkDirectoryRecursive(storageRoot, relativePath string, dirEntry fs.DirEntry, beforeChildren, onFile, afterChildren walkCallbackFunc) error {
	dirEntries, err := os.ReadDir(filepath.Join(storageRoot, relativePath))
	if err != nil {
		return fmt.Errorf("read dir: %w", err)
	}

	if err := beforeChildren(relativePath, dirEntry); err != nil {
		return fmt.Errorf("before children: %w", err)
	}

	for _, childEntry := range dirEntries {
		childRelativePath := filepath.Join(relativePath, childEntry.Name())
		if childEntry.IsDir() {
			if err := walkDirectory(storageRoot, childRelativePath, beforeChildren, onFile, afterChildren); err != nil {
				return fmt.Errorf("walk directory: %w", err)
			}

			continue
		}

		if err := onFile(childRelativePath, childEntry); err != nil {
			return fmt.Errorf("on file: %w", err)
		}
	}

	if err := afterChildren(relativePath, dirEntry); err != nil {
		return fmt.Errorf("after children: %w", err)
	}

	return nil
}
