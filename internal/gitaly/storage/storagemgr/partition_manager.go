package storagemgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/dgraph-io/badger/v4"
	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	gitalycfgprom "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
)

// ErrPartitionManagerClosed is returned when the PartitionManager stops processing transactions.
var ErrPartitionManagerClosed = errors.New("partition manager closed")

// transactionManager is the interface of TransactionManager as used by PartitionManager. See the
// TransactionManager's documentation for more details.
type transactionManager interface {
	Begin(context.Context, string, []string, bool) (*Transaction, error)
	Run() error
	Close()
	isClosing() bool
}

type transactionManagerFactory func(
	partitionID partitionID,
	storageMgr *storageManager,
	cmdFactory git.CommandFactory,
	absoluteStateDir, stagingDir string,
) transactionManager

// PartitionManager is responsible for managing the lifecycle of each TransactionManager.
type PartitionManager struct {
	// storages are the storages configured in this Gitaly server. The map is keyed by the storage name.
	storages map[string]*storageManager
	// commandFactory is passed as a dependency to the constructed TransactionManagers.
	commandFactory git.CommandFactory
	// transactionManagerFactory is a factory to create TransactionManagers. This shouldn't ever be changed
	// during normal operation, but can be used to adjust the transaction manager's behaviour in tests.
	transactionManagerFactory transactionManagerFactory
	// metrics accounts for all metrics of transaction operations. It will be
	// passed down to each transaction manager and is shared between them. The
	// metrics must be registered to be collected by prometheus collector.
	metrics *metrics
}

// DatabaseOpener is responsible for opening a database handle.
type DatabaseOpener interface {
	// OpenDatabase opens a database at the given path.
	OpenDatabase(log.Logger, string) (Database, error)
}

// DatabaseOpenerFunc is a function that implements DatabaseOpener.
type DatabaseOpenerFunc func(log.Logger, string) (Database, error)

// OpenDatabase opens a handle to the database at the given path.
func (fn DatabaseOpenerFunc) OpenDatabase(logger log.Logger, path string) (Database, error) {
	return fn(logger, path)
}

// storageManager represents a single storage.
type storageManager struct {
	// mu synchronizes access to the fields of storageManager.
	mu sync.Mutex
	// logger handles all logging for storageManager.
	logger log.Logger
	// path is the absolute path to the storage's root.
	path string
	// repoFactory is a factory type that builds localrepo instances for this storage.
	repoFactory localrepo.StorageScopedFactory
	// stagingDirectory is the directory where all of the TransactionManager staging directories
	// should be created.
	stagingDirectory string
	// closed tracks whether the storageManager has been closed. If it is closed,
	// no new transactions are allowed to begin.
	closed bool
	// stopGC stops the garbage collection and waits for it to return.
	stopGC func()
	// db is the handle to the key-value store used for storing the storage's database state.
	database Database
	// partitionAssigner manages partition assignments of repositories.
	partitionAssigner *partitionAssigner
	// partitions contains all the active partitions. Each repository can have up to one partition.
	partitions map[partitionID]*partition
	// activePartitions keeps track of active partitions.
	activePartitions sync.WaitGroup
}

func (sm *storageManager) close() {
	sm.mu.Lock()
	// Mark the storage as closed so no new transactions can begin anymore. This
	// also means no more partitions are spawned.
	sm.closed = true
	for _, ptn := range sm.partitions {
		// Close all partitions.
		ptn.close()
	}
	sm.mu.Unlock()

	// Wait for all partitions to finish.
	sm.activePartitions.Wait()

	if err := sm.partitionAssigner.Close(); err != nil {
		sm.logger.WithError(err).Error("failed closing partition assigner")
	}

	// Wait until the database's garbage collection goroutine has returned.
	sm.stopGC()

	if err := sm.database.Close(); err != nil {
		sm.logger.WithError(err).Error("failed closing storage's database")
	}
}

// finalizeTransaction decrements the partition's pending transaction count and closes it if there are no more
// transactions pending.
func (sm *storageManager) finalizeTransaction(ptn *partition) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	ptn.pendingTransactionCount--
	if ptn.pendingTransactionCount == 0 {
		ptn.close()
	}
}

// finalizableTransaction wraps a transaction to track the number of in-flight transactions for a Partition.
type finalizableTransaction struct {
	// finalize is called when the transaction is either committed or rolled back.
	finalize func()
	// Transaction is the underlying transaction.
	*Transaction
}

// Commit commits the transaction and runs the finalizer.
func (tx *finalizableTransaction) Commit(ctx context.Context) error {
	defer tx.finalize()
	return tx.Transaction.Commit(ctx)
}

// Rollback rolls back the transaction and runs the finalizer.
func (tx *finalizableTransaction) Rollback() error {
	defer tx.finalize()
	return tx.Transaction.Rollback()
}

// newFinalizableTransaction returns a wrapped transaction that executes finalizeTransaction when the transaction
// is committed or rolled back.
func (sm *storageManager) newFinalizableTransaction(ptn *partition, tx *Transaction) *finalizableTransaction {
	finalized := false
	return &finalizableTransaction{
		finalize: func() {
			if finalized {
				return
			}

			finalized = true
			sm.finalizeTransaction(ptn)
		},
		Transaction: tx,
	}
}

// partition contains the transaction manager and tracks the number of in-flight transactions for the partition.
type partition struct {
	// closing is closed when the partition has no longer any active transactions.
	closing chan struct{}
	// transactionManagerClosed is closed to signal when the partition's TransactionManager.Run has returned.
	// Clients stumbling on the partition when it is closing wait on this channel to know when the previous
	// TransactionManager has closed and it is safe to start another one.
	transactionManagerClosed chan struct{}
	// transactionManager manages all transactions for the partition.
	transactionManager transactionManager
	// pendingTransactionCount holds the current number of in flight transactions being processed by the manager.
	pendingTransactionCount uint
}

// close closes the partition's transaction manager.
func (ptn *partition) close() {
	// The partition may be closed either due to PartitionManager itself being closed,
	// or due it having no more active transactions. Both of these can happen, in which
	// case both of them would attempt to close the channel. Check first whether the
	// channel has already been closed.
	if ptn.isClosing() {
		return
	}

	close(ptn.closing)
	ptn.transactionManager.Close()
}

// isClosing returns whether partition is closing.
func (ptn *partition) isClosing() bool {
	select {
	case <-ptn.closing:
		return true
	default:
		return false
	}
}

// NewPartitionManager returns a new PartitionManager.
func NewPartitionManager(
	configuredStorages []config.Storage,
	cmdFactory git.CommandFactory,
	localRepoFactory localrepo.Factory,
	logger log.Logger,
	dbOpener DatabaseOpener,
	gcTickerFactory helper.TickerFactory,
	promCfg gitalycfgprom.Config,
) (*PartitionManager, error) {
	storages := make(map[string]*storageManager, len(configuredStorages))
	for _, storage := range configuredStorages {
		repoFactory, err := localRepoFactory.ScopeByStorage(storage.Name)
		if err != nil {
			return nil, fmt.Errorf("scope by storage: %w", err)
		}

		stagingDir := stagingDirectoryPath(storage.Path)
		// Remove a possible already existing staging directory as it may contain stale files
		// if the previous process didn't shutdown gracefully.
		if err := os.RemoveAll(stagingDir); err != nil {
			return nil, fmt.Errorf("failed clearing storage's staging directory: %w", err)
		}

		if err := os.Mkdir(stagingDir, perm.PrivateDir); err != nil {
			return nil, fmt.Errorf("create storage's staging directory: %w", err)
		}

		databaseDir := filepath.Join(storage.Path, "database")
		if err := os.Mkdir(databaseDir, perm.PrivateDir); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("create storage's database directory: %w", err)
		}

		if err := safe.NewSyncer().SyncHierarchy(storage.Path, "database"); err != nil {
			return nil, fmt.Errorf("sync database directory: %w", err)
		}

		storageLogger := logger.WithField("storage", storage.Name)
		db, err := dbOpener.OpenDatabase(storageLogger.WithField("component", "database"), databaseDir)
		if err != nil {
			return nil, fmt.Errorf("create storage's database directory: %w", err)
		}

		pa, err := newPartitionAssigner(db, storage.Path)
		if err != nil {
			return nil, fmt.Errorf("new partition assigner: %w", err)
		}

		gcCtx, stopGC := context.WithCancel(context.Background())
		gcStopped := make(chan struct{})
		go func() {
			defer func() {
				storageLogger.Info("value log garbage collection goroutine stopped")
				close(gcStopped)
			}()

			// Configure the garbage collection discard ratio at 0.5. This means the value log is garbage
			// collected if we can reclaim more than half of the space.
			const gcDiscardRatio = 0.5

			ticker := gcTickerFactory.NewTicker()
			for {
				storageLogger.Info("value log garbage collection started")

				for {
					if err := db.RunValueLogGC(gcDiscardRatio); err != nil {
						if errors.Is(err, badger.ErrNoRewrite) {
							// No log files were rewritten. This means there was nothing
							// to garbage collect.
							break
						}

						storageLogger.WithError(err).Error("value log garbage collection failed")
						break
					}

					// Log files were garbage collected. Check immediately if there are more
					// files that need garbage collection.
					storageLogger.Info("value log file garbage collected")

					if gcCtx.Err() != nil {
						// As we'd keep going until no log files were rewritten, break the loop
						// if GC has run.
						break
					}
				}

				storageLogger.Info("value log garbage collection finished")

				ticker.Reset()
				select {
				case <-ticker.C():
				case <-gcCtx.Done():
					ticker.Stop()
					return
				}
			}
		}()

		storages[storage.Name] = &storageManager{
			logger:           storageLogger,
			path:             storage.Path,
			repoFactory:      repoFactory,
			stagingDirectory: stagingDir,
			stopGC: func() {
				stopGC()
				<-gcStopped
			},
			database:          db,
			partitionAssigner: pa,
			partitions:        map[partitionID]*partition{},
		}
	}

	metrics := newMetrics(promCfg)

	return &PartitionManager{
		storages:       storages,
		commandFactory: cmdFactory,
		transactionManagerFactory: func(
			partitionID partitionID,
			storageMgr *storageManager,
			cmdFactory git.CommandFactory,
			absoluteStateDir, stagingDir string,
		) transactionManager {
			return NewTransactionManager(
				partitionID,
				logger,
				storageMgr.database,
				storageMgr.path,
				absoluteStateDir,
				stagingDir,
				cmdFactory,
				storageMgr.repoFactory,
				metrics,
			)
		},
		metrics: metrics,
	}, nil
}

func stagingDirectoryPath(storagePath string) string {
	return filepath.Join(storagePath, "staging")
}

// TransactionOptions are used to pass transaction options into Begin.
type TransactionOptions struct {
	// ReadOnly indicates whether this is a read-only transaction. Read-only transactions are not
	// configured with a quarantine directory and do not commit a log entry.
	ReadOnly bool
	// AlternateRelativePath specifies a repository to include in the transaction's snapshot as well.
	AlternateRelativePath string
	// AllowPartitionAssignmentWithoutRepository determines whether a partition assignment should be
	// written out even if repository does not exist.
	AllowPartitionAssignmentWithoutRepository bool
}

// Begin gets the TransactionManager for the specified repository and starts a transaction. If a
// TransactionManager is not already running, a new one is created and used. The partition tracks
// the number of pending transactions and this counter gets incremented when Begin is invoked.
//
// storageName and relativePath specify the target repository to begin a transaction against.
func (pm *PartitionManager) Begin(ctx context.Context, storageName, relativePath string, opts TransactionOptions) (*finalizableTransaction, error) {
	storageMgr, ok := pm.storages[storageName]
	if !ok {
		return nil, structerr.NewNotFound("unknown storage: %q", storageName)
	}

	relativePath, err := storage.ValidateRelativePath(storageMgr.path, relativePath)
	if err != nil {
		return nil, structerr.NewInvalidArgument("validate relative path: %w", err)
	}

	partitionID, err := storageMgr.partitionAssigner.getPartitionID(ctx, relativePath, opts.AlternateRelativePath, opts.AllowPartitionAssignmentWithoutRepository)
	if err != nil {
		if errors.Is(err, badger.ErrDBClosed) {
			// The database is closed when PartitionManager is closing. Return a more
			// descriptive error of what happened.
			return nil, ErrPartitionManagerClosed
		}

		return nil, fmt.Errorf("get partition: %w", err)
	}

	relativeStateDir := deriveStateDirectory(partitionID)
	absoluteStateDir := filepath.Join(storageMgr.path, relativeStateDir)
	if err := os.MkdirAll(filepath.Dir(absoluteStateDir), perm.PrivateDir); err != nil {
		return nil, fmt.Errorf("create state directory hierarchy: %w", err)
	}

	if err := safe.NewSyncer().SyncHierarchy(storageMgr.path, filepath.Dir(relativeStateDir)); err != nil {
		return nil, fmt.Errorf("sync state directory hierarchy: %w", err)
	}

	for {
		storageMgr.mu.Lock()
		if storageMgr.closed {
			storageMgr.mu.Unlock()
			return nil, ErrPartitionManagerClosed
		}

		ptn, ok := storageMgr.partitions[partitionID]
		if !ok {
			ptn = &partition{
				closing:                  make(chan struct{}),
				transactionManagerClosed: make(chan struct{}),
			}

			stagingDir, err := os.MkdirTemp(storageMgr.stagingDirectory, "")
			if err != nil {
				storageMgr.mu.Unlock()
				return nil, fmt.Errorf("create staging directory: %w", err)
			}

			mgr := pm.transactionManagerFactory(partitionID, storageMgr, pm.commandFactory, absoluteStateDir, stagingDir)

			ptn.transactionManager = mgr

			storageMgr.partitions[partitionID] = ptn

			storageMgr.activePartitions.Add(1)
			go func() {
				logger := storageMgr.logger.WithField("partition", relativePath)

				if err := mgr.Run(); err != nil {
					logger.WithError(err).Error("partition failed")
				}

				// In the event that TransactionManager stops running, a new TransactionManager will
				// need to be started in order to continue processing transactions. The partition is
				// deleted allowing the next transaction for the repository to create a new partition
				// and TransactionManager.
				storageMgr.mu.Lock()
				delete(storageMgr.partitions, partitionID)
				storageMgr.mu.Unlock()

				close(ptn.transactionManagerClosed)

				// If the TransactionManager returned due to an error, it could be that there are still
				// in-flight transactions operating on their staged state. Removing the staging directory
				// while they are active can lead to unexpected errors. Wait with the removal until they've
				// all finished, and only then remove the staging directory.
				//
				// All transactions must eventually finish, so we don't wait on a context cancellation here.
				<-ptn.closing

				if err := os.RemoveAll(stagingDir); err != nil {
					logger.WithError(err).Error("failed removing partition's staging directory")
				}

				storageMgr.activePartitions.Done()
			}()
		}

		if ptn.isClosing() {
			// If the partition is in the process of shutting down, the partition should not be
			// used. The lock is released while waiting for the partition to complete closing as to
			// not block other partitions from processing transactions. Once closing is complete, a
			// new attempt is made to get a valid partition.
			storageMgr.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-ptn.transactionManagerClosed:
			}

			continue
		}

		ptn.pendingTransactionCount++
		storageMgr.mu.Unlock()

		var snapshottedRelativePaths []string
		if opts.AlternateRelativePath != "" {
			snapshottedRelativePaths = []string{opts.AlternateRelativePath}
		}

		transaction, err := ptn.transactionManager.Begin(ctx, relativePath, snapshottedRelativePaths, opts.ReadOnly)
		if err != nil {
			// The pending transaction count needs to be decremented since the transaction is no longer
			// inflight. A transaction failing does not necessarily mean the transaction manager has
			// stopped running. Consequently, if there are no other pending transactions the partition
			// should be closed.
			storageMgr.finalizeTransaction(ptn)

			return nil, err
		}

		return storageMgr.newFinalizableTransaction(ptn, transaction), nil
	}
}

// deriveStateDirectory hashes the partition ID and returns the state
// directory where state related to the partition should be stored.
func deriveStateDirectory(id partitionID) string {
	hasher := sha256.New()
	hasher.Write([]byte(id.String()))
	hash := hex.EncodeToString(hasher.Sum(nil))

	return filepath.Join(
		"partitions",
		// These two levels balance the state directories into smaller
		// subdirectories to keep the directory sizes reasonable.
		hash[0:2],
		hash[2:4],
		id.String(),
	)
}

// Close closes transaction processing for all storages and waits for closing completion.
func (pm *PartitionManager) Close() {
	var activeStorages sync.WaitGroup
	for _, storageMgr := range pm.storages {
		activeStorages.Add(1)
		storageMgr := storageMgr
		go func() {
			storageMgr.close()
			activeStorages.Done()
		}()
	}

	activeStorages.Wait()
}

// Describe is used to describe Prometheus metrics.
func (pm *PartitionManager) Describe(metrics chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(pm, metrics)
}

// Collect is used to collect Prometheus metrics.
func (pm *PartitionManager) Collect(metrics chan<- prometheus.Metric) {
	pm.metrics.Collect(metrics)
}
