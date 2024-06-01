package keyvalue

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/dgraph-io/badger/v4"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

type dbWrapper struct {
	Store
	close         func() error
	runValueLogGC func(float64) error
}

func (db dbWrapper) RunValueLogGC(discardRatio float64) error {
	return db.runValueLogGC(discardRatio)
}

func (db dbWrapper) Close() error {
	return db.close()
}

func TestNewDBManager(t *testing.T) {
	t.Parallel()

	logger := testhelper.NewLogger(t)

	t.Run("no configured storages", func(t *testing.T) {
		dbMgr, err := NewDBManager(nil, nil, helper.NewNullTickerFactory(), logger)
		require.NoError(t, err)
		require.NotNil(t, dbMgr)
		require.Empty(t, dbMgr.databases)
		require.Empty(t, dbMgr.gcStoppers)
	})

	t.Run("database opener error", func(t *testing.T) {
		opener := func(log.Logger, string) (Store, error) {
			return nil, errors.New("database opener error")
		}

		cfg := testcfg.Build(t)
		dbMgr, err := NewDBManager(cfg.Storages, opener, helper.NewNullTickerFactory(), logger)
		require.Error(t, err)
		require.Nil(t, dbMgr)
	})

	t.Run("successful initialization", func(t *testing.T) {
		cfg := testcfg.Build(t, testcfg.WithStorages("first", "second"))

		dbMgr, err := NewDBManager(cfg.Storages, NewBadgerStore, helper.NewNullTickerFactory(), logger)
		require.NoError(t, err)
		require.NotNil(t, dbMgr)

		assertDB := func(storage string) {
			db, err := dbMgr.GetDB(storage)
			require.NoError(t, err)

			require.NoError(t, db.Update(func(txn ReadWriter) error {
				require.NoError(t, txn.Set([]byte("key"), []byte(storage)))
				return nil
			}))
			require.NoError(t, db.View(func(txn ReadWriter) error {
				item, err := txn.Get([]byte("key"))
				require.NoError(t, err)
				require.NoError(t, item.Value(func(value []byte) error {
					require.Equal(t, storage, string(value))
					return nil
				}))
				return nil
			}))
		}
		assertDB("first")
		assertDB("second")

		dbMgr.Close()
	})

	t.Run("first DB successful, second DB fails", func(t *testing.T) {
		cfg := testcfg.Build(t, testcfg.WithStorages("first", "second"))
		logger := testhelper.NewLogger(t)

		firstDBClosed := atomic.Bool{}
		opener := DatabaseOpenerFunc(func(logger log.Logger, path string) (Store, error) {
			// Return a successful store for the first storage
			if strings.Contains(path, "first") {
				return dbWrapper{
					close: func() error {
						firstDBClosed.Store(true)
						return nil
					},
					runValueLogGC: func(_ float64) error { return nil },
				}, nil
			}
			// Return an error for the second storage
			return nil, errors.New("failed to open second DB")
		})

		dbMgr, err := NewDBManager(cfg.Storages, opener, helper.NewNullTickerFactory(), logger)
		require.EqualError(t, err, "create storage's database directory: failed to open second DB")
		require.Nil(t, dbMgr)
		require.Equal(t, true, firstDBClosed.Load())
	})
}

func TestDBManager_GetDB(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t, testcfg.WithStorages("first", "second"))
	logger := testhelper.NewLogger(t)

	dbMgr, err := NewDBManager(cfg.Storages, NewBadgerStore, helper.NewNullTickerFactory(), logger)
	require.NoError(t, err)
	t.Cleanup(dbMgr.Close)

	t.Run("get non-existent storage", func(t *testing.T) {
		_, err := dbMgr.GetDB("non-existent")
		require.Error(t, err)
		require.EqualError(t, err, "database for storage \"non-existent\" not found")
	})

	t.Run("get existing storage", func(t *testing.T) {
		firstDB, err := dbMgr.GetDB("first")
		require.NoError(t, err)
		require.NotNil(t, firstDB)

		secondDB, err := dbMgr.GetDB("second")
		require.NoError(t, err)
		require.NotNil(t, secondDB)
	})
}

func TestDBManager_garbageCollection(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)

	logger := testhelper.NewLogger(t)
	loggerHook := testhelper.AddLoggerHook(logger)

	gcRunCount := 0
	gcCompleted := make(chan struct{})
	errExpected := errors.New("some gc failure")

	dbMgr, err := NewDBManager(
		cfg.Storages,
		func(logger log.Logger, path string) (Store, error) {
			db, err := NewBadgerStore(logger, path)
			return dbWrapper{
				Store: db,
				close: db.Close,
				runValueLogGC: func(discardRatio float64) error {
					defer func() { gcRunCount++ }()
					if gcRunCount < 2 {
						return nil
					}

					if gcRunCount == 2 {
						return badger.ErrNoRewrite
					}

					return errExpected
				},
			}, err
		},
		helper.TickerFactoryFunc(func() helper.Ticker {
			return helper.NewCountTicker(1, func() {
				close(gcCompleted)
			})
		}),
		logger,
	)
	require.NoError(t, err)
	defer dbMgr.Close()

	// The ticker has exhausted and we've performed the two GC runs we wanted to test.
	<-gcCompleted

	// Close the manager to ensure the GC goroutine also stops.
	dbMgr.Close()

	var gcLogs []*logrus.Entry
	for _, entry := range loggerHook.AllEntries() {
		if !strings.HasPrefix(entry.Message, "value log") {
			continue
		}

		gcLogs = append(gcLogs, entry)
	}

	// We're testing the garbage collection goroutine through multiple loops.
	//
	// The first runs immediately on startup before the ticker even ticks. The
	// First RunValueLogGC pretends to have performed a GC, so another GC is
	// immediately attempted. The second round returns badger.ErrNoRewrite, so
	// the GC loop stops and waits for another tick
	require.Equal(t, "value log garbage collection started", gcLogs[0].Message)
	require.Equal(t, "value log file garbage collected", gcLogs[1].Message)
	require.Equal(t, "value log file garbage collected", gcLogs[2].Message)
	require.Equal(t, "value log garbage collection finished", gcLogs[3].Message)

	// The second tick results in a garbage collection run that pretend to have
	// failed with errExpected.
	require.Equal(t, "value log garbage collection started", gcLogs[4].Message)
	require.Equal(t, "value log garbage collection failed", gcLogs[5].Message)
	require.Equal(t, errExpected, gcLogs[5].Data[logrus.ErrorKey])
	require.Equal(t, "value log garbage collection finished", gcLogs[6].Message)

	// After the second round, the DBMManager is closed and we assert that the
	// garbage collection goroutine has also stopped.
	require.Equal(t, "value log garbage collection goroutine stopped", gcLogs[7].Message)
}
