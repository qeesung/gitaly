package backup

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

type mockLogManager struct {
	partitionID   storage.PartitionID
	archiver      *LogEntryArchiver
	notifications []notification
	acknowledged  []storage.LSN
	finalLSN      storage.LSN
	entryRootPath string
	finishFunc    func()

	sync.Mutex
}

func (lm *mockLogManager) AcknowledgeTransaction(_ storagemgr.LogConsumer, lsn storage.LSN) {
	lm.Lock()
	defer lm.Unlock()

	lm.acknowledged = append(lm.acknowledged, lsn)

	// If the archiver has completed enough entries to reach our new low water mark,
	// send the next notification.
	if len(lm.notifications) > 0 && lsn >= lm.notifications[0].sendAt {
		lm.SendNotification()
	}

	if lsn == lm.finalLSN && len(lm.notifications) == 0 {
		lm.finishFunc()
	}
}

func (lm *mockLogManager) SendNotification() {
	n := lm.notifications[0]
	lm.archiver.NotifyNewTransactions(lm.partitionID, n.lowWaterMark, n.highWaterMark, lm)

	lm.notifications = lm.notifications[1:]
}

func (lm *mockLogManager) GetTransactionPath(lsn storage.LSN) string {
	return filepath.Join(lm.entryRootPath, fmt.Sprintf("%d", lm.partitionID), lsn.String())
}

type notification struct {
	lowWaterMark, highWaterMark, sendAt storage.LSN
}

func TestLogEntryArchiver(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc                string
		workerCount         int
		partitions          []storage.PartitionID
		notifications       []notification
		waitCount           int
		finalLSN            storage.LSN
		expectedBackupCount int
		expectedLogMessage  string
	}{
		{
			desc:        "notify one entry",
			workerCount: 1,
			partitions:  []storage.PartitionID{1},
			notifications: []notification{
				{
					lowWaterMark:  1,
					highWaterMark: 1,
				},
			},
			finalLSN:            1,
			expectedBackupCount: 1,
		},
		{
			desc:        "start from later LSN",
			workerCount: 1,
			partitions:  []storage.PartitionID{1},
			notifications: []notification{
				{
					lowWaterMark:  11,
					highWaterMark: 11,
				},
			},
			finalLSN:            11,
			expectedBackupCount: 1,
		},
		{
			desc:        "notify range of entries",
			workerCount: 1,
			partitions:  []storage.PartitionID{1},
			notifications: []notification{
				{
					lowWaterMark:  1,
					highWaterMark: 5,
				},
			},
			finalLSN:            5,
			expectedBackupCount: 5,
		},
		{
			desc:        "increasing high water mark",
			workerCount: 1,
			partitions:  []storage.PartitionID{1},
			notifications: []notification{
				{
					lowWaterMark:  1,
					highWaterMark: 5,
				},
				{
					lowWaterMark:  1,
					highWaterMark: 6,
				},
				{
					lowWaterMark:  1,
					highWaterMark: 7,
				},
			},
			finalLSN:            7,
			expectedBackupCount: 7,
		},
		{
			desc:        "increasing low water mark",
			workerCount: 1,
			partitions:  []storage.PartitionID{1},
			notifications: []notification{
				{
					lowWaterMark:  1,
					highWaterMark: 5,
				},
				{
					lowWaterMark:  2,
					highWaterMark: 5,
					sendAt:        2,
				},
				{
					lowWaterMark:  3,
					highWaterMark: 5,
					sendAt:        3,
				},
			},
			finalLSN:            5,
			expectedBackupCount: 5,
		},
		{
			desc:        "multiple partitions",
			workerCount: 1,
			partitions:  []storage.PartitionID{1, 2, 3},
			notifications: []notification{
				{
					lowWaterMark:  1,
					highWaterMark: 3,
				},
			},
			finalLSN:            3,
			expectedBackupCount: 9,
		},
		{
			desc:        "multiple partitions, multi-threaded",
			workerCount: 4,
			partitions:  []storage.PartitionID{1, 2, 3},
			notifications: []notification{
				{
					lowWaterMark:  1,
					highWaterMark: 3,
				},
			},
			finalLSN:            3,
			expectedBackupCount: 9,
		},
		{
			desc:        "resent items processed once",
			workerCount: 1,
			partitions:  []storage.PartitionID{1},
			notifications: []notification{
				{
					lowWaterMark:  1,
					highWaterMark: 3,
				},
				{
					lowWaterMark:  1,
					highWaterMark: 3,
					sendAt:        3,
				},
			},
			waitCount:           2,
			finalLSN:            3,
			expectedBackupCount: 3,
		},
		{
			desc:        "gap in sequence",
			workerCount: 1,
			partitions:  []storage.PartitionID{1},
			notifications: []notification{
				{
					lowWaterMark:  1,
					highWaterMark: 3,
				},
				{
					lowWaterMark:  11,
					highWaterMark: 13,
					sendAt:        3,
				},
			},
			finalLSN:            13,
			expectedBackupCount: 6,
			expectedLogMessage:  "log entry archiver: gap in log sequence",
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			umask := testhelper.Umask()

			entryRootPath := testhelper.TempDir(t)

			archivePath := testhelper.TempDir(t)
			archiveSink, err := ResolveSink(ctx, archivePath)
			require.NoError(t, err)

			logger := testhelper.NewLogger(t)
			hook := testhelper.AddLoggerHook(logger)
			var wg sync.WaitGroup

			archiver := NewLogEntryArchiver(logger, archiveSink, tc.workerCount)
			archiver.Run()
			defer archiver.Close()

			managers := make(map[storage.PartitionID]*mockLogManager, len(tc.partitions))
			for _, partitionID := range tc.partitions {
				manager := &mockLogManager{
					partitionID:   partitionID,
					entryRootPath: entryRootPath,
					archiver:      archiver,
					notifications: tc.notifications,
					finalLSN:      tc.finalLSN,
					finishFunc:    wg.Done,
				}
				managers[partitionID] = manager

				partitionID := partitionID

				waitCount := tc.waitCount
				if waitCount == 0 {
					waitCount = 1
				}
				wg.Add(waitCount)

				// Send partitions in parallel to mimic real usage.
				go func() {
					sentLSNs := make([]storage.LSN, 0, tc.finalLSN)
					for _, notification := range tc.notifications {
						for lsn := notification.lowWaterMark; lsn <= notification.highWaterMark; lsn++ {
							// Don't recreate entries.
							if !slices.Contains(sentLSNs, lsn) {
								createEntryDir(t, entryRootPath, partitionID, lsn)
							}

							sentLSNs = append(sentLSNs, lsn)
						}
					}
					manager.Lock()
					defer manager.Unlock()
					manager.SendNotification()
				}()
			}

			wg.Wait()

			cmpDir := testhelper.TempDir(t)
			for partitionID, manager := range managers {
				lastAck := manager.acknowledged[len(manager.acknowledged)-1]
				require.Equal(t, tc.finalLSN, lastAck)

				partitionDir := filepath.Join(cmpDir, fmt.Sprintf("%d", partitionID))
				require.NoError(t, os.Mkdir(partitionDir, perm.PrivateDir))

				for _, lsn := range manager.acknowledged {
					tarPath := filepath.Join(archivePath, fmt.Sprintf("%d", partitionID), lsn.String()+".tar")
					archive, err := os.Open(tarPath)
					require.NoError(t, err)
					testhelper.RequireTarState(t, archive, testhelper.DirectoryState{
						lsn.String() + "/":                          {Mode: umask.Mask(perm.PrivateDir)},
						filepath.Join(lsn.String(), "LSN"):          {Mode: umask.Mask(perm.PrivateFile), Content: []byte(lsn.String())},
						filepath.Join(lsn.String(), "PARTITION_ID"): {Mode: umask.Mask(perm.PrivateFile), Content: []byte(fmt.Sprintf("%d", partitionID))},
					})
				}
			}

			if tc.expectedLogMessage != "" {
				require.Equal(t, tc.expectedLogMessage, hook.LastEntry().Message)
			}

			require.NoError(t, testutil.CollectAndCompare(
				archiver.backupCounter,
				buildMetrics(t, tc.expectedBackupCount, 0),
				"gitaly_wal_backup_count"))
		})
	}
}

func TestLogEntryArchiver_retry(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(testhelper.Context(t))
	defer cancel()

	umask := testhelper.Umask()

	const partitionID = 1
	const lsn = storage.LSN(1)
	const workerCount = 1

	entryRootPath := testhelper.TempDir(t)

	var wg sync.WaitGroup

	logger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(logger)
	ticker := helper.NewManualTicker()

	// Finish waiting when logEntry.backoff is hit after a failure.
	tickerFunc := func(time.Duration) helper.Ticker {
		wg.Done()
		return ticker
	}

	archivePath := testhelper.TempDir(t)
	archiveSink, err := ResolveSink(ctx, archivePath)
	require.NoError(t, err)

	archiver := newLogEntryArchiver(logger, archiveSink, workerCount, tickerFunc)
	archiver.Run()
	defer archiver.Close()

	manager := &mockLogManager{
		partitionID:   partitionID,
		entryRootPath: entryRootPath,
		archiver:      archiver,
		notifications: []notification{
			{
				lowWaterMark:  1,
				highWaterMark: 1,
			},
		},
		finalLSN:   1,
		finishFunc: wg.Done,
	}

	wg.Add(1)
	manager.SendNotification()

	// Wait for initial request to fail and backoff.
	wg.Wait()

	require.Equal(t, "log entry archiver: failed to backup log entry", hook.LastEntry().Message)

	// Create entry so retry will succeed.
	createEntryDir(t, entryRootPath, partitionID, lsn)

	// Add to wg, this will be decremented when the retry completes.
	wg.Add(1)

	// Finish backoff.
	ticker.Tick()

	// Wait for retry to complete.
	wg.Wait()

	cmpDir := testhelper.TempDir(t)
	partitionDir := filepath.Join(cmpDir, fmt.Sprintf("%d", partitionID))
	require.NoError(t, os.Mkdir(partitionDir, perm.PrivateDir))

	tarPath := filepath.Join(archivePath, fmt.Sprintf("%d", partitionID), lsn.String()+".tar")
	archive, err := os.Open(tarPath)
	require.NoError(t, err)

	testhelper.RequireTarState(t, archive, testhelper.DirectoryState{
		lsn.String() + "/":                          {Mode: umask.Mask(perm.PrivateDir)},
		filepath.Join(lsn.String(), "LSN"):          {Mode: umask.Mask(perm.PrivateFile), Content: []byte(lsn.String())},
		filepath.Join(lsn.String(), "PARTITION_ID"): {Mode: umask.Mask(perm.PrivateFile), Content: []byte(fmt.Sprintf("%d", partitionID))},
	})

	require.NoError(t, testutil.CollectAndCompare(
		archiver.backupCounter,
		buildMetrics(t, 1, 1),
		"gitaly_wal_backup_count"))
}

func createEntryDir(t *testing.T, entryRootPath string, partitionID storage.PartitionID, lsn storage.LSN) {
	t.Helper()

	partitionPath := filepath.Join(entryRootPath, fmt.Sprintf("%d", partitionID))
	require.NoError(t, os.MkdirAll(partitionPath, perm.PrivateDir))

	testhelper.CreateFS(t, filepath.Join(partitionPath, lsn.String()), fstest.MapFS{
		".":            {Mode: fs.ModeDir | perm.PrivateDir},
		"LSN":          {Mode: perm.PrivateFile, Data: []byte(lsn.String())},
		"PARTITION_ID": {Mode: perm.PrivateFile, Data: []byte(fmt.Sprintf("%d", partitionID))},
	})
}

func buildMetrics(t *testing.T, successCt, failCt int) *strings.Reader {
	t.Helper()

	var builder strings.Builder
	_, err := builder.WriteString("# HELP gitaly_wal_backup_count Counter of the number of WAL entries backed up by status\n")
	require.NoError(t, err)
	_, err = builder.WriteString("# TYPE gitaly_wal_backup_count counter\n")
	require.NoError(t, err)

	_, err = builder.WriteString(
		fmt.Sprintf("gitaly_wal_backup_count{status=\"success\"} %d\n", successCt))
	require.NoError(t, err)

	if failCt > 0 {
		_, err = builder.WriteString(
			fmt.Sprintf("gitaly_wal_backup_count{status=\"fail\"} %d\n", failCt))
		require.NoError(t, err)
	}

	return strings.NewReader(builder.String())
}
