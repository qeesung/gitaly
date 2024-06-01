package keyvalue

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/dgraph-io/badger/v4"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
)

// Configure the garbage collection discard ratio at 0.5. This means the value log is garbage
// collected if we can reclaim more than half of the space.
const gcDiscardRatio = 0.5

// DatabaseOpener is responsible for opening a database handle.
type DatabaseOpener interface {
	// OpenDatabase opens a database at the given path.
	OpenDatabase(log.Logger, string) (Store, error)
}

// DatabaseOpenerFunc is a function that implements DatabaseOpener.
type DatabaseOpenerFunc func(log.Logger, string) (Store, error)

// OpenDatabase opens a handle to the database at the given path.
func (fn DatabaseOpenerFunc) OpenDatabase(logger log.Logger, path string) (Store, error) {
	return fn(logger, path)
}

// internalDirectoryPath returns the full path of Gitaly's internal data directory for the storage.
func internalDirectoryPath(storagePath string) string {
	return filepath.Join(storagePath, config.GitalyDataPrefix)
}

// DBManager manages the life-cycles of per-storage databases. It provides methods to access the
// databases as well as manages the garbage collection for each of them.
type DBManager struct {
	databases  map[string]Store
	gcStoppers []func()
	logger     log.Logger
}

// NewDBManager creates a new DBManager instance that manages the databases for the configured
// storages. It opens the databases and starts the garbage collection goroutines. It also ensures
// that if one of the databases fails to initialize, the garbage collection goroutines for the other
// successfully initialized databases are stopped.
func NewDBManager(
	configuredStorages []config.Storage,
	dbOpener DatabaseOpener,
	gcTickerFactory helper.TickerFactory,
	logger log.Logger,
) (dbMgr *DBManager, returnedErr error) {
	logger = logger.WithField("component", "database")

	databases := make(map[string]Store, len(configuredStorages))
	var gcStoppers []func()

	// If one of the DB could not be initialized, stops all GC goroutines of other successful DB.
	defer func() {
		if returnedErr != nil {
			closeAllDBs(logger, gcStoppers, databases)
		}
	}()

	for _, configuredStorage := range configuredStorages {
		internalDir := internalDirectoryPath(configuredStorage.Path)

		databaseDir := filepath.Join(internalDir, "database")
		if err := os.MkdirAll(databaseDir, perm.PrivateDir); err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("create storage's database directory: %w", err)
		}

		if err := safe.NewSyncer().SyncHierarchy(internalDir, "database"); err != nil {
			return nil, fmt.Errorf("sync database directory: %w", err)
		}

		storageLogger := logger.WithField("storage", configuredStorage.Name)
		db, err := dbOpener.OpenDatabase(storageLogger, databaseDir)
		if err != nil {
			return nil, fmt.Errorf("create storage's database directory: %w", err)
		}

		gcCtx, stopGC := context.WithCancel(context.Background())
		gcStopped := make(chan struct{})
		go func(db Store) {
			defer func() {
				storageLogger.Info("value log garbage collection goroutine stopped")
				close(gcStopped)
			}()

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
		}(db)

		gcStoppers = append(gcStoppers, func() {
			stopGC()
			<-gcStopped
		})

		databases[configuredStorage.Name] = db
	}
	return &DBManager{
		databases:  databases,
		gcStoppers: gcStoppers,
	}, nil
}

// GetDB returns the database for the given storage.
func (dbMgr *DBManager) GetDB(storage string) (Store, error) {
	if db, exist := dbMgr.databases[storage]; exist {
		return db, nil
	}
	return nil, fmt.Errorf("database for storage %q not found", storage)
}

// Close method is responsible for closing the database manager and all the databases it
// manages.  It first stops the garbage collection goroutines for each database, and then closes
// each database.  This ensures that all resources used by the databases are properly released
// before the manager is closed.
func (dbMgr *DBManager) Close() {
	closeAllDBs(dbMgr.logger, dbMgr.gcStoppers, dbMgr.databases)
}

func closeAllDBs(logger log.Logger, gcStoppers []func(), databases map[string]Store) {
	for _, gcStopper := range gcStoppers {
		gcStopper()
	}
	for _, db := range databases {
		if err := db.Close(); err != nil {
			logger.WithError(err).Error("failed closing storage's database")
		}
	}
}
