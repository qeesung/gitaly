package storagemgr

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sync"

	"github.com/dgraph-io/badger/v4"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/keyvalue"
)

var (
	// errPartitionAssignmentNotFound is returned when attempting to access a
	// partition assignment in the database that doesn't yet exist.
	errPartitionAssignmentNotFound = errors.New("partition assignment not found")
	// errNoAlternate is returned when a repository has no alternate.
	errNoAlternate = errors.New("repository has no alternate")
	// errMultipleAlternates is returned when a repository has multiple alternates
	// configured.
	errMultipleAlternates = errors.New("repository has multiple alternates")
	// errAlternatePointsToSelf is returned when a repository's alternate points to the
	// repository itself.
	errAlternatePointsToSelf = errors.New("repository's alternate points to self")
	// errAlternateHasAlternate is returned when a repository's alternate itself has an
	// alternate listed.
	errAlternateHasAlternate = errors.New("repository's alternate has an alternate itself")
	// ErrRepositoriesAreInDifferentPartitions is returned when attempting to begin a transaction spanning
	// repositories that are in different partitions.
	ErrRepositoriesAreInDifferentPartitions = errors.New("repositories are in different partitions")
)

const prefixPartitionAssignment = "partition_assignment/"

// relativePathNotFoundError is raised when attempting to assign a relative path that does not exist into
// a partition.
type relativePathNotFoundError string

func (err relativePathNotFoundError) Error() string {
	return fmt.Sprintf("relative path not found: %q", string(err))
}

// partitionAssignmentTable records which partitions repositories are assigned into.
type partitionAssignmentTable struct{ db keyvalue.Store }

func newPartitionAssignmentTable(db keyvalue.Store) *partitionAssignmentTable {
	return &partitionAssignmentTable{db: db}
}

func (pt *partitionAssignmentTable) key(relativePath string) []byte {
	return []byte(fmt.Sprintf("%s%s", prefixPartitionAssignment, relativePath))
}

func (pt *partitionAssignmentTable) getPartitionID(relativePath string) (storage.PartitionID, error) {
	var id storage.PartitionID

	if err := pt.db.View(func(txn keyvalue.ReadWriter) error {
		item, err := txn.Get(pt.key(relativePath))
		if err != nil {
			if errors.Is(err, badger.ErrKeyNotFound) {
				return errPartitionAssignmentNotFound
			}

			return fmt.Errorf("get: %w", err)
		}

		return item.Value(func(value []byte) error {
			id.UnmarshalBinary(value)
			return nil
		})
	}); err != nil {
		return 0, fmt.Errorf("view: %w", err)
	}

	return id, nil
}

func (pt *partitionAssignmentTable) setPartitionID(relativePath string, id storage.PartitionID) error {
	wb := pt.db.NewWriteBatch()
	if err := wb.Set(pt.key(relativePath), id.MarshalBinary()); err != nil {
		return fmt.Errorf("set: %w", err)
	}

	if err := wb.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	return nil
}

// repositoryLock guards access to a single repository. It's used to ensure only a single
// goroutine is attempting to assign a partition at a time.
type repositoryLock struct {
	// semaphore is used to pass the lock to other goroutines waiting to get or assign the repository's
	// partition.
	semaphore chan struct{}
	// goroutines is the number of goroutines waiting to get or assign the repository's partition.
	goroutines int
}

// partitionAssigner manages assignment of repositories in to partitions.
type partitionAssigner struct {
	// mutex synchronizes access to repositoryLocks.
	mutex sync.Mutex
	// repositoryLocks holds per-repository locks. The key is a relative path of the repository.
	repositoryLocks map[string]*repositoryLock
	// idSequence is the sequence used to mint partition IDs.
	idSequence *badger.Sequence
	// partitionAssignmentTable contains the partition assignment records.
	partitionAssignmentTable *partitionAssignmentTable
	// storagePath is the path to the root directory of the storage the relative
	// paths are computed against.
	storagePath string
}

// newPartitionAssigner returns a new partitionAssigner. Close must be called on the
// returned instance to release acquired resources.
func newPartitionAssigner(db keyvalue.Store, storagePath string) (*partitionAssigner, error) {
	seq, err := db.GetSequence([]byte("partition_id_seq"), 100)
	if err != nil {
		return nil, fmt.Errorf("get sequence: %w", err)
	}

	return &partitionAssigner{
		repositoryLocks:          make(map[string]*repositoryLock),
		idSequence:               seq,
		partitionAssignmentTable: newPartitionAssignmentTable(db),
		storagePath:              storagePath,
	}, nil
}

func (pa *partitionAssigner) Close() error {
	return pa.idSequence.Release()
}

func (pa *partitionAssigner) allocatePartitionID() (storage.PartitionID, error) {
	// Start allocating partition IDs from 2:
	// - The default value, 0, refers to an invalid partition.
	// - Partition ID 1 is reserved for the storage's metadata partition.
	var id uint64
	for id < 2 {
		var err error
		id, err = pa.idSequence.Next()
		if err != nil {
			return 0, fmt.Errorf("next: %w", err)
		}
	}

	return storage.PartitionID(id), nil
}

// getPartitionID returns the partition ID of the repository. If the repository wasn't yet assigned into
// a partition, it will be assigned into one and the assignment stored. Further accesses return the stored
// partition ID. Repositories without an alternate go into their own partitions. Repositories with an alternate
// are assigned into the same partition as the alternate repository. The alternate is assigned into a partition
// if it hasn't yet been. The method is safe to call concurrently.
func (pa *partitionAssigner) getPartitionID(ctx context.Context, relativePath, partitionWithRelativePath string, isRepositoryCreation bool) (storage.PartitionID, error) {
	var partitionHint storage.PartitionID
	if partitionWithRelativePath != "" {
		var err error
		// See if the target repository itself is already in a partition. If so, we should assign the other repository
		// in the same partition if it is not yet partitioned.
		if partitionHint, err = pa.partitionAssignmentTable.getPartitionID(relativePath); err != nil {
			if !errors.Is(err, errPartitionAssignmentNotFound) {
				return 0, fmt.Errorf("get possible partition id: %w", err)
			}

			// There was no assignment.
			partitionHint = 0
		}

		// Get or assign the alternate into a partition. If the target repository was already assigned into a partition,
		// assign the alternate in the same partition. The hinted repository should always exist already as it is an object pool, or
		// the origin repo of a fork.
		if partitionHint, err = pa.getPartitionIDRecursive(ctx, partitionWithRelativePath, false, partitionHint, false); err != nil {
			return 0, fmt.Errorf("get additional relative path's partition ID: %w", err)
		}
	}

	// Get the repository's partition, or assign if it yet wasn't assigned, assign it with the alternate.
	ptnID, err := pa.getPartitionIDRecursive(ctx, relativePath, false, partitionHint, isRepositoryCreation)
	if err != nil {
		return 0, fmt.Errorf("get partition ID: %w", err)
	}

	if partitionHint != 0 && ptnID != partitionHint {
		return 0, ErrRepositoriesAreInDifferentPartitions
	}

	return ptnID, nil
}

func (pa *partitionAssigner) acquireRepositoryLock(ctx context.Context, relativePath string) (func(), error) {
	pa.mutex.Lock()
	// See if some other goroutine already locked the repository. If so, wait for it to complete.
	if lock, ok := pa.repositoryLocks[relativePath]; ok {
		// Register ourselves as a waiter.
		lock.goroutines++
		pa.mutex.Unlock()
		select {
		case <-lock.semaphore:
			// It's our turn now.
		case <-ctx.Done():
			pa.releaseRepositoryLock(relativePath, false)
			return nil, ctx.Err()
		}
	} else {
		// No other goroutine had locked the repository yet. Lock the repository so other goroutines
		// wait while we assign the repository a partition.
		pa.repositoryLocks[relativePath] = &repositoryLock{
			semaphore:  make(chan struct{}, 1),
			goroutines: 1,
		}
		pa.mutex.Unlock()
	}

	return func() { pa.releaseRepositoryLock(relativePath, true) }, nil
}

func (pa *partitionAssigner) releaseRepositoryLock(relativePath string, hadToken bool) {
	pa.mutex.Lock()
	defer pa.mutex.Unlock()

	lock := pa.repositoryLocks[relativePath]
	if hadToken {
		lock.semaphore <- struct{}{}
	}

	lock.goroutines--
	if lock.goroutines == 0 {
		delete(pa.repositoryLocks, relativePath)
	}
}

func (pa *partitionAssigner) getPartitionIDRecursive(ctx context.Context, relativePath string, recursiveCall bool, partitionHint storage.PartitionID, isRepositoryCreation bool) (storage.PartitionID, error) {
	// Check first whether the repository is already assigned into a partition. If so, just return the assignment.
	ptnID, err := pa.partitionAssignmentTable.getPartitionID(relativePath)
	if err != nil {
		if !errors.Is(err, errPartitionAssignmentNotFound) {
			return 0, fmt.Errorf("get partition: %w", err)
		}

		// Repository wasn't yet assigned into a partition. This is the slow path. Requests attempting
		// to get or assign a partition ID concurrently are serialized.

		releaseLock, err := pa.acquireRepositoryLock(ctx, relativePath)
		if err != nil {
			return 0, fmt.Errorf("acquire repository lock: %w", err)
		}

		defer releaseLock()

		// With the repository locked, check first whether someone else assigned it into a partition
		// while we weren't holding the lock between the first failed attempt getting the assignment
		// and locking the repository.
		ptnID, err = pa.partitionAssignmentTable.getPartitionID(relativePath)
		if !errors.Is(err, errPartitionAssignmentNotFound) {
			if err != nil {
				return 0, fmt.Errorf("recheck partition: %w", err)
			}

			// Some other goroutine assigned a partition between the failed attempt and locking the
			// repository.
			return ptnID, nil
		}

		// With the repository under lock, verify it is a Git directory before we assign it into a partition.
		// It's okay if the repository doesn't yet exist as this transaction may be about to create it.
		if err := storage.ValidateGitDirectory(filepath.Join(pa.storagePath, relativePath)); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				if !isRepositoryCreation {
					return 0, relativePathNotFoundError(relativePath)
				}

				// Repository creations are allowed to target non-existing repositories. They create the partition
				// where the repository is to be created.
			} else {
				return 0, fmt.Errorf("validate git directory: %w", err)
			}
		}

		ptnID, err = pa.assignPartitionID(ctx, relativePath, recursiveCall, partitionHint)
		if err != nil {
			return 0, fmt.Errorf("assign partition ID: %w", err)
		}
	}

	return ptnID, nil
}

func (pa *partitionAssigner) assignPartitionID(ctx context.Context, relativePath string, recursiveCall bool, partitionHint storage.PartitionID) (storage.PartitionID, error) {
	// Check if the repository has an alternate. If so, it needs to go into the same
	// partition with it.
	ptnID, err := pa.getAlternatePartitionID(ctx, relativePath, recursiveCall, partitionHint)
	if err != nil {
		if !errors.Is(err, errNoAlternate) {
			return 0, fmt.Errorf("get alternate partition ID: %w", err)
		}

		ptnID = partitionHint
		if ptnID == 0 {
			// The repository has no alternate. Unpooled repositories go into their own partitions.
			// Allocate a new partition ID for this repository.
			ptnID, err = pa.allocatePartitionID()
			if err != nil {
				return 0, fmt.Errorf("acquire partition id: %w", err)
			}
		}
	}

	if err := pa.partitionAssignmentTable.setPartitionID(relativePath, ptnID); err != nil {
		return 0, fmt.Errorf("set partition: %w", err)
	}

	return ptnID, nil
}

func (pa *partitionAssigner) getAlternatePartitionID(ctx context.Context, relativePath string, recursiveCall bool, partitionHint storage.PartitionID) (storage.PartitionID, error) {
	alternate, err := readAlternatesFile(filepath.Join(pa.storagePath, relativePath))
	if err != nil {
		return 0, fmt.Errorf("read alternates file: %w", err)
	}

	if recursiveCall {
		// recursive being true indicates we've arrived here through another repository's alternate.
		// Repositories in Gitaly should only have a single alternate that points to the repository's
		// pool. Chains of alternates are unexpected and could go arbitrarily long, so fail the operation.
		return 0, errAlternateHasAlternate
	}

	// The relative path should point somewhere within the same storage.
	alternateRelativePath, err := storage.ValidateRelativePath(
		pa.storagePath,
		// Take the relative path to the repository, not 'repository/objects'.
		filepath.Dir(
			// The path in alternates file points to the object directory of the alternate
			// repository. The path is relative to the repository's own object directory.
			filepath.Join(relativePath, "objects", alternate),
		),
	)
	if err != nil {
		return 0, fmt.Errorf("validate relative path: %w", err)
	}

	if alternateRelativePath == relativePath {
		// The alternate must not point to the repository itself. Not only is it non-sensical
		// but it would also cause a dead lock as the repository is locked during this call
		// already.
		return 0, errAlternatePointsToSelf
	}

	// Recursively get the alternate's partition ID or assign it one. This time
	// we set recursive to true to fail the operation if the alternate itself has an
	// alternate configured.
	ptnID, err := pa.getPartitionIDRecursive(ctx, alternateRelativePath, true, partitionHint, false)
	if err != nil {
		return 0, fmt.Errorf("get partition ID: %w", err)
	}

	return ptnID, nil
}

func readAlternatesFile(repositoryPath string) (string, error) {
	alternates, err := stats.ReadAlternatesFile(repositoryPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", errNoAlternate
		}

		return "", fmt.Errorf("read alternates file: %w", err)
	}

	if len(alternates) == 0 {
		return "", errNoAlternate
	} else if len(alternates) > 1 {
		// Repositories shouldn't have more than one alternate given they should only be
		// linked to a single pool at most.
		return "", errMultipleAlternates
	}

	return alternates[0], nil
}
