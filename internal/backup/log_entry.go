package backup

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/archive"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
)

const (
	// minRetryWait is the shortest duration to backoff before retrying, and is also the initial backoff duration.
	minRetryWait = 5 * time.Second
	// maxRetryWait is the longest duration to backoff before retrying.
	maxRetryWait = 5 * time.Minute
)

// logEntry is used to track the state of a backup request.
type logEntry struct {
	partitionInfo partitionInfo
	lsn           storage.LSN
	logManager    storagemgr.LogManager
	success       bool
}

// newLogEntry constructs a new logEntry.
func newLogEntry(partitionInfo partitionInfo, lsn storage.LSN, mgr storagemgr.LogManager) *logEntry {
	return &logEntry{
		partitionInfo: partitionInfo,
		lsn:           lsn,
		logManager:    mgr,
	}
}

// partitionNotification is used to store the data received by NotifyNewTransactions.
type partitionNotification struct {
	lowWaterMark  storage.LSN
	highWaterMark storage.LSN
	partitionInfo partitionInfo
	mgr           storagemgr.LogManager
}

// newPartitionNotification constructs a new partitionNotification.
func newPartitionNotification(storageName string, partitionID storage.PartitionID, lowWaterMark, highWaterMark storage.LSN, mgr storagemgr.LogManager) *partitionNotification {
	return &partitionNotification{
		partitionInfo: partitionInfo{
			storageName: storageName,
			partitionID: partitionID,
		},
		lowWaterMark:  lowWaterMark,
		highWaterMark: highWaterMark,
		mgr:           mgr,
	}
}

// partitionState tracks the progress made on one partition.
type partitionState struct {
	// nextLSN is the next LSN to be backed up.
	nextLSN storage.LSN
	// highWaterMark is the highest LSN to be backed up.
	highWaterMark storage.LSN
	// logManager is the LogManager for this partition.
	logManager storagemgr.LogManager
	// hasJob indicates if a backup job is currently being processed for this partition.
	hasJob bool
}

// newPartitionState constructs a new partitionState.
func newPartitionState(nextLSN, highWaterMark storage.LSN, logManager storagemgr.LogManager) *partitionState {
	return &partitionState{
		nextLSN:       nextLSN,
		highWaterMark: highWaterMark,
		logManager:    logManager,
	}
}

// partitionInfo is the global identifier for a partition.
type partitionInfo struct {
	storageName string
	partitionID storage.PartitionID
}

// LogEntryArchiver is used to backup applied log entries. It has a configurable number of
// worker goroutines that will perform backups. Each partition may only have one backup
// executing at a time, entries are always processed in-order. Backup failures will trigger
// an exponential backoff.
type LogEntryArchiver struct {
	// logger is the logger to use to write log messages.
	logger log.Logger
	// archiveSink is the Sink used to backup log entries.
	archiveSink Sink

	// notificationCh is the channel used to signal that a new notification has arrived.
	notificationCh chan struct{}
	// workCh is the channel used to signal that the archiver should try to process more jobs.
	workCh chan struct{}
	// doneCh is the channel used to signal that the LogEntryArchiver should exit.
	doneCh chan struct{}

	// notifications is the list of log notifications to ingest. notificationsMutex must be held when accessing it.
	notifications *list.List
	// notificationsMutex is used to synchronize access to notifications.
	notificationsMutex sync.Mutex

	// partitionStates tracks the current LSN and entry backlog of each partition in a storage.
	partitionStates map[partitionInfo]*partitionState
	// activePartitions tracks with partitions need to be processed.
	activePartitions map[partitionInfo]struct{}

	// activeJobs tracks how many entries are currently being backed up.
	activeJobs uint
	// workerCount sets the number of goroutines used to perform backups.
	workerCount uint

	// waitDur controls how long to wait before retrying when a backup attempt fails.
	waitDur time.Duration
	// tickerFunc allows the archiver to wait with an exponential backoff between retries.
	tickerFunc func(time.Duration) helper.Ticker

	// backupCounter provides metrics with a count of the number of WAL entries backed up by status.
	backupCounter *prometheus.CounterVec
	// backupLatency provides metrics on the latency of WAL backup operations.
	backupLatency prometheus.Histogram
}

// NewLogEntryArchiver constructs a new LogEntryArchiver.
func NewLogEntryArchiver(logger log.Logger, archiveSink Sink, workerCount uint) *LogEntryArchiver {
	return newLogEntryArchiver(logger, archiveSink, workerCount, helper.NewTimerTicker)
}

// newLogEntryArchiver constructs a new LogEntryArchiver with a configurable ticker function.
func newLogEntryArchiver(logger log.Logger, archiveSink Sink, workerCount uint, tickerFunc func(time.Duration) helper.Ticker) *LogEntryArchiver {
	if workerCount < 1 {
		workerCount = 1
	}

	archiver := &LogEntryArchiver{
		logger:           logger,
		archiveSink:      archiveSink,
		notificationCh:   make(chan struct{}, 1),
		workCh:           make(chan struct{}, 1),
		doneCh:           make(chan struct{}),
		notifications:    list.New(),
		partitionStates:  make(map[partitionInfo]*partitionState),
		activePartitions: make(map[partitionInfo]struct{}),
		workerCount:      workerCount,
		tickerFunc:       tickerFunc,
		waitDur:          minRetryWait,
		backupCounter: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "gitaly_wal_backup_count",
				Help: "Counter of the number of WAL entries backed up by status",
			},
			[]string{"status"},
		),
		backupLatency: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Name: "gitaly_wal_backup_latency_seconds",
				Help: "Latency of WAL entry backups",
			},
		),
	}

	return archiver
}

// NotifyNewTransactions passes the transaction information to the LogEntryArchiver for processing.
func (la *LogEntryArchiver) NotifyNewTransactions(storageName string, partitionID storage.PartitionID, lowWaterMark, highWaterMark storage.LSN, mgr storagemgr.LogManager) {
	la.notificationsMutex.Lock()
	defer la.notificationsMutex.Unlock()

	la.notifications.PushBack(newPartitionNotification(storageName, partitionID, lowWaterMark, highWaterMark, mgr))

	select {
	case la.notificationCh <- struct{}{}:
	// Archiver has a pending notification already, no further action needed.
	default:
	}
}

// Run starts log entry archiving.
func (la *LogEntryArchiver) Run() {
	go func() {
		la.logger.Info("log entry archiver: started")
		defer func() {
			la.notificationsMutex.Lock()
			defer la.notificationsMutex.Unlock()
			la.logger.WithField("pending_entries", la.notifications.Len()).Info("log entry archiver: stopped")
		}()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		sendCh := make(chan *logEntry)
		recvCh := make(chan *logEntry)

		var wg sync.WaitGroup
		for i := uint(0); i < la.workerCount; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				la.processEntries(ctx, sendCh, recvCh)
			}()
		}

		la.main(sendCh, recvCh)

		close(sendCh)

		// Interrupt any running backups so we can exit quickly.
		cancel()

		// Wait for all workers to exit.
		wg.Wait()
	}()
}

// Close stops the LogEntryArchiver, causing Run to return.
func (la *LogEntryArchiver) Close() {
	close(la.doneCh)
}

// main is the main loop of the LogEntryArchiver. New notifications are ingested, jobs
// are sent to workers, and the result of jobs are received.
func (la *LogEntryArchiver) main(sendCh, recvCh chan *logEntry) {
	for {
		// Triggering sendEntries via workCh may not process all entries if there
		// are more active partitions than workers or more than one entry to process
		// in a partition. We will need to call it repeatedly to work through the
		// backlog. If there are no available jobs or workers sendEntries is a no-op.
		la.sendEntries(sendCh)

		select {
		case <-la.workCh:
			la.sendEntries(sendCh)
		case <-la.notificationCh:
			la.ingestNotifications()
		case entry := <-recvCh:
			la.receiveEntry(entry)
		case <-la.doneCh:
			return
		}
	}
}

// sendEntries sends available log entries to worker goroutines for processing.
// It may consume up to as many entries as there are available workers.
func (la *LogEntryArchiver) sendEntries(sendCh chan *logEntry) {
	// We use a map to randomize partition processing order. Map access is not
	// truly random or completely fair, but it's close enough for our purposes.
	for partitionInfo := range la.activePartitions {
		// All workers are busy, go back to waiting.
		if la.activeJobs == la.workerCount {
			return
		}

		state := la.partitionStates[partitionInfo]
		if state.hasJob {
			continue
		}

		state.hasJob = true
		sendCh <- newLogEntry(partitionInfo, state.nextLSN, state.logManager)

		la.activeJobs++
	}
}

// ingestNotifications read all new notifications and updates partition states.
func (la *LogEntryArchiver) ingestNotifications() {
	for {
		notification := la.popNextNotification()
		if notification == nil {
			return
		}

		state, ok := la.partitionStates[notification.partitionInfo]
		if !ok {
			state = newPartitionState(notification.lowWaterMark, notification.highWaterMark, notification.mgr)
			la.partitionStates[notification.partitionInfo] = state
		}

		// We have already backed up all entries sent by the LogManager, but the manager is
		// not aware of this. Acknowledge again with our last processed entry.
		if state.nextLSN > notification.highWaterMark {
			notification.mgr.AcknowledgeTransaction(la, state.nextLSN-1)
			continue
		}

		// We expect our next LSN to be at or above the oldest LSN available for backup. If not,
		// we will be unable to backup the full sequence.
		if state.nextLSN < notification.lowWaterMark {
			la.logger.WithFields(
				log.Fields{
					"storage":      notification.partitionInfo.storageName,
					"partition_id": notification.partitionInfo.partitionID,
					"expected_lsn": state.nextLSN,
					"actual_lsn":   notification.lowWaterMark,
				}).Error("log entry archiver: gap in log sequence")

			// The LogManager reports that it no longer has our expected
			// LSN available for consumption. Skip ahead to the oldest entry
			// still present.
			state.nextLSN = notification.lowWaterMark
		}

		state.highWaterMark = notification.highWaterMark

		// LogManagers may close and restart. Always store the most recent
		// one to maximize our chances of reaching an active manager.
		state.logManager = notification.mgr

		// Mark partition as active.
		la.activePartitions[notification.partitionInfo] = struct{}{}

		la.notifyNewEntries()
	}
}

// receiveEntry handles the result of a backup job. If the backup failed, then it
// will block for la.waitDur to allow the conditions that caused the failure to resolve
// themselves. Continued failure results in an exponential backoff.
func (la *LogEntryArchiver) receiveEntry(entry *logEntry) {
	la.activeJobs--

	state := la.partitionStates[entry.partitionInfo]
	state.hasJob = false

	if !entry.success {
		// It is likely that a problem with one backup will impact others, e.g.
		// connectivity issues with object storage. Wait to avoid a thundering
		// herd of retries.
		la.backoff()

		return
	}

	entry.logManager.AcknowledgeTransaction(la, entry.lsn)

	state.nextLSN++

	// All entries in partition have been backed up, the partition is dormant.
	if state.nextLSN > state.highWaterMark {
		delete(la.activePartitions, entry.partitionInfo)
	}

	// Decrease backoff on success.
	la.waitDur /= 2
	if la.waitDur < minRetryWait {
		la.waitDur = minRetryWait
	}
}

// processEntries is executed by worker goroutines. This performs the actual backups.
func (la *LogEntryArchiver) processEntries(ctx context.Context, inCh, outCh chan *logEntry) {
	for entry := range inCh {
		la.processEntry(ctx, entry)
		outCh <- entry
	}
}

// processEntry checks if an existing backup exists, and performs a backup if not present.
func (la *LogEntryArchiver) processEntry(ctx context.Context, entry *logEntry) {
	entryPath := entry.logManager.GetTransactionPath(entry.lsn)
	archiveRelPath := filepath.Join(entry.partitionInfo.storageName, fmt.Sprintf("%d", entry.partitionInfo.partitionID), entry.lsn.String()+".tar")

	logger := la.logger.WithFields(log.Fields{
		"storage":      entry.partitionInfo.storageName,
		"partition_id": entry.partitionInfo.partitionID,
		"lsn":          entry.lsn,
	})

	backupExists, err := la.checkForExistingBackup(ctx, archiveRelPath)
	if err != nil {
		la.backupCounter.WithLabelValues("fail").Add(1)
		logger.WithError(err).Error("log entry archiver: checking for existing log entry backup")
		return
	}
	if backupExists {
		// Don't increment backupCounter, we didn't perform a backup.
		entry.success = true
		return
	}

	if err := la.backupLogEntry(ctx, archiveRelPath, entryPath); err != nil {
		la.backupCounter.WithLabelValues("fail").Add(1)
		logger.WithError(err).Error("log entry archiver: failed to backup log entry")
		return
	}

	la.backupCounter.WithLabelValues("success").Add(1)

	entry.success = true
}

// backupLogEntry tar's the root directory of the transaction and writes it to the Sink.
func (la *LogEntryArchiver) backupLogEntry(ctx context.Context, archiveRelPath string, entryPath string) (returnErr error) {
	timer := prometheus.NewTimer(la.backupLatency)
	defer timer.ObserveDuration()

	// Create a new context to abort the write on failure.
	writeCtx, cancelWrite := context.WithCancel(ctx)
	defer cancelWrite()

	w, err := la.archiveSink.GetWriter(writeCtx, archiveRelPath)
	if err != nil {
		return fmt.Errorf("get backup writer: %w", err)
	}
	defer func() {
		if err := w.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("close log entry backup writer: %w", err)
		}
	}()

	entryParent := filepath.Dir(entryPath)
	entryName := filepath.Base(entryPath)

	if err := archive.WriteTarball(writeCtx, la.logger, w, entryParent, entryName); err != nil {
		// End the context before calling Close to ensure we don't persist the failed
		// write to object storage.
		cancelWrite()
		return fmt.Errorf("backup log archive: %w", err)
	}

	return nil
}

// checkForExistingBackup checks if a non-zero sized file or blob exists at the archive path
// of the log entry.
func (la *LogEntryArchiver) checkForExistingBackup(ctx context.Context, archiveRelPath string) (exists bool, returnErr error) {
	r, err := la.archiveSink.GetReader(ctx, archiveRelPath)
	if err != nil {
		if errors.Is(err, ErrDoesntExist) {
			return false, nil
		}
		return false, fmt.Errorf("get log entry backup reader: %w", err)
	}

	if err := r.Close(); err != nil {
		return false, fmt.Errorf("close log entry backup reader: %w", err)
	}

	return true, nil
}

// popNextNotification removes the next entry from the head of the list.
func (la *LogEntryArchiver) popNextNotification() *partitionNotification {
	la.notificationsMutex.Lock()
	defer la.notificationsMutex.Unlock()

	front := la.notifications.Front()
	if front == nil {
		return nil
	}

	return la.notifications.Remove(front).(*partitionNotification)
}

// notifyNewEntries alerts the LogEntryArchiver that new entries are available to backup.
func (la *LogEntryArchiver) notifyNewEntries() {
	select {
	case la.workCh <- struct{}{}:
		// There is already a pending notification, proceed.
	default:
	}
}

// backoff sleeps for waitDur and doubles the duration for the next backoff call.
func (la *LogEntryArchiver) backoff() {
	ticker := la.tickerFunc(la.waitDur)
	ticker.Reset()

	select {
	case <-la.doneCh:
		ticker.Stop()
	case <-ticker.C():
	}

	la.waitDur *= 2
	if la.waitDur > maxRetryWait {
		la.waitDur = maxRetryWait
	}
}

// Describe is used to describe Prometheus metrics.
func (la *LogEntryArchiver) Describe(descs chan<- *prometheus.Desc) {
	prometheus.DescribeByCollect(la, descs)
}

// Collect is used to collect Prometheus metrics.
func (la *LogEntryArchiver) Collect(metrics chan<- prometheus.Metric) {
	la.backupCounter.Collect(metrics)
}
