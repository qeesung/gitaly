package snapshot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
)

// sharedSnapshot contains the synchronization state related to
// snapshot sharing.
type sharedSnapshot struct {
	// referenceCount tracks the number of users this shared
	// snapshot has. Once there are no users left, the snapshot
	// will be cleaned up.
	referenceCount int
	// ready is closed when the goroutine performing the snapshotting
	// has finished and has populated snapshotErr and filesystem fields.
	ready chan struct{}
	// snapshotErr describes the possible error that has occurred while
	// creating the snapshot.
	snapshotErr error
	// filesystem is the filesystem snapshot to access.
	filesystem *FileSystem
}

// Manager creates file system snapshots from a given directory hierarchy.
type Manager struct {
	// nextDirectory is used to allocate unique directories
	// for snaphots that are about to be created.
	nextDirectory atomic.Uint64

	// storageDir is an absolute path to the root of the storage.
	storageDir string
	// workingDir is an absolute path to where the snapshots should
	// be created.
	workingDir string
	// currentLSN is the current LSN applied to the file system.
	currentLSN storage.LSN
	// metrics contains the metrics the manager gathers.
	metrics Metrics

	// mutex covers access to sharedSnapshots.
	mutex sync.Mutex
	// sharedSnapshots tracks all of the open shared snapshots.
	// - The first level key is the LSN when the snapshot was taken.
	//   Snapshots are only shared if the currentLSN matches the LSN
	//   when the snapshot was taken.
	// - The second level key is an ordered list of relative paths
	//   that are being snapshotted. Snapshots are only shared if they
	//   are accessing the same set of relative paths.
	sharedSnapshots map[storage.LSN]map[string]*sharedSnapshot
}

// NewManager returns a new Manager that creates snapshots from storageDir into workingDir.
func NewManager(storageDir, workingDir string, metrics Metrics) *Manager {
	return &Manager{
		storageDir:      storageDir,
		workingDir:      workingDir,
		sharedSnapshots: make(map[storage.LSN]map[string]*sharedSnapshot),
		metrics:         metrics,
	}
}

// SetLSN sets the current LSN. Snaphots returned by GetSnapshot always cover the latest LSN
// that was set prior to calling GetSnapshot.
//
// SetLSN must not be called concurrently with GetSnapshot.
func (mgr *Manager) SetLSN(currentLSN storage.LSN) {
	mgr.currentLSN = currentLSN
}

// GetSnapshot returns a file system snapshot. If exclusive is set, the snapshot is a new one and not shared with
// any other caller. If exclusive is not set, the snaphot is a shared one and may be shared with other callers.
//
// GetSnapshot is safe to call concurrently with itself. The caller is responsible for ensuring the state of the
// snapshotted file system is not modified while the snapshot is taken.
func (mgr *Manager) GetSnapshot(ctx context.Context, relativePaths []string, exclusive bool) (_ *FileSystem, _ func() error, returnedErr error) {
	if exclusive {
		mgr.metrics.createdExclusiveSnapshotTotal.Inc()
		filesystem, err := mgr.newSnapshot(ctx, relativePaths)
		if err != nil {
			return nil, nil, fmt.Errorf("new exclusive snapshot: %w", err)
		}

		return filesystem, func() error {
			mgr.metrics.destroyedExclusiveSnapshotTotal.Inc()
			// Exclusive snapshots are not shared, so it can be removed as soon
			// as the user finishes with it.
			if err := os.RemoveAll(filesystem.Root()); err != nil {
				return fmt.Errorf("remove exclusive snapshot: %w", err)
			}

			return nil
		}, nil
	}

	// This is a shared snapshot.
	key := mgr.key(relativePaths)

	mgr.mutex.Lock()
	lsn := mgr.currentLSN
	if mgr.sharedSnapshots[lsn] == nil {
		mgr.sharedSnapshots[lsn] = make(map[string]*sharedSnapshot)
	}

	wrapper, ok := mgr.sharedSnapshots[lsn][key]
	if !ok {
		// If there isn't a snapshot yet, create the synchronization
		// state to ensure other goroutines won't concurrently create
		// another snapshot, and instead wait for us to take the
		// snapshot.
		//
		// Once the synchronization state is in place, we'll release
		// the lock to allow other repositories to be concurrently
		// snapshotted. The goroutines waiting for this snapshot
		// wait on the `ready` channel.
		wrapper = &sharedSnapshot{ready: make(chan struct{})}
		mgr.sharedSnapshots[lsn][key] = wrapper
	}
	// Increment the reference counter to record that we are using
	// the snapshot.
	wrapper.referenceCount++
	mgr.mutex.Unlock()

	cleanup := func() error {
		removeSnapshot := false

		mgr.mutex.Lock()
		wrapper.referenceCount--
		if wrapper.referenceCount == 0 {
			// If we were the last user of the snapshot, remove it.
			delete(mgr.sharedSnapshots[lsn], key)

			// If this was the last snapshot on the given LSN, also
			// clear the LSNs entry.
			if len(mgr.sharedSnapshots[lsn]) == 0 {
				delete(mgr.sharedSnapshots, lsn)
			}

			// We need to remove the file system state of the snapshot
			// only if it was successfully created.
			removeSnapshot = wrapper.filesystem != nil
		}
		mgr.mutex.Unlock()

		if removeSnapshot {
			mgr.metrics.destroyedSharedSnapshotTotal.Inc()
			if err := os.RemoveAll(wrapper.filesystem.Root()); err != nil {
				return fmt.Errorf("remove shared snapshot: %w", err)
			}
		}

		return nil
	}
	defer func() {
		if returnedErr != nil {
			if err := cleanup(); err != nil {
				returnedErr = errors.Join(returnedErr, fmt.Errorf("clean failed snapshot: %w", err))
			}
		}
	}()

	if !ok {
		mgr.metrics.createdSharedSnapshotTotal.Inc()
		// If there was no existing snapshot, we need to create it.
		wrapper.filesystem, wrapper.snapshotErr = mgr.newSnapshot(ctx, relativePaths)
		// Other goroutines are waiting on the ready channel for us to finish the snapshotting
		// so close it to signal the process is finished.
		close(wrapper.ready)
	} else {
		mgr.metrics.reusedSharedSnapshotTotal.Inc()
	}

	select {
	case <-wrapper.ready:
		if wrapper.snapshotErr != nil {
			return nil, nil, fmt.Errorf("new shared snapshot: %w", wrapper.snapshotErr)
		}
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	}

	return wrapper.filesystem, cleanup, nil
}

func (mgr *Manager) newSnapshot(ctx context.Context, relativePaths []string) (_ *FileSystem, returnedErr error) {
	destinationPath := filepath.Join(mgr.workingDir, strconv.FormatUint(mgr.nextDirectory.Add(1), 36))

	defer func() {
		if returnedErr != nil {
			if err := os.RemoveAll(destinationPath); err != nil {
				returnedErr = errors.Join(returnedErr, fmt.Errorf("remove failed snapshot: %w", err))
			}
		}
	}()

	return newSnapshot(ctx,
		mgr.storageDir,
		destinationPath,
		relativePaths,
	)
}

func (mgr *Manager) key(relativePaths []string) string {
	// Sort the relative paths to ensure their ordering change
	// the key.
	slices.Sort(relativePaths)
	return strings.Join(relativePaths, ",")
}
