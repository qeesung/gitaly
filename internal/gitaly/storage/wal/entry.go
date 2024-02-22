package wal

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
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

// RecordFileCreation stages the file at the source and adds an operation to link it
// to the given destination relative path in the storage.
func (e *Entry) RecordFileCreation(sourceAbsolutePath string, relativePath string) error {
	stagedFile, err := e.stageFile(sourceAbsolutePath)
	if err != nil {
		return fmt.Errorf("stage file: %w", err)
	}

	e.operations.createHardLink(stagedFile, relativePath, false)
	return nil
}

// RecordFileUpdate records a file being updated. It stages operations to remove the old file,
// to place the new file in its place.
func (e *Entry) RecordFileUpdate(storageRoot, relativePath string) error {
	e.operations.removeDirectoryEntry(relativePath)

	if err := e.RecordFileCreation(filepath.Join(storageRoot, relativePath), relativePath); err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	return nil
}

// RecordRepositoryCreation records the creation of the repository itself and the directory
// hierarchy above it.
func (e *Entry) RecordRepositoryCreation(storageRoot, relativePath string) error {
	parentDir := filepath.Dir(relativePath)

	// If the repository is in the storage root, there are no parent directories to create.
	// Ignore the "." referring to the storage root.
	var dirComponents []string
	if parentDir != "." {
		dirComponents = strings.Split(parentDir, string(os.PathSeparator))
	}

	var previousParentDir string
	for _, dirComponent := range dirComponents {
		currentDir := filepath.Join(previousParentDir, dirComponent)
		e.operations.createDirectory(currentDir, perm.PrivateDir)

		previousParentDir = currentDir
	}

	// Log the repository itself.
	if err := e.recordDirectoryCreation(storageRoot, relativePath); err != nil {
		return fmt.Errorf("record directory creation: %w", err)
	}

	return nil
}

// RecordDirectoryCreation records the operations to create a given directory in the storage and
// all of its children in to the storage.
func (e *Entry) RecordDirectoryCreation(storageRoot, directoryRelativePath string) error {
	if err := e.recordDirectoryCreation(storageRoot, directoryRelativePath); err != nil {
		return err
	}

	return nil
}

func (e *Entry) recordDirectoryCreation(storageRoot, directoryRelativePath string) error {
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
			// The parent directory has already been created so we can immediately create
			// the file.
			if err := e.RecordFileCreation(filepath.Join(storageRoot, relativePath), relativePath); err != nil {
				return fmt.Errorf("create file: %w", err)
			}
			return nil
		},
		func(relativePath string, dirEntry fs.DirEntry) error {
			return nil
		},
	); err != nil {
		return fmt.Errorf("walk directory: %w", err)
	}

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

	e.operations = append(ops, e.operations...)

	return nil
}

// RecordAlternateUnlink records the operations to unlink the repository at the relative path
// from its alternate. All loose objects and packs are hard linked from the alternate to the
// repository and the `objects/info/alternate` file is removed.
func (e *Entry) RecordAlternateUnlink(storageRoot, relativePath, alternatePath string) error {
	destinationObjectsDir := filepath.Join(relativePath, "objects")
	sourceObjectsDir := filepath.Join(destinationObjectsDir, alternatePath)

	entries, err := os.ReadDir(filepath.Join(storageRoot, sourceObjectsDir))
	if err != nil {
		return fmt.Errorf("read alternate objects dir: %w", err)
	}

	var ops operations
	for _, subDir := range entries {
		if !subDir.IsDir() || !(len(subDir.Name()) == 2 || subDir.Name() == "pack") {
			// Only look in objects/<xx> and objects/pack for files.
			continue
		}

		sourceDir := filepath.Join(sourceObjectsDir, subDir.Name())

		objects, err := os.ReadDir(filepath.Join(storageRoot, sourceDir))
		if err != nil {
			return fmt.Errorf("read subdirectory: %w", err)
		}

		if len(objects) == 0 {
			// Don't create empty directories
			continue
		}

		destinationDir := filepath.Join(destinationObjectsDir, subDir.Name())

		// Create the destination directory if it doesn't yet exist.
		if _, err := os.Stat(filepath.Join(storageRoot, destinationDir)); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("stat: %w", err)
			}

			info, err := subDir.Info()
			if err != nil {
				return fmt.Errorf("subdirectory info: %w", err)
			}

			ops.createDirectory(destinationDir, info.Mode().Perm())
		}

		// Create all of the objects in the directory if they don't yet exist.
		for _, objectFile := range objects {
			if !objectFile.Type().IsRegular() {
				continue
			}

			objectDestination := filepath.Join(destinationDir, objectFile.Name())
			if _, err := os.Stat(filepath.Join(storageRoot, objectDestination)); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("stat: %w", err)
				}

				// The object doesn't yet exist, log the linking.
				ops.createHardLink(
					filepath.Join(sourceDir, objectFile.Name()),
					objectDestination,
					true,
				)
			}
		}
	}

	destinationAlternatesPath := filepath.Join(destinationObjectsDir, "info", "alternates")
	ops.removeDirectoryEntry(destinationAlternatesPath)

	e.operations = append(ops, e.operations...)

	return nil
}
