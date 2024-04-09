package storagemgr

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/dgraph-io/badger/v4"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

// stoppedTransactionManager is a wrapper type that prevents the transactionManager from
// running. This is useful in tests that test certain order of operations.
type stoppedTransactionManager struct{ transactionManager }

func (stoppedTransactionManager) Run() error { return nil }

func TestPartitionManager(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	umask := testhelper.Umask()
	logger := testhelper.NewLogger(t)

	// steps defines execution steps in a test. Each test case can define multiple steps to exercise
	// more complex behavior.
	type steps []any

	// begin calls Begin on the TransactionManager to start a new transaction.
	type begin struct {
		// transactionID is the identifier given to the transaction created. This is used to identify
		// the transaction in later steps.
		transactionID int
		// ctx is the context used when `Begin()` gets invoked.
		ctx context.Context
		// repo is the repository that the transaction belongs to.
		repo storage.Repository
		// alternateRelativePath is the relative path of the alternate repository.
		alternateRelativePath string
		// expectedState contains the partitions by their storages and their pending transaction count at
		// the end of the step.
		expectedState map[string]map[partitionID]uint
		// expectedError is the error expected to be returned when beginning the transaction.
		expectedError error
	}

	// commit calls Commit on a transaction.
	type commit struct {
		// transactionID identifies the transaction to commit.
		transactionID int
		// ctx is the context used when `Commit()` gets invoked.
		ctx context.Context
		// expectedState contains the partitions by their storages and their pending transaction count at
		// the end of the step.
		expectedState map[string]map[partitionID]uint
		// expectedError is the error that is expected to be returned when committing the transaction.
		expectedError error
	}

	// rollback calls Rollback on a transaction.
	type rollback struct {
		// transactionID identifies the transaction to rollback.
		transactionID int
		// expectedState contains the partitions by their storages and their pending transaction count at
		// the end of the step.
		expectedState map[string]map[partitionID]uint
		// expectedError is the error that is expected to be returned when rolling back the transaction.
		expectedError error
	}

	// closePartition closes the transaction manager for the specified repository. This is done to
	// simulate failures.
	type closePartition struct {
		// transactionID identifies the transaction manager associated with the transaction to stop.
		transactionID int
	}

	// finalizeTransaction runs the transaction finalizer for the specified repository. This is used
	// to simulate finalizers executing after a transaction manager has been stopped.
	type finalizeTransaction struct {
		// transactionID identifies the transaction to finalize.
		transactionID int
	}

	// closeManager closes the partition manager. This is done to simulate errors for transactions
	// being processed without a running partition manager.
	type closeManager struct{}

	// blockOnPartitionClosing checks if any partitions are currently in the process of
	// closing. If some are, the function waits for the closing process to complete before
	// continuing. This is required in order to accurately validate partition state.
	blockOnPartitionClosing := func(t *testing.T, pm *PartitionManager) {
		t.Helper()

		var waitFor []chan struct{}
		for _, sp := range pm.storages {
			sp.mu.Lock()
			for _, ptn := range sp.partitions {
				// The closePartition step closes the transaction manager directly without calling close
				// on the partition, so we check the manager directly here as well.
				if ptn.isClosing() || ptn.transactionManager.isClosing() {
					waitFor = append(waitFor, ptn.transactionManagerClosed)
				}
			}
			sp.mu.Unlock()
		}

		for _, closed := range waitFor {
			<-closed
		}
	}

	// checkExpectedState validates that the partition manager contains the correct partitions and
	// associated transaction count at the point of execution.
	checkExpectedState := func(t *testing.T, cfg config.Cfg, partitionManager *PartitionManager, expectedState map[string]map[partitionID]uint) {
		t.Helper()

		actualState := map[string]map[partitionID]uint{}
		for storageName, storageMgr := range partitionManager.storages {
			for ptnID, partition := range storageMgr.partitions {
				if actualState[storageName] == nil {
					actualState[storageName] = map[partitionID]uint{}
				}

				actualState[storageName][ptnID] = partition.pendingTransactionCount
			}
		}

		if expectedState == nil {
			expectedState = map[string]map[partitionID]uint{}
		}

		require.Equal(t, expectedState, actualState)
	}

	setupRepository := func(t *testing.T, cfg config.Cfg, storage config.Storage) storage.Repository {
		t.Helper()

		repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			Storage:                storage,
			SkipCreationViaService: true,
		})

		return repo
	}

	// transactionData holds relevant data for each transaction created during a testcase.
	type transactionData struct {
		txn        *finalizableTransaction
		storageMgr *storageManager
		ptn        *partition
	}

	type setupData struct {
		steps                     steps
		transactionManagerFactory transactionManagerFactory
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T, cfg config.Cfg) setupData
	}{
		{
			desc: "transaction committed for single repository",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						begin{
							repo: repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						commit{},
					},
				}
			},
		},
		{
			desc: "two transactions committed for single repository sequentially",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						begin{
							transactionID: 1,
							repo:          repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						commit{
							transactionID: 1,
						},
						begin{
							transactionID: 2,
							repo:          repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						commit{
							transactionID: 2,
						},
					},
				}
			},
		},
		{
			desc: "two transactions committed for single repository in parallel",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						begin{
							transactionID: 1,
							repo:          repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						begin{
							transactionID: 2,
							repo:          repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 2,
								},
							},
						},
						commit{
							transactionID: 1,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						commit{
							transactionID: 2,
						},
					},
				}
			},
		},
		{
			desc: "transaction committed for multiple repositories",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repoA := setupRepository(t, cfg, cfg.Storages[0])
				repoB := setupRepository(t, cfg, cfg.Storages[0])
				repoC := setupRepository(t, cfg, cfg.Storages[1])

				return setupData{
					steps: steps{
						begin{
							transactionID: 1,
							repo:          repoA,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						begin{
							transactionID: 2,
							repo:          repoB,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
									2: 1,
								},
							},
						},
						begin{
							transactionID: 3,
							repo:          repoC,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
									2: 1,
								},
								"other-storage": {
									1: 1,
								},
							},
						},
						commit{
							transactionID: 1,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									2: 1,
								},
								"other-storage": {
									1: 1,
								},
							},
						},
						commit{
							transactionID: 2,
							expectedState: map[string]map[partitionID]uint{
								"other-storage": {
									1: 1,
								},
							},
						},
						commit{
							transactionID: 3,
						},
					},
				}
			},
		},
		{
			desc: "transaction rolled back for single repository",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						begin{
							repo: repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						rollback{},
					},
				}
			},
		},
		{
			desc: "starting transaction failed due to cancelled context",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				stepCtx, cancel := context.WithCancel(ctx)
				cancel()

				return setupData{
					steps: steps{
						begin{
							ctx:           stepCtx,
							repo:          repo,
							expectedError: context.Canceled,
						},
					},
					transactionManagerFactory: func(
						testPartitionID partitionID,
						storageMgr *storageManager,
						commandFactory git.CommandFactory,
						absoluteStateDir, stagingDir string,
					) transactionManager {
						return stoppedTransactionManager{
							transactionManager: NewTransactionManager(
								testPartitionID,
								storageMgr.logger,
								storageMgr.database,
								storageMgr.path,
								absoluteStateDir,
								stagingDir,
								commandFactory,
								storageMgr.repoFactory,
								nil,
							),
						}
					},
				}
			},
		},
		{
			desc: "committing transaction failed due to cancelled context",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				stepCtx, cancel := context.WithCancel(ctx)
				cancel()

				return setupData{
					steps: steps{
						begin{
							repo: repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						commit{
							ctx:           stepCtx,
							expectedError: context.Canceled,
						},
					},
					transactionManagerFactory: func(
						testPartitionID partitionID,
						storageMgr *storageManager,
						commandFactory git.CommandFactory,
						absoluteStateDir, stagingDir string,
					) transactionManager {
						txMgr := NewTransactionManager(
							testPartitionID,
							logger,
							storageMgr.database,
							storageMgr.path,
							absoluteStateDir,
							stagingDir,
							commandFactory,
							storageMgr.repoFactory,
							nil,
						)

						// Unset the admission queue. This has the effect that we will block
						// indefinitely when trying to submit the transaction to it in the
						// commit step. Like this, we can racelessly verify that the context
						// cancellation does indeed abort the commit.
						txMgr.admissionQueue = nil

						return txMgr
					},
				}
			},
		},
		{
			desc: "committing transaction failed due to stopped transaction manager",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						begin{
							repo: repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						closePartition{},
						commit{
							expectedError: ErrTransactionProcessingStopped,
						},
					},
				}
			},
		},
		{
			desc: "transaction from previous transaction manager finalized after new manager started",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						begin{
							transactionID: 1,
							repo:          repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						closePartition{
							transactionID: 1,
						},
						begin{
							transactionID: 2,
							repo:          repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						finalizeTransaction{
							transactionID: 1,
						},
						commit{
							transactionID: 2,
						},
					},
				}
			},
		},
		{
			desc: "transaction started after partition manager stopped",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						closeManager{},
						begin{
							repo:          repo,
							expectedError: ErrPartitionManagerClosed,
						},
					},
				}
			},
		},
		{
			desc: "multiple transactions started after partition manager stopped",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						closeManager{},
						begin{
							transactionID: 1,
							repo:          repo,
							expectedError: ErrPartitionManagerClosed,
						},
						begin{
							transactionID: 2,
							repo:          repo,
							expectedError: ErrPartitionManagerClosed,
						},
					},
				}
			},
		},
		{
			desc: "transaction for a non-existent storage",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				return setupData{
					steps: steps{
						begin{
							repo: &gitalypb.Repository{
								StorageName: "non-existent",
							},
							expectedError: structerr.NewNotFound("unknown storage: %q", "non-existent"),
						},
					},
				}
			},
		},

		{
			desc: "relative paths are cleaned",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						begin{
							transactionID: 1,
							repo:          repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						begin{
							transactionID: 2,
							repo: &gitalypb.Repository{
								StorageName:  repo.GetStorageName(),
								RelativePath: filepath.Join(repo.GetRelativePath(), "child-dir", ".."),
							},
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 2,
								},
							},
						},
					},
				}
			},
		},
		{
			desc: "transaction finalized only once",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						begin{
							transactionID: 1,
							repo:          repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						begin{
							transactionID: 2,
							repo: &gitalypb.Repository{
								StorageName:  repo.GetStorageName(),
								RelativePath: repo.GetRelativePath(),
							},
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 2,
								},
							},
						},
						rollback{
							transactionID: 2,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						rollback{
							transactionID: 2,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
							expectedError: ErrTransactionAlreadyRollbacked,
						},
					},
				}
			},
		},
		{
			desc: "repository and alternate target the same partition",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo := setupRepository(t, cfg, cfg.Storages[0])
				alternateRepo := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						begin{
							transactionID:         1,
							repo:                  repo,
							alternateRelativePath: alternateRepo.GetRelativePath(),
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						begin{
							transactionID: 2,
							repo:          repo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 2,
								},
							},
						},
						begin{
							transactionID: 3,
							repo:          alternateRepo,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 3,
								},
							},
						},
						rollback{
							transactionID: 1,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 2,
								},
							},
						},
						rollback{
							transactionID: 2,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						rollback{
							transactionID: 3,
						},
					},
				}
			},
		},
		{
			desc: "beginning transaction on repositories in different partitions fails",
			setup: func(t *testing.T, cfg config.Cfg) setupData {
				repo1 := setupRepository(t, cfg, cfg.Storages[0])
				repo2 := setupRepository(t, cfg, cfg.Storages[0])

				return setupData{
					steps: steps{
						begin{
							transactionID: 1,
							repo:          repo1,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
								},
							},
						},
						begin{
							transactionID: 2,
							repo:          repo2,
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
									2: 1,
								},
							},
						},
						begin{
							transactionID:         3,
							repo:                  repo1,
							alternateRelativePath: repo2.GetRelativePath(),
							expectedState: map[string]map[partitionID]uint{
								"default": {
									1: 1,
									2: 1,
								},
							},
							expectedError: fmt.Errorf("get partition: %w", ErrRepositoriesAreInDifferentPartitions),
						},
					},
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			cfg := testcfg.Build(t, testcfg.WithStorages("default", "other-storage"))
			logger := testhelper.SharedLogger(t)

			cmdFactory := gittest.NewCommandFactory(t, cfg)
			catfileCache := catfile.NewCache(cfg)
			t.Cleanup(catfileCache.Stop)

			localRepoFactory := localrepo.NewFactory(logger, config.NewLocator(cfg), cmdFactory, catfileCache)

			setup := tc.setup(t, cfg)

			// Create some existing content in the staging directory so we can assert it gets removed and
			// recreated.
			for _, storage := range cfg.Storages {
				require.NoError(t,
					os.MkdirAll(
						filepath.Join(stagingDirectoryPath(storage.Path), "existing-content"),
						perm.PrivateDir,
					),
				)
			}

			partitionManager, err := NewPartitionManager(cfg.Storages, cmdFactory, localRepoFactory, logger, DatabaseOpenerFunc(OpenDatabase), helper.NewNullTickerFactory(), cfg.Prometheus)
			require.NoError(t, err)

			if setup.transactionManagerFactory != nil {
				partitionManager.transactionManagerFactory = setup.transactionManagerFactory
			}

			defer func() {
				partitionManager.Close()
				for _, storage := range cfg.Storages {
					// Assert all staging directories have been emptied at the end.
					testhelper.RequireDirectoryState(t, storage.Path, "staging", testhelper.DirectoryState{
						"/staging": {Mode: umask.Mask(fs.ModeDir | perm.PrivateDir)},
					})
				}
			}()

			for _, storage := range cfg.Storages {
				// Assert the existing content in the staging directory was removed.
				testhelper.RequireDirectoryState(t, storage.Path, "staging", testhelper.DirectoryState{
					"/staging": {Mode: umask.Mask(fs.ModeDir | perm.PrivateDir)},
				})
			}

			// openTransactionData holds references to all transactions and its associated partition
			// created during the testcase.
			openTransactionData := map[int]*transactionData{}

			var partitionManagerStopped bool
			for _, step := range setup.steps {
				switch step := step.(type) {
				case begin:
					require.NotContains(t, openTransactionData, step.transactionID, "test error: transaction id reused in begin")

					beginCtx := ctx
					if step.ctx != nil {
						beginCtx = step.ctx
					}

					txn, err := partitionManager.Begin(beginCtx, step.repo.GetStorageName(), step.repo.GetRelativePath(), TransactionOptions{
						AlternateRelativePath: step.alternateRelativePath,
					})
					require.Equal(t, step.expectedError, err)

					blockOnPartitionClosing(t, partitionManager)
					checkExpectedState(t, cfg, partitionManager, step.expectedState)

					if err != nil {
						continue
					}

					storageMgr := partitionManager.storages[step.repo.GetStorageName()]
					storageMgr.mu.Lock()

					ptnID, err := storageMgr.partitionAssigner.getPartitionID(ctx, step.repo.GetRelativePath(), "")
					require.NoError(t, err)

					ptn := storageMgr.partitions[ptnID]
					storageMgr.mu.Unlock()

					openTransactionData[step.transactionID] = &transactionData{
						txn:        txn,
						storageMgr: storageMgr,
						ptn:        ptn,
					}
				case commit:
					require.Contains(t, openTransactionData, step.transactionID, "test error: transaction committed before being started")

					data := openTransactionData[step.transactionID]

					commitCtx := ctx
					if step.ctx != nil {
						commitCtx = step.ctx
					}

					require.ErrorIs(t, data.txn.Commit(commitCtx), step.expectedError)

					blockOnPartitionClosing(t, partitionManager)
					checkExpectedState(t, cfg, partitionManager, step.expectedState)
				case rollback:
					require.Contains(t, openTransactionData, step.transactionID, "test error: transaction rolled back before being started")

					data := openTransactionData[step.transactionID]
					require.ErrorIs(t, data.txn.Rollback(), step.expectedError)

					blockOnPartitionClosing(t, partitionManager)
					checkExpectedState(t, cfg, partitionManager, step.expectedState)
				case closePartition:
					require.Contains(t, openTransactionData, step.transactionID, "test error: transaction manager stopped before being started")

					data := openTransactionData[step.transactionID]
					// Close the TransactionManager directly. Closing through the partition would change the
					// state used to sync which should only be changed when the closing is initiated through
					// the normal means.
					data.ptn.transactionManager.Close()

					blockOnPartitionClosing(t, partitionManager)
				case finalizeTransaction:
					require.Contains(t, openTransactionData, step.transactionID, "test error: transaction finalized before being started")

					data := openTransactionData[step.transactionID]

					data.storageMgr.finalizeTransaction(data.ptn)
				case closeManager:
					require.False(t, partitionManagerStopped, "test error: partition manager already stopped")
					partitionManagerStopped = true

					partitionManager.Close()
				}
			}
		})
	}
}

type dbWrapper struct {
	Database
	runValueLogGC func(float64) error
}

func (db dbWrapper) RunValueLogGC(discardRatio float64) error {
	return db.runValueLogGC(discardRatio)
}

func TestPartitionManager_garbageCollection(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)

	logger := testhelper.NewLogger(t)
	loggerHook := testhelper.AddLoggerHook(logger)

	cmdFactory := gittest.NewCommandFactory(t, cfg)
	catfileCache := catfile.NewCache(cfg)
	defer catfileCache.Stop()

	localRepoFactory := localrepo.NewFactory(logger, config.NewLocator(cfg), cmdFactory, catfileCache)

	gcRunCount := 0
	gcCompleted := make(chan struct{})
	errExpected := errors.New("some gc failure")

	partitionManager, err := NewPartitionManager(
		cfg.Storages,
		cmdFactory,
		localRepoFactory,
		logger,
		DatabaseOpenerFunc(func(logger log.Logger, path string) (Database, error) {
			db, err := OpenDatabase(logger, path)
			return dbWrapper{
				Database: db,
				runValueLogGC: func(discardRatio float64) error {
					gcRunCount++
					if gcRunCount < 3 {
						return nil
					}

					if gcRunCount == 3 {
						return badger.ErrNoRewrite
					}

					return errExpected
				},
			}, err
		}),
		helper.TickerFactoryFunc(func() helper.Ticker {
			return helper.NewCountTicker(1, func() {
				close(gcCompleted)
			})
		}),
		cfg.Prometheus,
	)
	require.NoError(t, err)
	defer partitionManager.Close()

	// The ticker has exhausted and we've performed the two GC runs we wanted to test.
	<-gcCompleted

	// Close the manager to ensure the GC goroutine also stops.
	partitionManager.Close()

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

	// After the second round, the PartititionManager is closed and we assert that the
	// garbage collection goroutine has also stopped.
	require.Equal(t, "value log garbage collection goroutine stopped", gcLogs[7].Message)
}

func TestPartitionManager_concurrentClose(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)
	logger := testhelper.SharedLogger(t)

	cmdFactory := gittest.NewCommandFactory(t, cfg)
	catfileCache := catfile.NewCache(cfg)
	defer catfileCache.Stop()

	localRepoFactory := localrepo.NewFactory(logger, config.NewLocator(cfg), cmdFactory, catfileCache)

	partitionManager, err := NewPartitionManager(cfg.Storages, cmdFactory, localRepoFactory, logger, DatabaseOpenerFunc(OpenDatabase), helper.NewNullTickerFactory(), cfg.Prometheus)
	require.NoError(t, err)
	defer partitionManager.Close()

	tx, err := partitionManager.Begin(ctx, cfg.Storages[0].Name, "relative-path", TransactionOptions{})
	require.NoError(t, err)

	start := make(chan struct{})

	var wg sync.WaitGroup
	wg.Add(3)

	// The last active transaction finishing will close partition.
	go func() {
		defer wg.Done()
		<-start
		assert.NoError(t, tx.Rollback())
	}()

	// PartitionManager may be closed if the server is shutting down.
	go func() {
		defer wg.Done()
		<-start
		partitionManager.Close()
	}()

	// The TransactionManager may return if it errors out.
	txMgr := partitionManager.storages[cfg.Storages[0].Name].partitions[1].transactionManager
	go func() {
		defer wg.Done()
		<-start
		txMgr.Close()
	}()

	close(start)

	wg.Wait()
}
