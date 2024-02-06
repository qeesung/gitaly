package wal

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// Entry represents a write-ahead log entry.
type Entry struct {
	// fileIDSequence is a sequence used to generate unique IDs for files
	// staged as part of this entry.
	fileIDSequence uint64
	// operations are the operations this entry consists of.
	operations operations
	// stateDirectory is the directory where the entry's state is stored.
	stateDirectory string
}

// NewEntry returns a new Entry that can be used to construct a write-ahead
// log entry.
func NewEntry(stateDirectory string) *Entry {
	return &Entry{stateDirectory: stateDirectory}
}

// Operations returns the operations of the log entry.
func (e *Entry) Operations() []*gitalypb.LogEntry_Operation {
	return e.operations
}

// stageFile stages a file into the WAL entry by linking it in the state directory.
// The file's name in the state directory is returned and can be used to link the file
// subsequently into the correct location.
func (e *Entry) stageFile(path string) (string, error) {
	e.fileIDSequence++

	// We use base 36 as it produces shorter names and thus smaller log entries.
	// The file names within the log entry are not important as the manifest records the
	// actual name the file will be linked as.
	fileName := strconv.FormatUint(e.fileIDSequence, 36)
	if err := os.Link(path, filepath.Join(e.stateDirectory, fileName)); err != nil {
		return "", fmt.Errorf("link: %w", err)
	}

	return fileName, nil
}

// RecordDirectoryCreation records the operations to create a given directory in the storage and
// all of its children in to the storage. It logs flush operations for all created directories and
// the parent of the directory.
func (e *Entry) RecordDirectoryCreation(storageRoot, directoryRelativePath string) error {
	if err := walkDirectory(storageRoot, directoryRelativePath,
		func(relativePath string, dirEntry fs.DirEntry) error {
			info, err := dirEntry.Info()
			if err != nil {
				return fmt.Errorf("info: %w", err)
			}

			// Create the directories before descending in them so they exist when
			// we try to create the children.
			e.operations.createDirectory(relativePath, info.Mode().Perm())
			return nil
		},
		func(relativePath string, dirEntry fs.DirEntry) error {
			stagedPath, err := e.stageFile(filepath.Join(storageRoot, relativePath))
			if err != nil {
				return fmt.Errorf("stage file: %w", err)
			}

			// The parent directory has already been created so we can immediately create
			// the file.
			e.operations.createHardLink(stagedPath, relativePath, false)
			return nil
		},
		func(relativePath string, dirEntry fs.DirEntry) error {
			// Flush the directory once only after all of its children have been created.
			e.operations.flush(relativePath)
			return nil
		},
	); err != nil {
		return fmt.Errorf("walk directory: %w", err)
	}

	e.operations.flush(filepath.Dir(directoryRelativePath))

	return nil
}

// RecordDirectoryRemoval records a directory to be removed with all of its children. The deletion
// is recorded at the head of the log entry to perform it before any of the newly created state
// from the snapshot is applied.
func (e *Entry) RecordDirectoryRemoval(storageRoot, directoryRelativePath string) error {
	var ops operations
	if err := walkDirectory(storageRoot, directoryRelativePath,
		func(string, fs.DirEntry) error { return nil },
		func(relativePath string, dirEntry fs.DirEntry) error {
			ops.removeDirectoryEntry(relativePath)
			return nil
		},
		func(relativePath string, dirEntry fs.DirEntry) error {
			ops.removeDirectoryEntry(relativePath)
			return nil
		},
	); err != nil {
		return fmt.Errorf("walk directory: %w", err)
	}

	ops.flush(filepath.Dir(directoryRelativePath))

	e.operations = append(ops, e.operations...)

	return nil
}
