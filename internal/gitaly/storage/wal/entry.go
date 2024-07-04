package wal

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
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

func newIrregularFileStagedError(mode fs.FileMode) error {
	return fmt.Errorf("irregular file staged: %q", mode)
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
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("lstat: %w", err)
	}

	// Error out if there is an attempt to stage someting other than a regular file, ie.
	// symlink, directory or anything else.
	if !info.Mode().IsRegular() {
		return "", newIrregularFileStagedError(info.Mode().Type())
	}

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

// SetKey adds an operation to set a key with a value in the partition's key-value store.
func (e *Entry) SetKey(key, value []byte) {
	e.operations.setKey(key, value)
}

// DeleteKey adds an operation to delete a key from the partition's key-value store.
func (e *Entry) DeleteKey(key []byte) {
	e.operations.deleteKey(key)
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
	e.RecordDirectoryEntryRemoval(relativePath)

	if err := e.RecordFileCreation(filepath.Join(storageRoot, relativePath), relativePath); err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	return nil
}

// RecordDirectoryEntryRemoval records the removal of the file system object at the given path.
func (e *Entry) RecordDirectoryEntryRemoval(relativePath string) {
	e.operations.removeDirectoryEntry(relativePath)
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
		e.operations.createDirectory(currentDir)

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
			// Create the directories before descending in them so they exist when
			// we try to create the children.
			e.operations.createDirectory(relativePath)
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

			e.operations.createDirectory(destinationDir)
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
				e.operations.createHardLink(
					filepath.Join(sourceDir, objectFile.Name()),
					objectDestination,
					true,
				)
			}
		}
	}

	destinationAlternatesPath := filepath.Join(destinationObjectsDir, "info", "alternates")
	e.RecordDirectoryEntryRemoval(destinationAlternatesPath)

	return nil
}

// RecordReferenceUpdates performs reference updates against a snapshot repository and records the file system
// Git performed in the log entry.
//
// The file system operations are recorded by first recording the pre-image of the repository. This is done by
// going through each of the paths that could be modified in the transaction and recording whether they exist.
// If `refs/heads/main` is updated, we walk each of the components to see whether they existed before
// the reference transaction. For example, we'd check `refs`, `refs/heads` and `refs/heads/main` and record their
// existence in the pre-image.
//
// While going through the reference changes, we'll build two tree representations of the references being updated
// The first tree consists of operations that create a file, namely refererence updates and creations. The second
// tree consists of operations that may delete files, so reference deletions.
//
// With the pre-image state built, the reference changes are applied to the repository. We then figure out the
// changes by walking the two trees we built.
//
// File and directory creations are identified by walking from the refs directory downwards in the hierarchy. Each
// directory and file that was did not exist in the pre-image state is recorded as a creation.
//
// File and directory removals are identified by walking from the leaf nodes, the references, towards the root 'refs'.
// directory. Each file and directory that existed in the pre-image but doesn't exist anymore is recorded as having
// been removed.
//
// Each recorded operation is staged against the target relative path by omitting the snapshotPrefix. This is works as
// the snapshots retain the directory hierarchy of the original storage.
//
// Only the state changes inside `refs` directory are captured. To fully apply the reference transaction, changes to
// `packed-refs` should be captured separately.
//
// The method assumes the reference directory is in good shape and doesn't have hierarchies of empty directories. It
// thus doesn't record the deletion of such directories. If a directory such as `refs/heads/parent/child/subdir`
// exists, and a reference called `refs/heads/parent` is created, we don't handle the deletions of empty directories.
func (e *Entry) RecordReferenceUpdates(ctx context.Context, storageRoot, snapshotPrefix, relativePath string, refTX *gitalypb.LogEntry_ReferenceTransaction, objectHash git.ObjectHash, performReferenceUpdates func([]*gitalypb.LogEntry_ReferenceTransaction_Change) error) error {
	if len(refTX.GetChanges()) == 0 {
		return nil
	}

	creations := newReferenceTree()
	deletions := newReferenceTree()
	preImagePaths := map[string]struct{}{}
	for _, change := range refTX.GetChanges() {
		tree := creations
		if objectHash.IsZeroOID(git.ObjectID(change.NewOid)) {
			tree = deletions
		}

		referenceName := string(change.ReferenceName)
		if err := tree.Insert(referenceName); err != nil {
			return fmt.Errorf("insert into tree: %w", err)
		}

		for component, remainder, haveMore := "", referenceName, true; haveMore; {
			var newComponent string
			newComponent, remainder, haveMore = strings.Cut(remainder, "/")

			component = filepath.Join(component, newComponent)
			info, err := os.Stat(filepath.Join(storageRoot, snapshotPrefix, relativePath, component))
			if err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("stat for pre-image: %w", err)
				}

				// The path did not exist before applying the transaction.
				// None of its children exist either.
				break
			}

			if !info.IsDir() && haveMore {
				return updateref.FileDirectoryConflictError{
					ExistingReferenceName:    component,
					ConflictingReferenceName: filepath.Join(component, remainder),
				}
			}

			preImagePaths[component] = struct{}{}
		}
	}

	if err := performReferenceUpdates(refTX.Changes); err != nil {
		return fmt.Errorf("perform reference updates: %w", err)
	}

	// Record creations
	if err := creations.WalkPreOrder(func(path string, isDir bool) error {
		targetRelativePath := filepath.Join(relativePath, path)
		if isDir {
			if _, ok := preImagePaths[path]; ok {
				// The directory existed already in the pre-image and doesn't need to be recreated.
				return nil
			}

			e.operations.createDirectory(targetRelativePath)

			return nil
		}

		if _, ok := preImagePaths[path]; ok {
			// The file already existed in the pre-image. This means the file has been updated
			// and we should remove the previous file before we can link the updated one in place.
			e.operations.removeDirectoryEntry(targetRelativePath)
		}

		fileID, err := e.stageFile(filepath.Join(storageRoot, snapshotPrefix, relativePath, path))
		if err != nil {
			return fmt.Errorf("stage file: %w", err)
		}

		e.operations.createHardLink(fileID, targetRelativePath, false)

		return nil
	}); err != nil {
		return fmt.Errorf("walk creations pre-order: %w", err)
	}

	// Check if the deletion created directories. This can happen if Git has special cased a directory
	// to not be deleted, such as 'refs/heads', 'refs/tags' and 'refs/remotes'. Git may create the
	// directory to create a lock and leave it in place after the transaction.
	if err := deletions.WalkPreOrder(func(path string, isDir bool) error {
		info, err := os.Stat(filepath.Join(storageRoot, snapshotPrefix, relativePath, path))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("stat for dir permissions: %w", err)
		}

		if _, existedPreImaged := preImagePaths[path]; info != nil && !existedPreImaged {
			e.operations.createDirectory(filepath.Join(relativePath, path))
		}

		return nil
	}); err != nil {
		return fmt.Errorf("walk deletion directory creations pre-order: %w", err)
	}

	// Walk down from the leaves of reference deletions towards the root.
	if err := deletions.WalkPostOrder(func(path string, isDir bool) error {
		if _, ok := preImagePaths[path]; !ok {
			// If the path did not exist in the pre-image, no need to check further. The reference deletion
			// was targeting a reference in the packed-refs file, and not in the loose reference hierarchy.
			return nil
		}

		if _, err := os.Stat(filepath.Join(storageRoot, snapshotPrefix, relativePath, path)); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("stat for deletion: %w", err)
			}

			// The path doesn't exist in the post-image after the transaction was applied.
			e.operations.removeDirectoryEntry(filepath.Join(relativePath, path))
			return nil
		}

		// The path existed. Continue checking other paths whether they've been removed or not.
		return nil
	}); err != nil {
		return fmt.Errorf("walk deletions post-order: %w", err)
	}

	return nil
}
