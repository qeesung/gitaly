package storagemgr

import (
	"bufio"
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dgraph-io/badger/v4"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	housekeepingcfg "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/repoutil"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/wal"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/tracing"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"
)

var (
	// ErrRepositoryAlreadyExists is attempting to create a repository that already exists.
	ErrRepositoryAlreadyExists = structerr.NewAlreadyExists("repository already exists")
	// ErrRepositoryNotFound is returned when the repository doesn't exist.
	ErrRepositoryNotFound = structerr.NewNotFound("repository not found")
	// ErrTransactionProcessingStopped is returned when the TransactionManager stops processing transactions.
	ErrTransactionProcessingStopped = errors.New("transaction processing stopped")
	// ErrTransactionAlreadyCommitted is returned when attempting to rollback or commit a transaction that
	// already had commit called on it.
	ErrTransactionAlreadyCommitted = errors.New("transaction already committed")
	// ErrTransactionAlreadyRollbacked is returned when attempting to rollback or commit a transaction that
	// already had rollback called on it.
	ErrTransactionAlreadyRollbacked = errors.New("transaction already rollbacked")
	// errInitializationFailed is returned when the TransactionManager failed to initialize successfully.
	errInitializationFailed = errors.New("initializing transaction processing failed")
	// errCommittedEntryGone is returned when the log entry of a LSN is gone from database while it's still
	// accessed by other transactions.
	errCommittedEntryGone = errors.New("in-used committed entry is gone")
	// errNotDirectory is returned when the repository's path doesn't point to a directory
	errNotDirectory = errors.New("repository's path didn't point to a directory")
	// errRelativePathNotSet is returned when a transaction is begun without providing a relative path
	// of the target repository.
	errRelativePathNotSet = errors.New("relative path not set")
	// errAlternateAlreadyLinked is returned when attempting to set an alternate on a repository that
	// already has one.
	errAlternateAlreadyLinked = errors.New("repository already has an alternate")
	// errConflictRepositoryDeletion is returned when an operation conflicts with repository deletion in another
	// transaction.
	errConflictRepositoryDeletion = errors.New("detected an update conflicting with repository deletion")
	// errPackRefsConflictRefDeletion is returned when there is a committed ref deletion before pack-refs
	// task is committed. The transaction should be aborted.
	errPackRefsConflictRefDeletion = errors.New("detected a conflict with reference deletion when committing packed-refs")
	// errHousekeepingConflictOtherUpdates is returned when the transaction includes housekeeping alongside
	// with other updates.
	errHousekeepingConflictOtherUpdates = errors.New("housekeeping in the same transaction with other updates")
	// errHousekeepingConflictConcurrent is returned when there are another concurrent housekeeping task.
	errHousekeepingConflictConcurrent = errors.New("conflict with another concurrent housekeeping task")
	// errRepackConflictPrunedObject is returned when the repacking task pruned an object that is still used by other
	// concurrent transactions.
	errRepackConflictPrunedObject = errors.New("pruned object used by other updates")
	// errRepackNotSupportedStrategy is returned when the manager runs the repacking task using unsupported strategy.
	errRepackNotSupportedStrategy = errors.New("strategy not supported")

	// Below errors are used to error out in cases when updates have been staged in a read-only transaction.
	errReadOnlyReferenceUpdates    = errors.New("reference updates staged in a read-only transaction")
	errReadOnlyDefaultBranchUpdate = errors.New("default branch update staged in a read-only transaction")
	errReadOnlyCustomHooksUpdate   = errors.New("custom hooks update staged in a read-only transaction")
	errReadOnlyRepositoryDeletion  = errors.New("repository deletion staged in a read-only transaction")
	errReadOnlyObjectsIncluded     = errors.New("objects staged in a read-only transaction")
	errReadOnlyHousekeeping        = errors.New("housekeeping in a read-only transaction")
)

// InvalidReferenceFormatError is returned when a reference name was invalid.
type InvalidReferenceFormatError struct {
	// ReferenceName is the reference with invalid format.
	ReferenceName git.ReferenceName
}

// Error returns the formatted error string.
func (err InvalidReferenceFormatError) Error() string {
	return fmt.Sprintf("invalid reference format: %q", err.ReferenceName)
}

// ReferenceVerificationError is returned when a reference's old OID did not match the expected.
type ReferenceVerificationError struct {
	// ReferenceName is the name of the reference that failed verification.
	ReferenceName git.ReferenceName
	// ExpectedOID is the OID the reference was expected to point to.
	ExpectedOID git.ObjectID
	// ActualOID is the OID the reference actually pointed to.
	ActualOID git.ObjectID
}

// Error returns the formatted error string.
func (err ReferenceVerificationError) Error() string {
	return fmt.Sprintf("expected %q to point to %q but it pointed to %q", err.ReferenceName, err.ExpectedOID, err.ActualOID)
}

// ReferenceUpdate describes the state of a reference's old and new tip in an update.
type ReferenceUpdate struct {
	// OldOID is the old OID the reference is expected to point to prior to updating it.
	// If the reference does not point to the old value, the reference verification fails.
	OldOID git.ObjectID
	// NewOID is the new desired OID to point the reference to.
	NewOID git.ObjectID
}

// repositoryCreation models a repository creation in a transaction.
type repositoryCreation struct {
	// objectHash defines the object format the repository is created with.
	objectHash git.ObjectHash
}

// runHousekeeping models housekeeping tasks. It is supposed to handle housekeeping tasks for repositories
// such as the cleanup of unneeded files and optimizations for the repository's data structures.
type runHousekeeping struct {
	packRefs          *runPackRefs
	repack            *runRepack
	writeCommitGraphs *writeCommitGraphs
}

// runPackRefs models refs packing housekeeping task. It packs heads and tags for efficient repository access.
type runPackRefs struct {
	// PrunedRefs contain a list of references pruned by the `git-pack-refs` command. They are used
	// for comparing to the ref list of the destination repository
	PrunedRefs map[git.ReferenceName]struct{}
}

// runRepack models repack housekeeping task. We support multiple repacking strategies. At this stage, the outside
// scheduler determines which strategy to use. The transaction manager is responsible for executing it. In the future,
// we want to make housekeeping smarter by migrating housekeeping scheduling responsibility to this manager. That work
// is tracked in https://gitlab.com/gitlab-org/gitaly/-/issues/5709.
type runRepack struct {
	// config tells which strategy and baggaged options.
	config       housekeepingcfg.RepackObjectsConfig
	isFullRepack bool
	// newFiles contains the list of new packfiles to be applied into the destination repository.
	newFiles []string
	// deletedFiles contains the list of packfiles that should be removed in the destination repository. The repacking
	// command runs in the snapshot repository for a significant amount of time. Meanwhile, other requests
	// containing new objects can land and be applied before housekeeping task finishes. So, we keep a snapshot of
	// the packfile structure before and after running the task. When the manager applies the task, it targets those
	// known files only. Other packfiles can still co-exist along side with the resulting repacked packfile(s).
	deletedFiles []string
}

// writeCommitGraphs models a commit graph update.
type writeCommitGraphs struct {
	// config includes the configs for writing commit graph.
	config housekeepingcfg.WriteCommitGraphConfig
}

// ReferenceUpdates contains references to update. Reference name is used as the key and the value
// is the expected old tip and the desired new tip.
type ReferenceUpdates map[git.ReferenceName]ReferenceUpdate

type transactionState int

const (
	// transactionStateOpen indicates the transaction is open, and hasn't been committed or rolled back yet.
	transactionStateOpen = transactionState(iota)
	// transactionStateRollback indicates the transaction has been rolled back.
	transactionStateRollback
	// transactionStateCommit indicates the transaction has already been committed.
	transactionStateCommit
)

// Transaction is a unit-of-work that contains reference changes to perform on the repository.
type Transaction struct {
	// readOnly denotes whether or not this transaction is read-only.
	readOnly bool
	// repositoryExists indicates whether the target repository existed when this transaction began.
	repositoryExists bool

	// state records whether the transaction is still open. Transaction is open until either Commit()
	// or Rollback() is called on it.
	state transactionState
	// commit commits the Transaction through the TransactionManager.
	commit func(context.Context, *Transaction) error
	// result is where the outcome of the transaction is sent to by TransactionManager once it
	// has been determined.
	result chan error
	// admitted is set when the transaction was admitted for processing in the TransactionManager.
	// Transaction queues in admissionQueue to be committed, and is considered admitted once it has
	// been dequeued by TransactionManager.Run(). Once the transaction is admitted, its ownership moves
	// from the client goroutine to the TransactionManager.Run() goroutine, and the client goroutine must
	// not do any modifications to the state of the transaction anymore to avoid races.
	admitted bool
	// finish cleans up the transaction releasing the resources associated with it. It must be called
	// once the transaction is done with.
	finish func() error
	// finished is closed when the transaction has been finished. This enables waiting on transactions
	// to finish where needed.
	finished chan struct{}

	// relativePath is the relative path of the repository this transaction is targeting.
	relativePath string
	// stagingDirectory is the directory where the transaction stages its files prior
	// to them being logged. It is cleaned up when the transaction finishes.
	stagingDirectory string
	// quarantineDirectory is the directory within the stagingDirectory where the new objects of the
	// transaction are quarantined.
	quarantineDirectory string
	// packPrefix contains the prefix (`pack-<digest>`) of the transaction's pack if the transaction
	// had objects to log.
	packPrefix string
	// snapshotRepository is a snapshot of the target repository with a possible quarantine applied
	// if this is a read-write transaction.
	snapshotRepository *localrepo.Repo

	// snapshotLSN is the log sequence number which this transaction is reading the repository's
	// state at.
	snapshotLSN LSN
	// snapshot is the transaction's snapshot of the partition. It's used to rewrite relative paths to
	// point to the snapshot instead of the actual repositories.
	snapshot snapshot
	// stagingRepository is a repository that is used to stage the transaction. If there are quarantined
	// objects, it has the quarantine applied so the objects are available for verification and packing.
	// Generally the staging repository is the actual repository instance. If the repository doesn't exist
	// yet, the staging repository is a temporary repository that is deleted once the transaction has been
	// finished.
	stagingRepository *localrepo.Repo

	// walEntry is the log entry where the transaction stages its state for committing.
	walEntry                 *wal.Entry
	skipVerificationFailures bool
	initialReferenceValues   map[git.ReferenceName]git.ObjectID
	referenceUpdates         []ReferenceUpdates
	defaultBranchUpdated     bool
	customHooksUpdated       bool
	repositoryCreation       *repositoryCreation
	deleteRepository         bool
	includedObjects          map[git.ObjectID]struct{}
	alternateUpdated         bool
	runHousekeeping          *runHousekeeping
}

// Begin opens a new transaction. The caller must call either Commit or Rollback to release
// the resources tied to the transaction. The returned Transaction is not safe for concurrent use.
//
// The returned Transaction's read snapshot includes all writes that were committed prior to the
// Begin call. Begin blocks until the committed writes have been applied to the repository.
//
// relativePath is the relative path of the target repository the transaction is operating on.
//
// snapshottedRelativePaths are the relative paths to snapshot in addition to target repository.
// These are read-only as the transaction can only perform changes against the target repository.
//
// readOnly indicates whether this is a read-only transaction. Read-only transactions are not
// configured with a quarantine directory and do not commit a log entry.
func (mgr *TransactionManager) Begin(ctx context.Context, relativePath string, snapshottedRelativePaths []string, readOnly bool) (_ *Transaction, returnedErr error) {
	// Wait until the manager has been initialized so the notification channels
	// and the LSNs are loaded.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-mgr.initialized:
		if !mgr.initializationSuccessful {
			return nil, errInitializationFailed
		}
	}

	if relativePath == "" {
		// For now we don't have a use case for transactions that don't target a repository.
		// Until support is implemented, error out.
		return nil, errRelativePathNotSet
	}

	span, _ := tracing.StartSpanIfHasParent(ctx, "transaction.Begin", nil)
	span.SetTag("readonly", readOnly)
	span.SetTag("snapshotLSN", mgr.appendedLSN)
	span.SetTag("relativePath", relativePath)
	defer span.Finish()

	mgr.mutex.Lock()

	txn := &Transaction{
		readOnly:     readOnly,
		commit:       mgr.commit,
		snapshotLSN:  mgr.appendedLSN,
		finished:     make(chan struct{}),
		relativePath: relativePath,
	}

	mgr.snapshotLocks[txn.snapshotLSN].activeSnapshotters.Add(1)
	defer mgr.snapshotLocks[txn.snapshotLSN].activeSnapshotters.Done()
	readReady := mgr.snapshotLocks[txn.snapshotLSN].applied

	var entry *committedEntry
	if !txn.readOnly {
		var err error
		entry, err = mgr.updateCommittedEntry(txn.snapshotLSN)
		if err != nil {
			return nil, err
		}
	}

	mgr.mutex.Unlock()

	txn.finish = func() error {
		defer close(txn.finished)
		defer func() {
			if !txn.readOnly {
				// Signal the manager this transaction finishes. The purpose of this signaling is to wake it up
				// and clean up stale entries. If the manager is not listening to this channel, it is either
				// processing other transaction or applying appended log entries. In either cases, it will
				// circle back. So, no need to wait for the manager to pick up the signal.
				select {
				case mgr.completedQueue <- struct{}{}:
				default:
				}
			}
		}()

		if txn.stagingDirectory != "" {
			if err := os.RemoveAll(txn.stagingDirectory); err != nil {
				return fmt.Errorf("remove staging directory: %w", err)
			}
		}

		if !txn.readOnly {
			mgr.mutex.Lock()
			defer mgr.mutex.Unlock()
			if err := mgr.cleanCommittedEntry(entry); err != nil {
				return fmt.Errorf("cleaning committed entry: %w", err)
			}
		}

		return nil
	}

	defer func() {
		if returnedErr != nil {
			if err := txn.finish(); err != nil {
				mgr.logger.WithError(err).ErrorContext(ctx, "failed finishing unsuccessful transaction begin")
			}
		}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-mgr.ctx.Done():
		return nil, ErrTransactionProcessingStopped
	case <-readReady:
		var err error
		txn.stagingDirectory, err = os.MkdirTemp(mgr.stagingDirectory, "")
		if err != nil {
			return nil, fmt.Errorf("mkdir temp: %w", err)
		}

		if txn.snapshot, err = newSnapshot(ctx,
			mgr.storagePath,
			filepath.Join(txn.stagingDirectory, "snapshot"),
			append(snapshottedRelativePaths, txn.relativePath),
		); err != nil {
			return nil, fmt.Errorf("new snapshot: %w", err)
		}

		txn.repositoryExists, err = mgr.doesRepositoryExist(txn.snapshot.relativePath(txn.relativePath))
		if err != nil {
			return nil, fmt.Errorf("does repository exist: %w", err)
		}

		txn.snapshotRepository = mgr.repositoryFactory.Build(txn.snapshot.relativePath(txn.relativePath))
		if !txn.readOnly {
			if txn.repositoryExists {
				txn.quarantineDirectory = filepath.Join(txn.stagingDirectory, "quarantine")
				if err := os.MkdirAll(filepath.Join(txn.quarantineDirectory, "pack"), perm.PrivateDir); err != nil {
					return nil, fmt.Errorf("create quarantine directory: %w", err)
				}

				txn.snapshotRepository, err = txn.snapshotRepository.Quarantine(txn.quarantineDirectory)
				if err != nil {
					return nil, fmt.Errorf("quarantine: %w", err)
				}
			} else {
				txn.quarantineDirectory = filepath.Join(mgr.storagePath, txn.snapshot.relativePath(txn.relativePath), "objects")
			}
		}

		return txn, nil
	}
}

// originalPackedRefsFilePath returns the path of the original `packed-refs` file that records the state of the
// `packed-refs` file as it was when the transaction started.
func (txn *Transaction) originalPackedRefsFilePath() string {
	return filepath.Join(txn.stagingDirectory, "packed-refs.original")
}

// RewriteRepository returns a copy of the repository that has been set up to correctly access
// the repository in the transaction's snapshot.
func (txn *Transaction) RewriteRepository(repo *gitalypb.Repository) *gitalypb.Repository {
	rewritten := proto.Clone(repo).(*gitalypb.Repository)
	rewritten.RelativePath = txn.snapshot.relativePath(repo.RelativePath)

	if repo.RelativePath == txn.relativePath {
		rewritten.GitObjectDirectory = txn.snapshotRepository.GetGitObjectDirectory()
		rewritten.GitAlternateObjectDirectories = txn.snapshotRepository.GetGitAlternateObjectDirectories()
	}

	return rewritten
}

// OriginalRepository returns the repository as it was before rewriting it to point to the snapshot.
func (txn *Transaction) OriginalRepository(repo *gitalypb.Repository) *gitalypb.Repository {
	original := proto.Clone(repo).(*gitalypb.Repository)
	original.RelativePath = strings.TrimPrefix(repo.RelativePath, txn.snapshot.prefix+string(os.PathSeparator))
	original.GitObjectDirectory = ""
	original.GitAlternateObjectDirectories = nil
	return original
}

func (txn *Transaction) updateState(newState transactionState) error {
	switch txn.state {
	case transactionStateOpen:
		txn.state = newState
		return nil
	case transactionStateRollback:
		return ErrTransactionAlreadyRollbacked
	case transactionStateCommit:
		return ErrTransactionAlreadyCommitted
	default:
		return fmt.Errorf("unknown transaction state: %q", txn.state)
	}
}

// Commit performs the changes. If no error is returned, the transaction was successful and the changes
// have been performed. If an error was returned, the transaction may or may not be persisted.
func (txn *Transaction) Commit(ctx context.Context) (returnedErr error) {
	if err := txn.updateState(transactionStateCommit); err != nil {
		return err
	}

	defer func() {
		if err := txn.finishUnadmitted(); err != nil && returnedErr == nil {
			returnedErr = err
		}
	}()

	if txn.readOnly {
		// These errors are only for reporting programming mistakes where updates have been
		// accidentally staged in a read-only transaction. The changes would not be anyway
		// performed as read-only transactions are not committed through the manager.
		switch {
		case txn.referenceUpdates != nil:
			return errReadOnlyReferenceUpdates
		case txn.defaultBranchUpdated:
			return errReadOnlyDefaultBranchUpdate
		case txn.customHooksUpdated:
			return errReadOnlyCustomHooksUpdate
		case txn.deleteRepository:
			return errReadOnlyRepositoryDeletion
		case txn.includedObjects != nil:
			return errReadOnlyObjectsIncluded
		case txn.runHousekeeping != nil:
			return errReadOnlyHousekeeping
		default:
			return nil
		}
	}

	if txn.runHousekeeping != nil && (txn.referenceUpdates != nil ||
		txn.defaultBranchUpdated ||
		txn.customHooksUpdated ||
		txn.deleteRepository ||
		txn.includedObjects != nil) {
		return errHousekeepingConflictOtherUpdates
	}

	return txn.commit(ctx, txn)
}

// Rollback releases resources associated with the transaction without performing any changes.
func (txn *Transaction) Rollback() error {
	if err := txn.updateState(transactionStateRollback); err != nil {
		return err
	}

	return txn.finishUnadmitted()
}

// finishUnadmitted cleans up after the transaction if it wasn't yet admitted. If the transaction was admitted,
// the Transaction is being processed by TransactionManager. The clean up responsibility moves there as well
// to avoid races.
func (txn *Transaction) finishUnadmitted() error {
	if txn.admitted {
		return nil
	}

	return txn.finish()
}

// SnapshotLSN returns the LSN of the Transaction's read snapshot.
func (txn *Transaction) SnapshotLSN() LSN {
	return txn.snapshotLSN
}

// SkipVerificationFailures configures the transaction to skip reference updates that fail verification.
// If a reference update fails verification with this set, the update is dropped from the transaction but
// other successful reference updates will be made. By default, the entire transaction is aborted if a
// reference fails verification.
//
// The default behavior models `git push --atomic`. Toggling this option models the behavior without
// the `--atomic` flag.
func (txn *Transaction) SkipVerificationFailures() {
	txn.skipVerificationFailures = true
}

// RecordInitialReferenceValues records the initial values of the references for the next UpdateReferences call. If oid is
// not a zero OID, it's used as the initial value. If oid is a zero value, the reference's actual value is resolved.
//
// The reference's first recorded value is used as its old OID in the update. RecordInitialReferenceValues can be used to
// record the value without staging an update in the transaction. This is useful for example generally recording the initial
// value in the 'prepare' phase of the reference transaction hook before any changes are made without staging any updates
// before the 'committed' phase is reached. The recorded initial values are only used for the next UpdateReferences call.
func (txn *Transaction) RecordInitialReferenceValues(ctx context.Context, initialValues map[git.ReferenceName]git.ObjectID) error {
	txn.initialReferenceValues = make(map[git.ReferenceName]git.ObjectID, len(initialValues))

	objectHash, err := txn.snapshotRepository.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("object hash: %w", err)
	}

	for reference, oid := range initialValues {
		if objectHash.IsZeroOID(oid) {
			// If this is a zero OID, resolve the value to see if this is a force update or the
			// reference doesn't exist.
			if current, err := txn.snapshotRepository.ResolveRevision(ctx, reference.Revision()); err != nil {
				if !errors.Is(err, git.ErrReferenceNotFound) {
					return fmt.Errorf("resolve revision: %w", err)
				}

				// The reference doesn't exist, leave the value as zero oid.
			} else {
				oid = current
			}
		}

		txn.initialReferenceValues[reference] = oid
	}

	return nil
}

// UpdateReferences updates the given references as part of the transaction. Each call is treated as
// a different reference transaction. This is allows for performing directory-file conflict inducing
// changes in a transaction. For example:
//
// - First call  - delete 'refs/heads/parent'
// - Second call - create 'refs/heads/parent/child'
//
// If a reference is updated multiple times during a transaction, its first recorded old OID used as
// the old OID when verifying the reference update, and the last recorded new OID is used as the new
// OID in the final commit. This means updates like 'oid-1 -> oid-2 -> oid-3' will ultimately be
// committed as 'oid-1 -> oid-3'. The old OIDs of the intermediate states are not verified when
// committing the write to the actual repository and are discarded from the final committed log
// entry.
func (txn *Transaction) UpdateReferences(updates ReferenceUpdates) {
	u := ReferenceUpdates{}

	for reference, update := range updates {
		oldOID := update.OldOID
		if initialValue, ok := txn.initialReferenceValues[reference]; ok {
			oldOID = initialValue
		}

		for _, updates := range txn.referenceUpdates {
			if update, ok := updates[reference]; ok {
				oldOID = update.NewOID
			}
		}

		u[reference] = ReferenceUpdate{
			OldOID: oldOID,
			NewOID: update.NewOID,
		}
	}

	txn.initialReferenceValues = nil
	txn.referenceUpdates = append(txn.referenceUpdates, u)
}

// flattenReferenceTransactions flattens the recorded reference transactions by dropping
// all intermediate states. The returned ReferenceUpdates contains the reference changes
// with the OldOID set to the reference's value at the beginning of the transaction, and the
// NewOID set to the reference's final value after all of the changes.
func (txn *Transaction) flattenReferenceTransactions() ReferenceUpdates {
	flattenedUpdates := ReferenceUpdates{}
	for _, updates := range txn.referenceUpdates {
		for reference, update := range updates {
			u := ReferenceUpdate{
				OldOID: update.OldOID,
				NewOID: update.NewOID,
			}

			if previousUpdate, ok := flattenedUpdates[reference]; ok {
				u.OldOID = previousUpdate.OldOID
			}

			flattenedUpdates[reference] = u
		}
	}

	return flattenedUpdates
}

// DeleteRepository deletes the repository when the transaction is committed.
func (txn *Transaction) DeleteRepository() {
	txn.deleteRepository = true
}

// MarkDefaultBranchUpdated sets a hint for the transaction manager that the default branch has been updated
// as a part of the transaction. This leads to the manager identifying changes and staging them for commit.
func (txn *Transaction) MarkDefaultBranchUpdated() {
	txn.defaultBranchUpdated = true
}

// MarkCustomHooksUpdated sets a hint to the transaction manager that custom hooks have been updated as part
// of the transaction. This leads to the manager identifying changes and staging them for commit.
func (txn *Transaction) MarkCustomHooksUpdated() {
	txn.customHooksUpdated = true
}

// PackRefs sets pack-refs housekeeping task as a part of the transaction. The transaction can only runs other
// housekeeping tasks in the same transaction. No other updates are allowed.
func (txn *Transaction) PackRefs() {
	if txn.runHousekeeping == nil {
		txn.runHousekeeping = &runHousekeeping{}
	}
	txn.runHousekeeping.packRefs = &runPackRefs{
		PrunedRefs: map[git.ReferenceName]struct{}{},
	}
}

// Repack sets repacking housekeeping task as a part of the transaction.
func (txn *Transaction) Repack(config housekeepingcfg.RepackObjectsConfig) {
	if txn.runHousekeeping == nil {
		txn.runHousekeeping = &runHousekeeping{}
	}
	txn.runHousekeeping.repack = &runRepack{
		config: config,
	}
}

// WriteCommitGraphs enables the commit graph to be rewritten as part of the transaction.
func (txn *Transaction) WriteCommitGraphs(config housekeepingcfg.WriteCommitGraphConfig) {
	if txn.runHousekeeping == nil {
		txn.runHousekeeping = &runHousekeeping{}
	}
	txn.runHousekeeping.writeCommitGraphs = &writeCommitGraphs{
		config: config,
	}
}

// IncludeObject includes the given object and its dependencies in the transaction's logged pack file even
// if the object is unreachable from the references.
func (txn *Transaction) IncludeObject(oid git.ObjectID) {
	if txn.includedObjects == nil {
		txn.includedObjects = map[git.ObjectID]struct{}{}
	}

	txn.includedObjects[oid] = struct{}{}
}

// MarkAlternateUpdated hints to the transaction manager that  'objects/info/alternates' file has been updated or
// removed. The file's modification will then be included in the transaction.
func (txn *Transaction) MarkAlternateUpdated() {
	txn.alternateUpdated = true
}

// walFilesPath returns the path to the directory where this transaction is staging the files that will
// be logged alongside the transaction's log entry.
func (txn *Transaction) walFilesPath() string {
	return filepath.Join(txn.stagingDirectory, "wal-files")
}

// snapshotLock contains state used to synchronize snapshotters and the log application with each other.
// Snapshotters wait on the applied channel until all of the committed writes in the read snapshot have
// been applied on the repository. The log application waits until all activeSnapshotters have managed to
// snapshot their state prior to applying the next log entry to the repository.
type snapshotLock struct {
	// applied is closed when the transaction the snapshotters are waiting for has been applied to the
	// repository and is ready for reading.
	applied chan struct{}
	// activeSnapshotters tracks snapshotters who are either taking a snapshot or waiting for the
	// log entry to be applied. Log application waits for active snapshotters to finish before applying
	// the next entry.
	activeSnapshotters sync.WaitGroup
}

// committedEntry is a wrapper for a log entry. It is used to keep track of entries in which their snapshots are still
// accessed by other transactions.
type committedEntry struct {
	// lsn is the associated LSN of the entry
	lsn LSN
	// snapshotReaders accounts for the number of transaction readers of the snapshot.
	snapshotReaders int
}

// TransactionManager is responsible for transaction management of a single repository. Each repository has
// a single TransactionManager; it is the repository's single-writer. It accepts writes one at a time from
// the admissionQueue. Each admitted write is processed in three steps:
//
//  1. The references being updated are verified by ensuring the expected old tips match what the references
//     actually point to prior to update. The entire transaction is by default aborted if a single reference
//     fails the verification step. The reference verification behavior can be controlled on a per-transaction
//     level by setting:
//     - The reference verification failures can be ignored instead of aborting the entire transaction.
//     If done, the references that failed verification are dropped from the transaction but the updates
//     that passed verification are still performed.
//  2. The transaction is appended to the write-ahead log. Once the write has been logged, it is effectively
//     committed and will be applied to the repository even after restarting.
//  3. The transaction is applied from the write-ahead log to the repository by actually performing the reference
//     changes.
//
// The goroutine that issued the transaction is waiting for the result while these steps are being performed. As
// there is no transaction control for readers yet, the issuer is only notified of a successful write after the
// write has been applied to the repository.
//
// TransactionManager recovers transactions after interruptions by applying the write-ahead logged transactions to
// the repository on start up.
//
// TransactionManager maintains the write-ahead log in a key-value store. It maintains the following key spaces:
// - `partition/<partition_id>/applied_lsn`
//   - This key stores the LSN of the log entry that has been applied to the repository. This allows for
//     determining how far a partition is in processing the log and which log entries need to be applied
//     after starting up. Partition starts from LSN 0 if there are no log entries recorded to have
//     been applied.
//
// - `partition/<partition_id:string>/log/entry/<log_index:uint64>`
//   - These keys hold the actual write-ahead log entries. A partition's first log entry starts at LSN 1
//     and the LSN keeps monotonically increasing from there on without gaps. The write-ahead log
//     entries are processed in ascending order.
//
// The values in the database are marshaled protocol buffer messages. Numbers in the keys are encoded as big
// endian to preserve the sort order of the numbers also in lexicographical order.
type TransactionManager struct {
	// ctx is the context used for all operations.
	ctx context.Context
	// close cancels ctx and stops the transaction processing.
	close context.CancelFunc
	// logger is the logger to use to write log messages.
	logger log.Logger

	// closing is closed when close is called. It unblock transactions that are waiting to be admitted.
	closing <-chan struct{}
	// closed is closed when Run returns. It unblocks transactions that are waiting for a result after
	// being admitted. This is differentiated from ctx.Done in order to enable testing that Run correctly
	// releases awaiters when the transactions processing is stopped.
	closed chan struct{}
	// stateDirectory is an absolute path to a directory where the TransactionManager stores the state related to its
	// write-ahead log.
	stateDirectory string
	// stagingDirectory is a path to a directory where this TransactionManager should stage the files of the transactions
	// before it logs them. The TransactionManager cleans up the files during runtime but stale files may be
	// left around after crashes. The files are temporary and any leftover files are expected to be cleaned up when
	// Gitaly starts.
	stagingDirectory string
	// commandFactory is used to spawn git commands without a repository.
	commandFactory git.CommandFactory
	// repositoryFactory is used to build localrepo.Repo instances.
	repositoryFactory localrepo.StorageScopedFactory
	// storagePath is an absolute path to the root of the storage this TransactionManager
	// is operating in.
	storagePath string
	// partitionID is the ID of the partition this manager is operating on. This is used to determine the database keys.
	partitionID partitionID
	// db is the handle to the key-value store used for storing the write-ahead log related state.
	db Database
	// admissionQueue is where the incoming writes are waiting to be admitted to the transaction
	// manager.
	admissionQueue chan *Transaction
	// completedQueue is a queue notifying when a transaction finishes.
	completedQueue chan struct{}

	// initialized is closed when the manager has been initialized. It's used to block new transactions
	// from beginning prior to the manager having initialized its runtime state on start up.
	initialized chan struct{}
	// initializationSuccessful is set if the TransactionManager initialized successfully. If it didn't,
	// transactions will fail to begin.
	initializationSuccessful bool
	// mutex guards access to snapshotLocks and appendedLSN. These fields are accessed by both
	// Run and Begin which are ran in different goroutines.
	mutex sync.Mutex

	// snapshotLocks contains state used for synchronizing snapshotters with the log application. The
	// lock is released after the corresponding log entry is applied.
	snapshotLocks map[LSN]*snapshotLock

	// appendedLSN holds the LSN of the last log entry appended to the partition's write-ahead log.
	appendedLSN LSN
	// appliedLSN holds the LSN of the last log entry applied to the partition.
	appliedLSN LSN
	// oldestLSN holds the LSN of the head of log entries which is still kept in the database. The manager keeps
	// them because they are still referred by a transaction.
	oldestLSN LSN

	// awaitingTransactions contains transactions waiting for their log entry to be applied to
	// the partition. It's keyed by the LSN the transaction is waiting to be applied and the
	// value is the resultChannel that is waiting the result.
	awaitingTransactions map[LSN]resultChannel
	// committedEntries keeps some latest appended log entries around. Some types of transactions, such as
	// housekeeping, operate on snapshot repository. There is a gap between transaction doing its work and the time
	// when it is committed. They need to verify if concurrent operations can cause conflict. These log entries are
	// still kept around even after they are applied. They are removed when there are no active readers accessing
	// the corresponding snapshots.
	committedEntries *list.List

	// testHooks are used in the tests to trigger logic at certain points in the execution.
	// They are used to synchronize more complex test scenarios. Not used in production.
	testHooks testHooks
}

type testHooks struct {
	beforeInitialization      func()
	beforeAppendLogEntry      func()
	beforeApplyLogEntry       func()
	beforeStoreAppliedLSN     func()
	beforeDeleteLogEntryFiles func()
	beforeRunExiting          func()
}

// NewTransactionManager returns a new TransactionManager for the given repository.
func NewTransactionManager(
	ptnID partitionID,
	logger log.Logger,
	db Database,
	storagePath,
	stateDir,
	stagingDir string,
	cmdFactory git.CommandFactory,
	repositoryFactory localrepo.StorageScopedFactory,
) *TransactionManager {
	ctx, cancel := context.WithCancel(context.Background())
	return &TransactionManager{
		ctx:                  ctx,
		close:                cancel,
		logger:               logger,
		closing:              ctx.Done(),
		closed:               make(chan struct{}),
		commandFactory:       cmdFactory,
		repositoryFactory:    repositoryFactory,
		storagePath:          storagePath,
		partitionID:          ptnID,
		db:                   db,
		admissionQueue:       make(chan *Transaction),
		completedQueue:       make(chan struct{}),
		initialized:          make(chan struct{}),
		snapshotLocks:        make(map[LSN]*snapshotLock),
		stateDirectory:       stateDir,
		stagingDirectory:     stagingDir,
		awaitingTransactions: make(map[LSN]resultChannel),
		committedEntries:     list.New(),

		testHooks: testHooks{
			beforeInitialization:      func() {},
			beforeAppendLogEntry:      func() {},
			beforeApplyLogEntry:       func() {},
			beforeStoreAppliedLSN:     func() {},
			beforeDeleteLogEntryFiles: func() {},
			beforeRunExiting:          func() {},
		},
	}
}

// resultChannel represents a future that will yield the result of a transaction once its
// outcome has been decided.
type resultChannel chan error

// commit queues the transaction for processing and returns once the result has been determined.
func (mgr *TransactionManager) commit(ctx context.Context, transaction *Transaction) error {
	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.Commit", nil)
	defer span.Finish()

	transaction.result = make(resultChannel, 1)

	if !transaction.repositoryExists {
		// Determine if the repository was created in this transaction and stage its state
		// for committing if so.
		if err := mgr.stageRepositoryCreation(ctx, transaction); err != nil {
			if errors.Is(err, storage.ErrRepositoryNotFound) {
				// The repository wasn't created as part of this transaction.
				return nil
			}

			return fmt.Errorf("stage repository creation: %w", err)
		}
	}

	// Create a directory to store all staging files.
	if err := os.Mkdir(transaction.walFilesPath(), perm.PrivateDir); err != nil {
		return fmt.Errorf("create wal files directory: %w", err)
	}

	transaction.walEntry = wal.NewEntry(transaction.walFilesPath())

	if err := mgr.setupStagingRepository(ctx, transaction); err != nil {
		return fmt.Errorf("setup staging repository: %w", err)
	}

	if err := mgr.packObjects(ctx, transaction); err != nil {
		return fmt.Errorf("pack objects: %w", err)
	}

	if err := mgr.prepareHousekeeping(ctx, transaction); err != nil {
		return fmt.Errorf("preparing housekeeping: %w", err)
	}

	if transaction.repositoryCreation != nil {
		// Below we'll log the repository exactly as it was created by the transaction. While
		// we can expect the transaction leaves the repository in a good state, we'll override
		// the object directory of the repository so it only contains:
		// - The packfiles we generated above that contain only the reachable objects.
		// - The alternate link if set.
		//
		// This ensures the object state of the repository is exactly as the TransactionManager
		// expects it to be. There should be no objects missing dependencies or else the repository
		// could be corrupted if these objects are used. All objects in the generated pack
		// are verified to have all their dependencies present. Replacing the object directory thus
		// ensures we only log the packfile with the verified objects, and that no loose objects make
		// it into the repository, or anything else that breaks our assumption about the object
		// database contents.
		if err := mgr.replaceObjectDirectory(transaction); err != nil {
			return fmt.Errorf("replace object directory: %w", err)
		}

		if err := transaction.walEntry.RecordRepositoryCreation(
			mgr.getAbsolutePath(transaction.snapshot.prefix),
			transaction.relativePath,
		); err != nil {
			return fmt.Errorf("record repository creation: %w", err)
		}
	} else {
		if transaction.alternateUpdated {
			stagedAlternatesRelativePath := stats.AlternatesFilePath(transaction.relativePath)
			stagedAlternatesAbsolutePath := mgr.getAbsolutePath(transaction.snapshot.relativePath(stagedAlternatesRelativePath))
			if _, err := os.Stat(stagedAlternatesAbsolutePath); err != nil {
				if !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("check alternates existence: %w", err)
				}

				// Alternates file did not exist, nothing to stage. This was an unlink operation.
			} else {
				if err := transaction.walEntry.RecordFileCreation(
					stagedAlternatesAbsolutePath,
					stagedAlternatesRelativePath,
				); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("record alternates update: %w", err)
				}
			}
		}

		if transaction.customHooksUpdated {
			// Log the custom hook creation. The deletion of the previous hooks is logged after admission to
			// ensure we log the latest state for deletion in case someone else modified the hooks.
			//
			// If the transaction removed the custom hooks, we won't have anything to log. We'll ignore the
			// ErrNotExist and stage the deletion later.
			if err := transaction.walEntry.RecordDirectoryCreation(
				mgr.getAbsolutePath(transaction.snapshot.prefix),
				filepath.Join(transaction.relativePath, repoutil.CustomHooksDir),
			); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("record custom hook directory: %w", err)
			}
		}

		// If there were objects packed that should be committed, record the packfile's creation.
		if transaction.packPrefix != "" {
			packDir := filepath.Join(transaction.relativePath, "objects", "pack")
			for _, fileExtension := range []string{".pack", ".idx", ".rev"} {
				if err := transaction.walEntry.RecordFileCreation(
					filepath.Join(transaction.stagingDirectory, "objects"+fileExtension),
					filepath.Join(packDir, transaction.packPrefix+fileExtension),
				); err != nil {
					return fmt.Errorf("record file creation: %w", err)
				}
			}
		}

		if transaction.defaultBranchUpdated {
			if err := transaction.walEntry.RecordFileUpdate(
				mgr.getAbsolutePath(transaction.snapshot.prefix),
				filepath.Join(transaction.relativePath, "HEAD"),
			); err != nil {
				return fmt.Errorf("record HEAD update: %w", err)
			}
		}
	}

	if err := safe.NewSyncer().SyncRecursive(transaction.walFilesPath()); err != nil {
		return fmt.Errorf("synchronizing WAL directory: %w", err)
	}

	select {
	case mgr.admissionQueue <- transaction:
		transaction.admitted = true

		select {
		case err := <-transaction.result:
			return unwrapExpectedError(err)
		case <-ctx.Done():
			return ctx.Err()
		case <-mgr.closed:
			return ErrTransactionProcessingStopped
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-mgr.closing:
		return ErrTransactionProcessingStopped
	}
}

// replaceObjectDirectory replaces the snapshot repository's object directory
// to only contain the packs generated by TransactionManager and the possible
// alternate link if present.
func (mgr *TransactionManager) replaceObjectDirectory(tx *Transaction) (returnedErr error) {
	repoPath, err := tx.snapshotRepository.Path()
	if err != nil {
		return fmt.Errorf("snapshot repository path: %w", err)
	}

	objectsDir := filepath.Join(repoPath, "objects")
	objectsInfo, err := os.Stat(objectsDir)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}

	// Create the standard object directory structure.
	newObjectsDir := filepath.Join(tx.stagingDirectory, "objects_tmp")
	if err := os.Mkdir(newObjectsDir, objectsInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("create new objects dir: %w", err)
	}

	for _, dir := range []string{"info", "pack"} {
		info, err := os.Stat(filepath.Join(objectsDir, dir))
		if err != nil {
			return fmt.Errorf("stat: %w", err)
		}

		if err := os.Mkdir(filepath.Join(newObjectsDir, dir), info.Mode().Perm()); err != nil {
			return fmt.Errorf("mkdir: %w", err)
		}
	}

	// If there were objects packed that should be committed, link the pack to the new
	// replacement object directory.
	if tx.packPrefix != "" {
		for _, fileExtension := range []string{".pack", ".idx", ".rev"} {
			if err := os.Link(
				filepath.Join(tx.stagingDirectory, "objects"+fileExtension),
				filepath.Join(newObjectsDir, "pack", tx.packPrefix+fileExtension),
			); err != nil {
				return fmt.Errorf("link to synthesized object directory: %w", err)
			}
		}
	}

	// If there was an alternate link set by the transaction, place it also in the replacement
	// object directory.
	if tx.alternateUpdated {
		if err := os.Link(
			filepath.Join(objectsDir, "info", "alternates"),
			filepath.Join(newObjectsDir, "info", "alternates"),
		); err != nil {
			return fmt.Errorf("link alternate: %w", err)
		}
	}

	// Remove the old object directory from the snapshot repository and replace it with the
	// object directory that only contains verified data.
	if err := os.RemoveAll(objectsDir); err != nil {
		return fmt.Errorf("remove objects directory: %w", err)
	}

	if err := os.Rename(newObjectsDir, objectsDir); err != nil {
		return fmt.Errorf("rename replacement directory in place: %w", err)
	}

	return nil
}

// stageRepositoryCreation determines the repository's state following a creation. It reads the repository's
// complete state and stages it into the transaction for committing.
func (mgr *TransactionManager) stageRepositoryCreation(ctx context.Context, transaction *Transaction) error {
	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.stageRepositoryCreation", nil)
	defer span.Finish()

	objectHash, err := transaction.snapshotRepository.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("object hash: %w", err)
	}

	transaction.repositoryCreation = &repositoryCreation{
		objectHash: objectHash,
	}

	references, err := transaction.snapshotRepository.GetReferences(ctx)
	if err != nil {
		return fmt.Errorf("get references: %w", err)
	}

	referenceUpdates := make(ReferenceUpdates, len(references))
	for _, ref := range references {
		referenceUpdates[ref.Name] = ReferenceUpdate{
			OldOID: objectHash.ZeroOID,
			NewOID: git.ObjectID(ref.Target),
		}
	}

	transaction.referenceUpdates = []ReferenceUpdates{referenceUpdates}

	var customHooks bytes.Buffer
	if err := repoutil.GetCustomHooks(ctx, mgr.logger,
		filepath.Join(mgr.storagePath, transaction.snapshotRepository.GetRelativePath()), &customHooks); err != nil {
		return fmt.Errorf("get custom hooks: %w", err)
	}

	if customHooks.Len() > 0 {
		transaction.MarkCustomHooksUpdated()
	}

	if _, err := readAlternatesFile(mgr.getAbsolutePath(transaction.snapshotRepository.GetRelativePath())); err != nil {
		if !errors.Is(err, errNoAlternate) {
			return fmt.Errorf("read alternates file: %w", err)
		}

		// Repository had no alternate.
	} else {
		transaction.MarkAlternateUpdated()
	}

	return nil
}

// setupStagingRepository sets a repository that is used to stage the transaction. The staging repository
// has the quarantine applied so the objects are available for packing and verifying the references.
func (mgr *TransactionManager) setupStagingRepository(ctx context.Context, transaction *Transaction) error {
	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.setupStagingRepository", nil)
	defer span.Finish()

	// If this is not a creation, the repository should already exist. Use it to stage the transaction.
	stagingRepository := mgr.repositoryFactory.Build(transaction.relativePath)
	if transaction.repositoryCreation != nil {
		// The reference updates in the transaction are normally verified against the actual repository.
		// If the repository doesn't exist yet, the reference updates are verified against an empty
		// repository to ensure they'll apply when the log entry creates the repository. After the
		// transaction is logged, the staging repository is removed, and the actual repository will be
		// created when the log entry is applied.

		relativePath, err := filepath.Rel(mgr.storagePath, filepath.Join(transaction.stagingDirectory, "staging.git"))
		if err != nil {
			return fmt.Errorf("rel: %w", err)
		}

		if err := mgr.createRepository(ctx, filepath.Join(mgr.storagePath, relativePath), transaction.repositoryCreation.objectHash.ProtoFormat); err != nil {
			return fmt.Errorf("create staging repository: %w", err)
		}

		stagingRepository = mgr.repositoryFactory.Build(relativePath)
	}

	var err error
	transaction.stagingRepository, err = stagingRepository.Quarantine(transaction.quarantineDirectory)
	if err != nil {
		return fmt.Errorf("quarantine: %w", err)
	}

	return nil
}

// packPrefixRegexp matches the output of `git index-pack` where it
// prints the packs prefix in the format `pack <digest>`.
var packPrefixRegexp = regexp.MustCompile(`^pack\t([0-9a-f]+)\n$`)

// shouldPackObjects checks whether the quarantine directory has any non-default content in it.
// If so, this signifies objects were written into it and we should pack them.
func shouldPackObjects(quarantineDirectory string) (bool, error) {
	errHasNewContent := errors.New("new content found")

	// The quarantine directory itself and the pack directory within it are created when the transaction
	// begins. These don't signify new content so we ignore them.
	preExistingDirs := map[string]struct{}{
		quarantineDirectory:                        {},
		filepath.Join(quarantineDirectory, "pack"): {},
	}
	if err := filepath.Walk(quarantineDirectory, func(path string, info fs.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if _, ok := preExistingDirs[path]; ok {
			// The pre-existing directories don't signal any new content to pack.
			return nil
		}

		// Use an error sentinel to cancel the walk as soon as new content has been found.
		return errHasNewContent
	}); err != nil {
		if errors.Is(err, errHasNewContent) {
			return true, nil
		}

		return false, fmt.Errorf("check for objects: %w", err)
	}

	return false, nil
}

// packObjects packs the objects included in the transaction into a single pack file that is ready
// for logging. The pack file includes all unreachable objects that are about to be made reachable and
// unreachable objects that have been explicitly included in the transaction.
func (mgr *TransactionManager) packObjects(ctx context.Context, transaction *Transaction) error {
	if shouldPack, err := shouldPackObjects(transaction.quarantineDirectory); err != nil {
		return fmt.Errorf("should pack objects: %w", err)
	} else if !shouldPack {
		return nil
	}

	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.packObjects", nil)
	defer span.Finish()

	// We should actually be figuring out the new objects to pack against the transaction's snapshot
	// repository as the objects and references in the actual repository may change while we're doing
	// this. We don't yet have a way to figure out which objects the new quarantined objects depend on
	// in the main object database of the snapshot.
	//
	// This is pending https://gitlab.com/groups/gitlab-org/-/epics/11242.
	objectHash, err := transaction.stagingRepository.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("object hash: %w", err)
	}

	heads := make([]string, 0)
	for _, update := range transaction.flattenReferenceTransactions() {
		if update.NewOID == objectHash.ZeroOID {
			// Reference deletions can't introduce new objects so ignore them.
			continue
		}

		heads = append(heads, update.NewOID.String())
	}

	for objectID := range transaction.includedObjects {
		heads = append(heads, objectID.String())
	}

	if len(heads) == 0 {
		// No need to pack objects if there are no changes that can introduce new objects.
		return nil
	}

	objectsReader, objectsWriter := io.Pipe()

	group, ctx := errgroup.WithContext(ctx)
	group.Go(func() (returnedErr error) {
		defer func() { objectsWriter.CloseWithError(returnedErr) }()

		if err := transaction.stagingRepository.WalkUnreachableObjects(ctx,
			strings.NewReader(strings.Join(heads, "\n")),
			objectsWriter,
		); err != nil {
			return fmt.Errorf("walk objects: %w", err)
		}

		return nil
	})

	packReader, packWriter := io.Pipe()
	group.Go(func() (returnedErr error) {
		defer func() {
			objectsReader.CloseWithError(returnedErr)
			packWriter.CloseWithError(returnedErr)
		}()

		if err := transaction.stagingRepository.PackObjects(ctx, objectsReader, packWriter); err != nil {
			return fmt.Errorf("pack objects: %w", err)
		}

		return nil
	})

	group.Go(func() (returnedErr error) {
		defer packReader.CloseWithError(returnedErr)

		// index-pack places the pack, index, and reverse index into the repository's object directory.
		// The staging repository is configured with a quarantine so we execute it there.
		var stdout, stderr bytes.Buffer
		if err := transaction.stagingRepository.ExecAndWait(ctx, git.Command{
			Name:  "index-pack",
			Flags: []git.Option{git.Flag{Name: "--stdin"}, git.Flag{Name: "--rev-index"}},
			Args:  []string{filepath.Join(transaction.stagingDirectory, "objects.pack")},
		}, git.WithStdin(packReader), git.WithStdout(&stdout), git.WithStderr(&stderr)); err != nil {
			return structerr.New("index pack: %w", err).WithMetadata("stderr", stderr.String())
		}

		matches := packPrefixRegexp.FindStringSubmatch(stdout.String())
		if len(matches) != 2 {
			return structerr.New("unexpected index-pack output").WithMetadata("stdout", stdout.String())
		}

		transaction.packPrefix = fmt.Sprintf("pack-%s", matches[1])

		return nil
	})

	return group.Wait()
}

// prepareHousekeeping composes and prepares necessary steps on the staging repository before the changes are staged and
// applied. All commands run in the scope of the staging repository. Thus, we can avoid any impact on other concurrent
// transactions.
func (mgr *TransactionManager) prepareHousekeeping(ctx context.Context, transaction *Transaction) error {
	if transaction.runHousekeeping == nil {
		return nil
	}

	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.prepareHousekeeping", nil)
	defer span.Finish()

	if err := mgr.preparePackRefs(ctx, transaction); err != nil {
		return err
	}
	if err := mgr.prepareRepacking(ctx, transaction); err != nil {
		return err
	}
	if err := mgr.prepareCommitGraphs(ctx, transaction); err != nil {
		return err
	}
	return nil
}

// preparePackRefs runs git-pack-refs command against the snapshot repository. It collects the resulting packed-refs
// file and the list of pruned references. Unfortunately, git-pack-refs doesn't output which refs are pruned. So, we
// performed two ref walkings before and after running the command. The difference between the two walks is the list of
// pruned refs. This workaround works but is not performant on large repositories with huge amount of loose references.
// Smaller repositories or ones that run housekeeping frequent won't have this issue.
// The work of adding pruned refs dump to `git-pack-refs` is tracked here:
// https://gitlab.com/gitlab-org/git/-/issues/222
func (mgr *TransactionManager) preparePackRefs(ctx context.Context, transaction *Transaction) error {
	if transaction.runHousekeeping.packRefs == nil {
		return nil
	}

	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.preparePackRefs", nil)
	defer span.Finish()

	runPackRefs := transaction.runHousekeeping.packRefs
	repoPath := mgr.getAbsolutePath(transaction.snapshotRepository.GetRelativePath())

	if err := mgr.removePackedRefsLocks(mgr.ctx, repoPath); err != nil {
		return fmt.Errorf("remove stale packed-refs locks: %w", err)
	}
	// First walk to collect the list of loose refs.
	looseReferences := make(map[git.ReferenceName]struct{})
	if err := filepath.WalkDir(filepath.Join(repoPath, "refs"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			// Get fully qualified refs.
			ref, err := filepath.Rel(repoPath, path)
			if err != nil {
				return fmt.Errorf("extracting ref name: %w", err)
			}
			looseReferences[git.ReferenceName(ref)] = struct{}{}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("initial walking refs directory: %w", err)
	}

	// Execute git-pack-refs command. The command runs in the scope of the snapshot repository. Thus, we can
	// let it prune the ref references without causing any impact to other concurrent transactions.
	var stderr bytes.Buffer
	if err := transaction.snapshotRepository.ExecAndWait(ctx, git.Command{
		Name:  "pack-refs",
		Flags: []git.Option{git.Flag{Name: "--all"}},
	}, git.WithStderr(&stderr)); err != nil {
		return structerr.New("exec pack-refs: %w", err).WithMetadata("stderr", stderr.String())
	}

	// Copy the resulting packed-refs file to the WAL directory.
	if err := os.Link(
		filepath.Join(filepath.Join(repoPath, "packed-refs")),
		filepath.Join(transaction.walFilesPath(), "packed-refs"),
	); err != nil {
		return fmt.Errorf("copying packed-refs file to WAL directory: %w", err)
	}

	// Second walk and compare with the initial list of loose references. Any disappeared refs are pruned.
	for ref := range looseReferences {
		_, err := os.Stat(filepath.Join(repoPath, ref.String()))
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				runPackRefs.PrunedRefs[ref] = struct{}{}
			} else {
				return fmt.Errorf("second walk refs directory: %w", err)
			}
		}
	}

	return nil
}

// prepareRepacking runs git-repack(1) command against the snapshot repository using desired repacking strategy. Each
// strategy has a different cost and effect corresponding to scheduling frequency.
// - IncrementalWithUnreachable: pack all loose objects into one packfile. This strategy is a no-op because all new
// objects regardless of their reachablity status are packed by default by the manager.
// - Geometric: merge all packs together with geometric repacking. This is expensive or cheap depending on which packs
// get merged. No need for a connectivity check.
// - FullWithUnreachable: merge all packs into one but keep unreachable objects. This is more expensive but we don't
// take connectivity into account. This strategy is essential for object pool. As we cannot prune objects in a pool,
// packing them into one single packfile boosts its performance.
// - FullWithCruft: Merge all packs into one and prune unreachable objects. It is the most effective, but yet costly
// strategy. We cannot run this type of task frequently on a large repository. This strategy is handled as a full
// repacking without cruft because we don't need object expiry.
// Before the command runs, we capture a snapshot of existing packfiles. After the command finishes, we re-capture the
// list and extract the list of to-be-updated packfiles. This practice is to prevent repacking task from deleting
// packfiles of other concurrent updates at the applying phase.
func (mgr *TransactionManager) prepareRepacking(ctx context.Context, transaction *Transaction) error {
	if transaction.runHousekeeping.repack == nil {
		return nil
	}

	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.prepareRepacking", nil)
	defer span.Finish()

	var err error
	repack := transaction.runHousekeeping.repack

	// Build a working repository pointing to snapshot repository. Housekeeping task can access the repository
	// without the needs for quarantine.
	workingRepository := mgr.repositoryFactory.Build(transaction.snapshot.relativePath(transaction.relativePath))
	repoPath := mgr.getAbsolutePath(workingRepository.GetRelativePath())

	if repack.isFullRepack, err = housekeeping.ValidateRepacking(repack.config); err != nil {
		return fmt.Errorf("validating repacking: %w", err)
	}

	if repack.config.Strategy == housekeepingcfg.RepackObjectsStrategyIncrementalWithUnreachable {
		// Once the transaction manager has been applied and at least one complete repack has occurred, there
		// should be no loose unreachable objects remaining in the repository. When the transaction manager
		// processes a change, it consolidates all unreachable objects and objects about to become reachable
		// into a new packfile, which is then placed in the repository. As a result, unreachable objects may
		// still exist but are confined to packfiles. These will eventually be cleaned up during a full repack.
		// In the interim, geometric repacking is utilized to optimize the structure of packfiles for faster
		// access. Therefore, this operation is effectively a no-op. However, we maintain it for the sake of
		// backward compatibility with the existing housekeeping scheduler.
		return errRepackNotSupportedStrategy
	}

	// Capture the list of packfiles and their baggages before repacking.
	beforeFiles, err := mgr.collectPackfiles(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("collecting existing packfiles: %w", err)
	}

	switch repack.config.Strategy {
	case housekeepingcfg.RepackObjectsStrategyGeometric:
		// Geometric repacking rearranges the list of packfiles according to a geometric progression. This process
		// does not consider object reachability. Since all unreachable objects remain within small packfiles,
		// they become included in the newly created packfiles. Geometric repacking does not prune any objects.
		if err := housekeeping.PerformGeometricRepacking(ctx, workingRepository, repack.config); err != nil {
			return fmt.Errorf("perform geometric repacking: %w", err)
		}
	case housekeepingcfg.RepackObjectsStrategyFullWithUnreachable:
		// This strategy merges all packfiles into a single packfile, simultaneously removing any loose objects
		// if present. Unreachable objects are then appended to the end of this unified packfile. Although the
		// `git-repack(1)` command does not offer an option to specifically pack loose unreachable objects, this
		// is not an issue because the transaction manager already ensures that unreachable objects are
		// contained within packfiles. Therefore, this strategy effectively consolidates all packfiles into a
		// single one. Adopting this strategy is crucial for alternates, as it ensures that we can manage
		// objects within an object pool without the capability to prune them.
		if err := housekeeping.PerformFullRepackingWithUnreachable(ctx, workingRepository, repack.config); err != nil {
			return err
		}
	case housekeepingcfg.RepackObjectsStrategyFullWithCruft:
		// Both of above strategies don't prune unreachable objects. They re-organize the objects between
		// packfiles. In the traditional housekeeping, the manager gets rid of unreachable objects via full
		// repacking with cruft. It pushes all unreachable objects to a cruft packfile and keeps track of each
		// object mtimes. All unreachable objects exceeding a grace period are cleaned up. The grace period is
		// to ensure the housekeeping doesn't delete a to-be-reachable object accidentally, for example when GC
		// runs while a concurrent push is being processed.
		// The transaction manager handles concurrent requests very differently from the original git way. Each
		// request runs on a snapshot repository and the results are collected in the form of packfiles. Those
		// packfiles contain resulting reachable and unreachable objects. As a result, we don't need to take
		// object expiry nor curft pack into account. This operation triggers a normal full repack without
		// cruft packing.
		// Afterward, packed unreachable objects are removed. During migration to transaction system, there
		// might be some loose unreachable objects. They will eventually be packed via either of the above tasks.
		if err := housekeeping.PerformRepack(ctx, workingRepository, repack.config,
			// Do a full repack. By using `-a` instead of `-A` we will immediately discard unreachable
			// objects instead of exploding them into loose objects.
			git.Flag{Name: "-a"},
			// Don't include objects part of alternate.
			git.Flag{Name: "-l"},
			// Delete loose objects made redundant by this repack and redundant packfiles.
			git.Flag{Name: "-d"},
		); err != nil {
			return err
		}
	}

	// Re-capture the list of packfiles and their baggages after repacking.
	afterFiles, err := mgr.collectPackfiles(ctx, repoPath)
	if err != nil {
		return fmt.Errorf("collecting new packfiles: %w", err)
	}

	for file := range beforeFiles {
		// We delete the files only if it's missing from the before set.
		if _, exist := afterFiles[file]; !exist {
			repack.deletedFiles = append(repack.deletedFiles, file)
		}
	}

	for file := range afterFiles {
		// Similarly, we don't need to link existing packfiles.
		if _, exist := beforeFiles[file]; !exist {
			repack.newFiles = append(repack.newFiles, file)
			if err := os.Link(
				filepath.Join(filepath.Join(repoPath, "objects", "pack"), file),
				filepath.Join(transaction.walFilesPath(), file),
			); err != nil {
				return fmt.Errorf("copying packfiles to WAL directory: %w", err)
			}
		}
	}

	return nil
}

// prepareCommitGraphs updates the commit-graph in the snapshot repository. It then hard-links the
// graphs to the staging repository so it can be applied by the transaction manager.
func (mgr *TransactionManager) prepareCommitGraphs(ctx context.Context, transaction *Transaction) error {
	if transaction.runHousekeeping.writeCommitGraphs == nil {
		return nil
	}

	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.prepareCommitGraphs", nil)
	defer span.Finish()

	workingRepository := mgr.repositoryFactory.Build(transaction.snapshot.relativePath(transaction.relativePath))
	repoPath := mgr.getAbsolutePath(workingRepository.GetRelativePath())

	if err := housekeeping.WriteCommitGraph(ctx, workingRepository, transaction.runHousekeeping.writeCommitGraphs.config); err != nil {
		return fmt.Errorf("re-writing commit graph: %w", err)
	}

	commitGraphsDir := filepath.Join(repoPath, "objects", "info", "commit-graphs")
	if graphEntries, err := os.ReadDir(commitGraphsDir); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reading commit-graphs directory: %w", err)
		}
	} else if len(graphEntries) > 0 {
		walGraphsDir := filepath.Join(transaction.walFilesPath(), "commit-graphs")
		if err := os.Mkdir(walGraphsDir, perm.PrivateDir); err != nil {
			return fmt.Errorf("creating commit-graphs dir in WAL dir: %w", err)
		}
		for _, entry := range graphEntries {
			if err := os.Link(
				filepath.Join(commitGraphsDir, entry.Name()),
				filepath.Join(walGraphsDir, entry.Name()),
			); err != nil {
				return fmt.Errorf("linking commit-graph entry to WAL dir: %w", err)
			}
		}
	}

	return nil
}

// packfileExtensions contains the packfile extension and its dependencies. They will be collected after running
// repacking command.
var packfileExtensions = map[string]struct{}{
	"multi-pack-index": {},
	".pack":            {},
	".idx":             {},
	".rev":             {},
	".mtimes":          {},
	".bitmap":          {},
}

// collectPackfiles collects the list of packfiles and their luggage files.
func (mgr *TransactionManager) collectPackfiles(ctx context.Context, repoPath string) (map[string]struct{}, error) {
	files, err := os.ReadDir(filepath.Join(repoPath, "objects", "pack"))
	if err != nil {
		return nil, fmt.Errorf("reading objects/pack dir: %w", err)
	}

	// Filter packfiles and relevant files.
	collectedFiles := make(map[string]struct{})
	for _, file := range files {
		// objects/pack directory should not include any sub-directory. We can simply ignore them.
		if file.IsDir() {
			continue
		}
		for extension := range packfileExtensions {
			if strings.HasSuffix(file.Name(), extension) {
				collectedFiles[file.Name()] = struct{}{}
			}
		}
	}

	return collectedFiles, nil
}

// unwrapExpectedError unwraps expected errors that may occur and returns them directly to the caller.
func unwrapExpectedError(err error) error {
	// The manager controls its own execution context and it is canceled only when Stop is called.
	// Any context.Canceled errors returned are thus from shutting down so we report that here.
	if errors.Is(err, context.Canceled) {
		return ErrTransactionProcessingStopped
	}

	return err
}

// Run starts the transaction processing. On start up Run loads the indexes of the last appended and applied
// log entries from the database. It will then apply any transactions that have been logged but not applied
// to the repository. Once the recovery is completed, Run starts processing new transactions by verifying the
// references, logging the transaction and finally applying it to the repository. The transactions are acknowledged
// once they've been applied to the repository.
//
// Run keeps running until Stop is called or it encounters a fatal error. All transactions will error with
// ErrTransactionProcessingStopped when Run returns.
func (mgr *TransactionManager) Run() (returnedErr error) {
	defer func() {
		// On-going operations may fail with a context canceled error if the manager is stopped. This is
		// not a real error though given the manager will recover from this on restart. Swallow the error.
		if errors.Is(returnedErr, context.Canceled) {
			returnedErr = nil
		}
	}()

	// Defer the Stop in order to release all on-going Commit calls in case of error.
	defer close(mgr.closed)
	defer mgr.Close()
	defer mgr.testHooks.beforeRunExiting()

	if err := mgr.initialize(mgr.ctx); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}

	for {
		if mgr.appliedLSN < mgr.appendedLSN {
			lsn := mgr.appliedLSN + 1

			if err := mgr.applyLogEntry(mgr.ctx, lsn); err != nil {
				return fmt.Errorf("apply log entry: %w", err)
			}

			continue
		}

		// When a log entry is applied, if there is any log in front of it which are still referred, we cannot delete
		// it. This condition is to prevent a "hole" in the list. A transaction referring to a log entry at the
		// low-water mark might scan all afterward log entries. Thus, the manager needs to keep in the database.
		//
		// ┌─ Oldest LSN
		// ┌─ Can be removed ─┐            ┌─ Cannot be removed
		// □ □ □ □ □ □ □ □ □ □ ■ ■ ⧅ ⧅ ⧅ ⧅ ⧅ ⧅ ■ ■ ⧅ ⧅ ⧅ ⧅ ■
		//                     └─ Low-water mark, still referred by another transaction
		if mgr.oldestLSN < mgr.lowWaterMark() {
			if err := mgr.deleteLogEntry(mgr.oldestLSN); err != nil {
				return fmt.Errorf("deleting log entry: %w", err)
			}
			mgr.oldestLSN++
			continue
		}

		if err := mgr.processTransaction(); err != nil {
			return fmt.Errorf("process transaction: %w", err)
		}
	}
}

// processTransaction waits for a transaction and processes it by verifying and
// logging it.
func (mgr *TransactionManager) processTransaction() (returnedErr error) {
	var transaction *Transaction
	select {
	case transaction = <-mgr.admissionQueue:
		// The Transaction does not finish itself anymore once it has been admitted for
		// processing. This avoids the Transaction concurrently removing the staged state
		// while the manager is still operating on it. We thus need to defer its finishing.
		defer func() {
			if err := transaction.finish(); err != nil && returnedErr == nil {
				returnedErr = fmt.Errorf("finish transaction: %w", err)
			}
		}()
	case <-mgr.completedQueue:
		return nil
	case <-mgr.ctx.Done():
	}

	// Return if the manager was stopped. The select is indeterministic so this guarantees
	// the manager stops the processing even if there are transactions in the queue.
	if err := mgr.ctx.Err(); err != nil {
		return err
	}

	span, ctx := tracing.StartSpanIfHasParent(mgr.ctx, "transaction.processTransaction", nil)
	defer span.Finish()

	if err := func() (commitErr error) {
		repositoryExists, err := mgr.doesRepositoryExist(transaction.relativePath)
		if err != nil {
			return fmt.Errorf("does repository exist: %w", err)
		}

		logEntry := &gitalypb.LogEntry{
			RelativePath: transaction.relativePath,
		}

		if transaction.repositoryCreation != nil && repositoryExists {
			return ErrRepositoryAlreadyExists
		} else if transaction.repositoryCreation == nil && !repositoryExists {
			return ErrRepositoryNotFound
		}

		if err := mgr.verifyAlternateUpdate(ctx, transaction); err != nil {
			return fmt.Errorf("verify alternate update: %w", err)
		}

		if transaction.repositoryCreation == nil && transaction.runHousekeeping == nil && !transaction.deleteRepository {
			logEntry.ReferenceTransactions, err = mgr.verifyReferences(ctx, transaction)
			if err != nil {
				return fmt.Errorf("verify references: %w", err)
			}
		}

		if transaction.customHooksUpdated {
			// Log a deletion of the existing custom hooks so they are removed before the
			// new ones are put in place.
			if err := transaction.walEntry.RecordDirectoryRemoval(
				mgr.storagePath,
				filepath.Join(transaction.relativePath, repoutil.CustomHooksDir),
			); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("record custom hook removal: %w", err)
			}
		}

		if transaction.deleteRepository {
			logEntry.RepositoryDeletion = &gitalypb.LogEntry_RepositoryDeletion{}

			if err := transaction.walEntry.RecordDirectoryRemoval(
				mgr.storagePath,
				transaction.relativePath,
			); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("record repository removal: %w", err)
			}
		}

		if transaction.runHousekeeping != nil {
			housekeepingEntry, err := mgr.verifyHousekeeping(ctx, transaction)
			if err != nil {
				return fmt.Errorf("verifying pack refs: %w", err)
			}
			logEntry.Housekeeping = housekeepingEntry
		}

		logEntry.Operations = transaction.walEntry.Operations()

		return mgr.appendLogEntry(logEntry, transaction.walFilesPath())
	}(); err != nil {
		transaction.result <- err
		return nil
	}

	mgr.awaitingTransactions[mgr.appendedLSN] = transaction.result

	return nil
}

// Close stops the transaction processing causing Run to return.
func (mgr *TransactionManager) Close() { mgr.close() }

// isClosing returns whether closing of the manager was initiated.
func (mgr *TransactionManager) isClosing() bool {
	select {
	case <-mgr.closing:
		return true
	default:
		return false
	}
}

// initialize initializes the TransactionManager's state from the database. It loads the appended and the applied
// LSNs and initializes the notification channels that synchronize transaction beginning with log entry applying.
func (mgr *TransactionManager) initialize(ctx context.Context) error {
	defer close(mgr.initialized)

	var appliedLSN gitalypb.LSN
	if err := mgr.readKey(keyAppliedLSN(mgr.partitionID), &appliedLSN); err != nil && !errors.Is(err, badger.ErrKeyNotFound) {
		return fmt.Errorf("read applied LSN: %w", err)
	}

	mgr.appliedLSN = LSN(appliedLSN.Value)

	if err := mgr.createStateDirectory(); err != nil {
		return fmt.Errorf("create state directory: %w", err)
	}

	// The LSN of the last appended log entry is determined from the LSN of the latest entry in the log and
	// the latest applied log entry. The manager also keeps track of committed entries and reserves them until there
	// is no transaction refers them. It's possible there are some left-over entries in the database because a
	// transaction can hold the entry stubbornly. So, the manager could not clean them up in the last session.
	//
	// ┌─ oldestLSN                    ┌─ appendedLSN
	// ⧅ ⧅ ⧅ ⧅ ⧅ ⧅ ⧅ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■
	//                └─ appliedLSN
	//
	//
	// oldestLSN is initialized to appliedLSN + 1. If there are no log entries in the log, then everything has been
	// pruned already or there has not been any log entries yet. Setting this +1 avoids trying to clean up log entries
	// that do not exsti. If there are some, we'll set oldestLSN to the head of the log below.
	mgr.oldestLSN = mgr.appliedLSN + 1
	// appendedLSN is initialized to appliedLSN. If there are no log entries, then there has been no transaction yet, or
	// all log entries have been applied and have been already pruned. If there are some in the log, we'll update this
	// below to match.
	mgr.appendedLSN = mgr.appliedLSN

	if logEntries, err := os.ReadDir(walFilesPath(mgr.stateDirectory)); err != nil {
		return fmt.Errorf("read wal directory: %w", err)
	} else if len(logEntries) > 0 {
		if mgr.oldestLSN, err = parseLSN(logEntries[0].Name()); err != nil {
			return fmt.Errorf("parse oldest LSN: %w", err)
		}
		if mgr.appendedLSN, err = parseLSN(logEntries[len(logEntries)-1].Name()); err != nil {
			return fmt.Errorf("parse appended LSN: %w", err)
		}
	}

	// Create a snapshot lock for the applied LSN as it is used for synchronizing
	// the snapshotters with the log application.
	mgr.snapshotLocks[mgr.appliedLSN] = &snapshotLock{applied: make(chan struct{})}
	close(mgr.snapshotLocks[mgr.appliedLSN].applied)

	// Each unapplied log entry should have a snapshot lock as they are created in normal
	// operation when committing a log entry. Recover these entries.
	for i := mgr.appliedLSN + 1; i <= mgr.appendedLSN; i++ {
		mgr.snapshotLocks[i] = &snapshotLock{applied: make(chan struct{})}
	}

	if err := mgr.removeStaleWALFiles(mgr.ctx, mgr.oldestLSN, mgr.appendedLSN); err != nil {
		return fmt.Errorf("remove stale packs: %w", err)
	}

	mgr.testHooks.beforeInitialization()
	mgr.initializationSuccessful = true

	return nil
}

// doesRepositoryExist returns whether the repository exists or not.
func (mgr *TransactionManager) doesRepositoryExist(relativePath string) (bool, error) {
	stat, err := os.Stat(mgr.getAbsolutePath(relativePath))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}

		return false, fmt.Errorf("stat repository directory: %w", err)
	}

	if !stat.IsDir() {
		return false, errNotDirectory
	}

	return true, nil
}

func (mgr *TransactionManager) createStateDirectory() error {
	for _, path := range []string{
		mgr.stateDirectory,
		filepath.Join(mgr.stateDirectory, "wal"),
	} {
		if err := os.Mkdir(path, perm.PrivateDir); err != nil {
			if !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("mkdir: %w", err)
			}
		}
	}

	syncer := safe.NewSyncer()
	if err := syncer.SyncRecursive(mgr.stateDirectory); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	if err := syncer.SyncParent(mgr.stateDirectory); err != nil {
		return fmt.Errorf("sync parent: %w", err)
	}

	return nil
}

// getAbsolutePath returns the relative path's absolute path in the storage.
func (mgr *TransactionManager) getAbsolutePath(relativePath ...string) string {
	return filepath.Join(append([]string{mgr.storagePath}, relativePath...)...)
}

// removePackedRefsLocks removes any packed-refs.lock and packed-refs.new files present in the manager's
// repository. No grace period for the locks is given as any lockfiles present must be stale and can be
// safely removed immediately.
func (mgr *TransactionManager) removePackedRefsLocks(ctx context.Context, repositoryPath string) error {
	for _, lock := range []string{".new", ".lock"} {
		lockPath := filepath.Join(repositoryPath, "packed-refs"+lock)

		// We deliberately do not fsync this deletion. Should a crash occur before this is persisted
		// to disk, the restarted transaction manager will simply remove them again.
		if err := os.Remove(lockPath); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}

			return fmt.Errorf("remove %v: %w", lockPath, err)
		}
	}

	return nil
}

// removeStaleWALFiles removes files from the log directory that have no associated log entry.
// Such files can be left around if transaction's files were moved in place successfully
// but the manager was interrupted before successfully persisting the log entry itself.
// If the manager deletes a log entry successfully from the database but is interrupted before it cleans
// up the associated files, such a directory can also be left at the head of the log.
func (mgr *TransactionManager) removeStaleWALFiles(ctx context.Context, oldestLSN, appendedLSN LSN) error {
	needsFsync := false
	for _, possibleStaleFilesPath := range []string{
		// Log entries are pruned one by one. If a write is interrupted, the only possible stale files would be
		// for the log entry preceding the oldest log entry.
		walFilesPathForLSN(mgr.stateDirectory, oldestLSN-1),
		// Log entries are appended one by one to the log. If a write is interrupted, the only possible stale
		// files would be for the next LSN. Remove the files if they exist.
		walFilesPathForLSN(mgr.stateDirectory, appendedLSN+1),
	} {

		if _, err := os.Stat(possibleStaleFilesPath); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("stat: %w", err)
			}

			// No stale files were present.
			continue
		}

		if err := os.RemoveAll(possibleStaleFilesPath); err != nil {
			return fmt.Errorf("remove all: %w", err)
		}

		needsFsync = true
	}

	if needsFsync {
		// Sync the parent directory to flush the file deletion.
		if err := safe.NewSyncer().Sync(walFilesPath(mgr.stateDirectory)); err != nil {
			return fmt.Errorf("sync: %w", err)
		}
	}

	return nil
}

// walFilesPath returns the WAL directory's path.
func walFilesPath(stateDir string) string {
	return filepath.Join(stateDir, "wal")
}

// walFilesPathForLSN returns an absolute path to a given log entry's WAL files.
func walFilesPathForLSN(stateDir string, lsn LSN) string {
	return filepath.Join(walFilesPath(stateDir), lsn.String())
}

// manifestPath returns the manifest file's path in the log entry.
func manifestPath(logEntryPath string) string {
	return filepath.Join(logEntryPath, "MANIFEST")
}

// packFilePath returns a log entry's pack file's absolute path in the wal files directory.
func packFilePath(walFiles string) string {
	return filepath.Join(walFiles, "transaction.pack")
}

// verifyAlternateUpdate verifies the staged alternate update.
func (mgr *TransactionManager) verifyAlternateUpdate(ctx context.Context, transaction *Transaction) error {
	if !transaction.alternateUpdated {
		return nil
	}

	span, _ := tracing.StartSpanIfHasParent(mgr.ctx, "transaction.verifyAlternateUpdate", nil)
	defer span.Finish()

	repositoryPath := mgr.getAbsolutePath(transaction.relativePath)
	existingAlternate, err := readAlternatesFile(repositoryPath)
	if err != nil && !errors.Is(err, errNoAlternate) {
		return fmt.Errorf("read existing alternates file: %w", err)
	}

	snapshotRepoPath, err := transaction.snapshotRepository.Path()
	if err != nil {
		return fmt.Errorf("snapshot repo path: %w", err)
	}

	stagedAlternate, err := readAlternatesFile(snapshotRepoPath)
	if err != nil && !errors.Is(err, errNoAlternate) {
		return fmt.Errorf("read staged alternates file: %w", err)
	}

	if stagedAlternate == "" {
		if existingAlternate == "" {
			// Transaction attempted to remove an alternate from the repository
			// even if it didn't have one.
			return errNoAlternate
		}

		if err := transaction.walEntry.RecordAlternateUnlink(mgr.storagePath, transaction.relativePath, existingAlternate); err != nil {
			return fmt.Errorf("record alternate unlink: %w", err)
		}

		return nil
	}

	if existingAlternate != "" {
		return errAlternateAlreadyLinked
	}

	alternateObjectsDir, err := storage.ValidateRelativePath(
		mgr.storagePath,
		filepath.Join(transaction.relativePath, "objects", stagedAlternate),
	)
	if err != nil {
		return fmt.Errorf("validate relative path: %w", err)
	}

	alternateRelativePath := filepath.Dir(alternateObjectsDir)
	if alternateRelativePath == transaction.relativePath {
		return errAlternatePointsToSelf
	}

	// Check that the alternate repository exists. This works as a basic conflict check
	// to prevent linking a repository that was deleted concurrently.
	alternateRepositoryPath := mgr.getAbsolutePath(alternateRelativePath)
	if err := storage.ValidateGitDirectory(alternateRepositoryPath); err != nil {
		return fmt.Errorf("validate git directory: %w", err)
	}

	if _, err := readAlternatesFile(alternateRepositoryPath); !errors.Is(err, errNoAlternate) {
		if err == nil {
			// We don't support chaining alternates like repo-1 > repo-2 > repo-3.
			return errAlternateHasAlternate
		}

		return fmt.Errorf("read alternates file: %w", err)
	}

	return nil
}

// verifyReferences verifies that the references in the transaction apply on top of the already accepted
// reference changes. The old tips in the transaction are verified against the current actual tips.
// It returns the write-ahead log entry for the reference transactions successfully verified.
func (mgr *TransactionManager) verifyReferences(ctx context.Context, transaction *Transaction) ([]*gitalypb.LogEntry_ReferenceTransaction, error) {
	if len(transaction.referenceUpdates) == 0 {
		return nil, nil
	}

	span, _ := tracing.StartSpanIfHasParent(mgr.ctx, "transaction.verifyReferences", nil)
	defer span.Finish()

	conflictingReferenceUpdates := map[git.ReferenceName]struct{}{}
	for referenceName, update := range transaction.flattenReferenceTransactions() {
		// Transactions should only stage references with valid names as otherwise Git would already
		// fail when they try to stage them against their snapshot. `update-ref` happily accepts references
		// outside of `refs` directory so such references could theoretically arrive here. We thus sanity
		// check that all references modified are within the refs directory.
		if !strings.HasPrefix(referenceName.String(), "refs/") {
			return nil, InvalidReferenceFormatError{ReferenceName: referenceName}
		}

		actualOldTip, err := transaction.stagingRepository.ResolveRevision(ctx, referenceName.Revision())
		if errors.Is(err, git.ErrReferenceNotFound) {
			objectHash, err := transaction.stagingRepository.ObjectHash(ctx)
			if err != nil {
				return nil, fmt.Errorf("object hash: %w", err)
			}

			actualOldTip = objectHash.ZeroOID
		} else if err != nil {
			return nil, fmt.Errorf("resolve revision: %w", err)
		}

		if update.OldOID != actualOldTip {
			if transaction.skipVerificationFailures {
				conflictingReferenceUpdates[referenceName] = struct{}{}
				continue
			}

			return nil, ReferenceVerificationError{
				ReferenceName: referenceName,
				ExpectedOID:   update.OldOID,
				ActualOID:     actualOldTip,
			}
		}
	}

	var referenceTransactions []*gitalypb.LogEntry_ReferenceTransaction
	for _, updates := range transaction.referenceUpdates {
		changes := make([]*gitalypb.LogEntry_ReferenceTransaction_Change, 0, len(updates))
		for reference, update := range updates {
			if _, ok := conflictingReferenceUpdates[reference]; ok {
				continue
			}

			changes = append(changes, &gitalypb.LogEntry_ReferenceTransaction_Change{
				ReferenceName: []byte(reference),
				NewOid:        []byte(update.NewOID),
			})
		}

		// Sort the reference updates so the reference changes are always logged in a deterministic order.
		sort.Slice(changes, func(i, j int) bool {
			return bytes.Compare(
				changes[i].ReferenceName,
				changes[j].ReferenceName,
			) == -1
		})

		referenceTransactions = append(referenceTransactions, &gitalypb.LogEntry_ReferenceTransaction{
			Changes: changes,
		})
	}

	if err := mgr.verifyReferencesWithGit(ctx, referenceTransactions, transaction); err != nil {
		return nil, fmt.Errorf("verify references with git: %w", err)
	}

	return referenceTransactions, nil
}

// vefifyReferencesWithGit verifies the reference updates with git by committing them against a snapshot of the target
// repository. This ensures the updates will go through when they are being applied from the log. This also catches any
// invalid reference names and file/directory conflicts with Git's loose reference storage which can occur with references
// like 'refs/heads/parent' and 'refs/heads/parent/child'.
func (mgr *TransactionManager) verifyReferencesWithGit(ctx context.Context, referenceTransactions []*gitalypb.LogEntry_ReferenceTransaction, tx *Transaction) error {
	relativePath := tx.stagingRepository.GetRelativePath()
	snapshot, err := newSnapshot(ctx,
		mgr.storagePath,
		filepath.Join(tx.stagingDirectory, "staging"),
		[]string{relativePath},
	)
	if err != nil {
		return fmt.Errorf("new snapshot: %w", err)
	}

	snapshotPackedRefsPath := mgr.getAbsolutePath(snapshot.relativePath(tx.relativePath), "packed-refs")

	// Record the original packed-refs file. We use it for determining whether the transaction has changed it and
	// for conflict checking to see if other concurrent transactions have changed the `packed-refs` file. We link
	// the file instead of just recording the inode number to prevent it from being recycled.
	if err := os.Link(
		snapshotPackedRefsPath,
		tx.originalPackedRefsFilePath(),
	); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("record original packed-refs: %w", err)
	}

	repo, err := mgr.repositoryFactory.Build(snapshot.relativePath(relativePath)).Quarantine(tx.quarantineDirectory)
	if err != nil {
		return fmt.Errorf("quarantine: %w", err)
	}

	objectHash, err := repo.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("object hash: %w", err)
	}

	for _, referenceTransaction := range referenceTransactions {
		if err := tx.walEntry.RecordReferenceUpdates(ctx,
			mgr.storagePath,
			snapshot.prefix,
			tx.relativePath,
			referenceTransaction,
			objectHash,
			func(changes []*gitalypb.LogEntry_ReferenceTransaction_Change) error {
				return mgr.applyReferenceTransaction(ctx, changes, repo)
			},
		); err != nil {
			return fmt.Errorf("record reference updates: %w", err)
		}
	}

	// Get the inode of the `packed-refs` file as it was before we applied the reference transactions.
	originalPackedRefsInode, err := getInode(tx.originalPackedRefsFilePath())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("get original packed-refs inode: %w", err)
	}

	// Get the inode of the `packed-refs` file as it is in the snapshot after the reference transactions.
	stagedPackedRefsInode, err := getInode(snapshotPackedRefsPath)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("get staged packed-refs inode: %w", err)
	}

	targetPackedRefsPath := filepath.Join(tx.relativePath, "packed-refs")
	// If the packed-refs file was updated, stage the new updated file.
	if stagedPackedRefsInode != originalPackedRefsInode {
		if originalPackedRefsInode > 0 {
			// If the repository has a packed-refs file already, remove it.
			tx.walEntry.RecordDirectoryEntryRemoval(targetPackedRefsPath)
		}

		if stagedPackedRefsInode > 0 {
			// If there is a new packed refs file, stage it.
			if err := tx.walEntry.RecordFileCreation(
				snapshotPackedRefsPath,
				targetPackedRefsPath,
			); err != nil {
				return fmt.Errorf("record new packed-refs: %w", err)
			}
		}
	}

	return nil
}

// verifyHousekeeping verifies if all included housekeeping tasks can be performed. Although it's feasible for multiple
// housekeeping tasks running at the same time, it's not guaranteed they are conflict-free. So, we need to ensure there
// is no other concurrent housekeeping task. Each sub-task also needs specific verification.
func (mgr *TransactionManager) verifyHousekeeping(ctx context.Context, transaction *Transaction) (*gitalypb.LogEntry_Housekeeping, error) {
	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.verifyHousekeeping", nil)
	defer span.Finish()

	mgr.mutex.Lock()
	defer mgr.mutex.Unlock()

	// Check for any concurrent housekeeping between this transaction's snapshot LSN and the latest appended LSN.
	if err := mgr.walkCommittedEntries(transaction, func(entry *gitalypb.LogEntry) error {
		if entry.GetHousekeeping() != nil {
			return errHousekeepingConflictConcurrent
		}
		if entry.GetRepositoryDeletion() != nil {
			return errConflictRepositoryDeletion
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walking committed entries: %w", err)
	}

	packRefsEntry, err := mgr.verifyPackRefs(ctx, transaction)
	if err != nil {
		return nil, fmt.Errorf("verifying pack refs: %w", err)
	}

	repackEntry, err := mgr.verifyRepacking(ctx, transaction)
	if err != nil {
		return nil, fmt.Errorf("verifying repacking: %w", err)
	}

	commitGraphsEntry, err := mgr.verifyCommitGraphs(mgr.ctx, transaction)
	if err != nil {
		return nil, fmt.Errorf("verifying commit graph update: %w", err)
	}

	return &gitalypb.LogEntry_Housekeeping{
		PackRefs:          packRefsEntry,
		Repack:            repackEntry,
		WriteCommitGraphs: commitGraphsEntry,
	}, nil
}

// verifyPackRefs verifies if the pack-refs housekeeping task can be logged. Ideally, we can just apply the packed-refs
// file and prune the loose references. Unfortunately, there could be a ref modification between the time the pack-refs
// command runs and the time this transaction is logged. Thus, we need to verify if the transaction conflicts with the
// current state of the repository.
//
// There are three cases when a reference is modified:
// - Reference creation: this is the easiest case. The new reference exists as a loose reference on disk and shadows the
// one in the packed-ref.
// - Reference update: similarly, the loose reference shadows the one in packed-refs with the new OID. However, we need
// to remove it from the list of pruned references. Otherwise, the repository continues to use the old OID.
// - Reference deletion. When a reference is deleted, both loose reference and the entry in the packed-refs file are
// removed. The reflogs are also removed. In addition, we don't use reflogs in Gitaly as core.logAllRefUpdates defaults
// to false in bare repositories. It could of course be that an admin manually enabled it by modifying the config
// on-disk directly. There is no way to extract reference deletion between two states.
//
// In theory, if there is any reference deletion, it can be removed from the packed-refs file. However, it requires
// parsing and regenerating the packed-refs file. So, let's settle down with a conflict error at this point.
func (mgr *TransactionManager) verifyPackRefs(ctx context.Context, transaction *Transaction) (*gitalypb.LogEntry_Housekeeping_PackRefs, error) {
	if transaction.runHousekeeping.packRefs == nil {
		return nil, nil
	}

	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.verifyPackRefs", nil)
	defer span.Finish()

	objectHash, err := transaction.stagingRepository.ObjectHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("object hash: %w", err)
	}
	packRefs := transaction.runHousekeeping.packRefs

	// Check for any concurrent ref deletion between this transaction's snapshot LSN to the end.
	if err := mgr.walkCommittedEntries(transaction, func(entry *gitalypb.LogEntry) error {
		for _, refTransaction := range entry.ReferenceTransactions {
			for _, change := range refTransaction.Changes {
				if objectHash.IsZeroOID(git.ObjectID(change.GetNewOid())) {
					// Oops, there is a reference deletion. Bail out.
					return errPackRefsConflictRefDeletion
				}
				// Ref update. Remove the updated ref from the list of pruned refs so that the
				// new OID in loose reference shadows the outdated OID in packed-refs.
				delete(packRefs.PrunedRefs, git.ReferenceName(change.GetReferenceName()))
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walking committed entries: %w", err)
	}

	var prunedRefs [][]byte
	for ref := range packRefs.PrunedRefs {
		prunedRefs = append(prunedRefs, []byte(ref))
	}
	return &gitalypb.LogEntry_Housekeeping_PackRefs{
		PrunedRefs: prunedRefs,
	}, nil
}

// The function verifyRepacking confirms that the repacking process can integrate a new collection of packfiles.
// Repacking should generally be risk-free as it systematically arranges packfiles and removes old, unused objects. The
// standard housekeeping procedure includes a grace period, in days, to protect objects that might be in use by
// simultaneous operations. However, the transaction manager deals with concurrent requests more effectively. By
// reviewing the list of transactions since the repacking began, we can identify potential conflicts involving
// concurrent processes.
// We need to watch out for two types of conflicts:
// 1. Overlapping transactions could reference objects that have been pruned. Extracting these pruned objects from the
// repacking process isn't straightforward. The repacking task must confirm that these referenced objects are still
// accessible in the repacked repository. This is done using the `git-cat-file --batch-check` command. Simultaneously,
// it's possible for these transactions to refer to new objects created by other concurrent transactions. So, we need to
// setup a working directory based on the destination repository and apply changed packfiles of repacking transaction
// for checking.
// 2. There might be transactions in progress that rely on dependencies that have been pruned. This type of conflict
// can't be fully checked until Git v2.43 is implemented in Gitaly, as detailed in the GitLab epic
// (https://gitlab.com/groups/gitlab-org/-/epics/11242).
// It's important to note that this verification is specific to the `RepackObjectsStrategyFullWithCruft` strategy. Other
// strategies focus solely on reorganizing packfiles and do not remove objects.
func (mgr *TransactionManager) verifyRepacking(ctx context.Context, transaction *Transaction) (_ *gitalypb.LogEntry_Housekeeping_Repack, finalErr error) {
	repack := transaction.runHousekeeping.repack
	if repack == nil {
		return nil, nil
	}

	span, ctx := tracing.StartSpanIfHasParent(ctx, "transaction.verifyRepacking", nil)
	defer span.Finish()

	// Other strategies re-organize packfiles without pruning unreachable objects. No need to run following
	// expensive verification.
	if repack.config.Strategy != housekeepingcfg.RepackObjectsStrategyFullWithCruft {
		return &gitalypb.LogEntry_Housekeeping_Repack{
			NewFiles:     repack.newFiles,
			DeletedFiles: repack.deletedFiles,
			IsFullRepack: repack.isFullRepack,
		}, nil
	}

	// Setup a working repository of the destination repository and all changes of current transactions. All
	// concurrent changes must land in that repository already.
	snapshot, err := newSnapshot(ctx,
		mgr.storagePath,
		filepath.Join(transaction.stagingDirectory, "staging"),
		[]string{transaction.relativePath},
	)
	if err != nil {
		return nil, fmt.Errorf("setting up new snapshot for verifying repacking: %w", err)
	}

	workingRepository := mgr.repositoryFactory.Build(snapshot.relativePath(transaction.relativePath))
	workingRepositoryPath, err := workingRepository.Path()
	if err != nil {
		return nil, fmt.Errorf("getting working repository path: %w", err)
	}

	// Apply the changes of current transaction.
	if err := mgr.replacePackfiles(workingRepositoryPath, transaction.walFilesPath(), repack.newFiles, repack.deletedFiles); err != nil {
		return nil, fmt.Errorf("applying packfiles for verifying repacking: %w", err)
	}

	// Apply new commit graph if any. Although commit graph update belongs to another task, a repacking task will
	// result in rewriting the commit graph. It would be nice to apply the commit graph when setting up working
	// repository for repacking task.
	if err := mgr.replaceCommitGraphs(workingRepositoryPath, transaction.walFilesPath()); err != nil {
		return nil, fmt.Errorf("applying commit graph for verifying repacking: %w", err)
	}

	objectHash, err := workingRepository.ObjectHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("detecting object hash: %w", err)
	}

	// Collect reference tips. All of them should exist in the resulting packfile or new concurrent
	// packfiles while repacking is running.
	referenceTips := map[string]struct{}{}
	if err := mgr.walkCommittedEntries(transaction, func(entry *gitalypb.LogEntry) error {
		for _, txn := range entry.GetReferenceTransactions() {
			for _, change := range txn.GetChanges() {
				oid := change.GetNewOid()
				if !objectHash.IsZeroOID(git.ObjectID(oid)) {
					referenceTips[string(oid)] = struct{}{}
				}
			}
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("walking committed entries: %w", err)
	}

	if len(referenceTips) == 0 {
		return &gitalypb.LogEntry_Housekeeping_Repack{
			NewFiles:     repack.newFiles,
			DeletedFiles: repack.deletedFiles,
			IsFullRepack: repack.isFullRepack,
		}, nil
	}

	// Although we have a wrapper for caching `git-cat-file` process, this command targets the snapshot repository
	// that the repacking task depends on. This task might run for minutes to hours. It's unlikely there is any
	// concurrent operation sharing this process. So, no need to cache it. An alternative solution is to read and
	// parse the index files of the working directory. That approach avoids spawning `git-cat-file(1)`. In contrast,
	// it requires storing the list of objects in memory.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var stderr bytes.Buffer
	stdinReader, stdinWriter := io.Pipe()
	stdoutReader, stdoutWriter := io.Pipe()

	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() (returnedErr error) {
		defer func() { stdinWriter.CloseWithError(returnedErr) }()
		for oid := range referenceTips {
			// Verify if the reference tip is reachable from the resulting single packfile.
			if _, err := stdinWriter.Write([]byte(fmt.Sprintf("%s\000", oid))); err != nil {
				return fmt.Errorf("writing cat-file command: %w", err)
			}
		}
		if _, err := stdinWriter.Write([]byte("flush\000")); err != nil {
			return fmt.Errorf("writing flush: %w", err)
		}
		return nil
	})

	group.Go(func() (returnedErr error) {
		defer func() {
			stdinReader.CloseWithError(returnedErr)
			stdoutReader.CloseWithError(returnedErr)
		}()

		stdout := bufio.NewReader(stdoutReader)
		for range referenceTips {
			if _, err := catfile.ParseObjectInfo(objectHash, stdout, true); err != nil {
				if errors.As(err, &catfile.NotFoundError{}) {
					return errRepackConflictPrunedObject
				}
				return fmt.Errorf("reading object info: %w", err)
			}
		}
		return nil
	})

	group.Go(func() (returnedErr error) {
		defer func() { stdoutWriter.CloseWithError(returnedErr) }()
		if err := workingRepository.ExecAndWait(groupCtx, git.Command{
			Name: "cat-file",
			Flags: []git.Option{
				git.Flag{Name: "--batch-check"},
				git.Flag{Name: "--buffer"},
				git.Flag{Name: "-Z"},
			},
		}, git.WithStdin(stdinReader), git.WithStdout(stdoutWriter), git.WithStderr(&stderr)); err != nil {
			return structerr.New("spawning cat-file command: %w", err).WithMetadata("stderr", stderr.String())
		}
		return nil
	})

	if err := group.Wait(); err != nil {
		return nil, err
	}

	return &gitalypb.LogEntry_Housekeeping_Repack{
		NewFiles:     repack.newFiles,
		DeletedFiles: repack.deletedFiles,
		IsFullRepack: repack.isFullRepack,
	}, nil
}

// verifyCommitGraphs verifies if the commit-graph update is valid. As we replace the whole commit-graph directory by
// the new directory, this step is a no-op now.
func (mgr *TransactionManager) verifyCommitGraphs(ctx context.Context, transaction *Transaction) (*gitalypb.LogEntry_Housekeeping_WriteCommitGraphs, error) {
	if transaction.runHousekeeping.writeCommitGraphs == nil {
		return nil, nil
	}
	return &gitalypb.LogEntry_Housekeeping_WriteCommitGraphs{}, nil
}

// applyReferenceTransaction applies a reference transaction with `git update-ref`.
func (mgr *TransactionManager) applyReferenceTransaction(ctx context.Context, changes []*gitalypb.LogEntry_ReferenceTransaction_Change, repository *localrepo.Repo) error {
	updater, err := updateref.New(ctx, repository, updateref.WithDisabledTransactions(), updateref.WithNoDeref())
	if err != nil {
		return fmt.Errorf("new: %w", err)
	}

	if err := updater.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	for _, change := range changes {
		if err := updater.Update(git.ReferenceName(change.ReferenceName), git.ObjectID(change.NewOid), ""); err != nil {
			return fmt.Errorf("update %q: %w", change.ReferenceName, err)
		}
	}

	if err := updater.Prepare(); err != nil {
		return fmt.Errorf("prepare: %w", err)
	}

	if err := updater.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// appendLogEntry appends the transaction to the write-ahead log. It first writes the transaction's manifest file
// into the log entry's directory. Afterwards it moves the log entry's directory from the staging area to its final
// place in the write-ahead log.
func (mgr *TransactionManager) appendLogEntry(logEntry *gitalypb.LogEntry, logEntryPath string) error {
	manifestBytes, err := proto.Marshal(logEntry)
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}

	// Finalize the log entry by writing the MANIFEST file into the log entry's directory.
	manifestPath := manifestPath(logEntryPath)
	if err := os.WriteFile(manifestPath, manifestBytes, perm.PrivateFile); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}

	syncer := safe.NewSyncer()
	// Sync the log entry completely before committing it.
	//
	// Ideally the log entry would be completely flushed to the disk before queuing the
	// transaction for commit to ensure we don't write a lot to the disk while in the critical
	// section. We currently stage some of the files only in the critical section though. This
	// is due to currently lacking conflict checks which prevents staging the log entry completely
	// before queuing it for commit.
	//
	// See https://gitlab.com/gitlab-org/gitaly/-/issues/5892 for more details. Once the issue is
	// addressed, we could stage the transaction entirely before queuing it for commit, and thus not
	// need to sync here.
	if err := syncer.SyncRecursive(logEntryPath); err != nil {
		return fmt.Errorf("synchronizing WAL directory: %w", err)
	}

	if err := syncer.SyncParent(manifestPath); err != nil {
		return fmt.Errorf("sync manifest directory entry: %w", err)
	}

	mgr.testHooks.beforeAppendLogEntry()

	nextLSN := mgr.appendedLSN + 1
	// Move the log entry from the staging directory into its place in the log.
	destinationPath := walFilesPathForLSN(mgr.stateDirectory, nextLSN)
	if err := os.Rename(logEntryPath, destinationPath); err != nil {
		return fmt.Errorf("move wal files: %w", err)
	}

	// Sync the WAL directory. The manifest has been synced above, and all of the other files
	// have been synced before queuing for commit. At this point we just have to sync the
	// directory entry of the new log entry in the WAL directory to finalize the commit.
	//
	// After this sync, the log entry has been persisted and will be recovered on failure.
	if err := safe.NewSyncer().SyncParent(destinationPath); err != nil {
		// If this fails, the log entry will be left in the write-ahead log but it is not
		// properly persisted. If the fsync fails, something is seriously wrong and there's no
		// point trying to delete the files. The right thing to do is to terminate Gitaly
		// immediately as going further could cause data loss and corruption. This error check
		// will later be replaced with a panic that terminates Gitaly.
		//
		// For more details, see: https://gitlab.com/gitlab-org/gitaly/-/issues/5774
		return fmt.Errorf("sync log entry: %w", err)
	}

	// After this latch block, the transaction is committed and all subsequent transactions
	// are guaranteed to read it.
	mgr.mutex.Lock()
	mgr.appendedLSN = nextLSN
	mgr.snapshotLocks[nextLSN] = &snapshotLock{applied: make(chan struct{})}
	mgr.committedEntries.PushBack(&committedEntry{
		lsn: nextLSN,
	})
	mgr.mutex.Unlock()

	return nil
}

// applyLogEntry reads a log entry at the given LSN and applies it to the repository.
func (mgr *TransactionManager) applyLogEntry(ctx context.Context, lsn LSN) error {
	logEntry, err := mgr.readLogEntry(lsn)
	if err != nil {
		return fmt.Errorf("read log entry: %w", err)
	}

	// Ensure all snapshotters have finished snapshotting the previous state before we apply
	// the new state to the repository. No new snapshotters can arrive at this point. All
	// new transactions would be waiting for the committed log entry we are about to apply.
	previousLSN := lsn - 1
	mgr.snapshotLocks[previousLSN].activeSnapshotters.Wait()
	mgr.mutex.Lock()
	delete(mgr.snapshotLocks, previousLSN)
	mgr.mutex.Unlock()

	mgr.testHooks.beforeApplyLogEntry()

	if err := applyOperations(safe.NewSyncer().Sync, mgr.storagePath, walFilesPathForLSN(mgr.stateDirectory, lsn), logEntry); err != nil {
		return fmt.Errorf("apply operations: %w", err)
	}

	// If the repository is being deleted, just delete it without any other changes given
	// they'd all be removed anyway. Reapplying the other changes after a crash would also
	// not work if the repository was successfully deleted before the crash.
	if logEntry.RepositoryDeletion == nil {
		if err := mgr.applyHousekeeping(ctx, lsn, logEntry); err != nil {
			return fmt.Errorf("apply housekeeping: %w", err)
		}
	}

	mgr.testHooks.beforeStoreAppliedLSN()
	if err := mgr.storeAppliedLSN(lsn); err != nil {
		return fmt.Errorf("set applied LSN: %w", err)
	}

	mgr.appliedLSN = lsn

	// There is no awaiter for a transaction if the transaction manager is recovering
	// transactions from the log after starting up.
	if resultChan, ok := mgr.awaitingTransactions[lsn]; ok {
		resultChan <- nil
		delete(mgr.awaitingTransactions, lsn)
	}

	// Notify the transactions waiting for this log entry to be applied prior to take their
	// snapshot.
	close(mgr.snapshotLocks[lsn].applied)

	return nil
}

// createRepository creates a repository at the given path with the given object format.
func (mgr *TransactionManager) createRepository(ctx context.Context, repositoryPath string, objectFormat gitalypb.ObjectFormat) error {
	objectHash, err := git.ObjectHashByProto(objectFormat)
	if err != nil {
		return fmt.Errorf("object hash by proto: %w", err)
	}

	stderr := &bytes.Buffer{}
	cmd, err := mgr.commandFactory.NewWithoutRepo(ctx, git.Command{
		Name: "init",
		Flags: []git.Option{
			git.Flag{Name: "--bare"},
			git.Flag{Name: "--quiet"},
			git.Flag{Name: "--object-format=" + objectHash.Format},
		},
		Args: []string{repositoryPath},
	}, git.WithStderr(stderr))
	if err != nil {
		return fmt.Errorf("spawn git init: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return structerr.New("wait git init: %w", err).WithMetadata("stderr", stderr.String())
	}

	return nil
}

// applyHousekeeping applies housekeeping results to the target repository.
func (mgr *TransactionManager) applyHousekeeping(ctx context.Context, lsn LSN, logEntry *gitalypb.LogEntry) error {
	if logEntry.Housekeeping == nil {
		return nil
	}

	span, _ := tracing.StartSpanIfHasParent(mgr.ctx, "transaction.applyHousekeeping", nil)
	defer span.Finish()

	if err := mgr.applyPackRefs(ctx, lsn, logEntry); err != nil {
		return fmt.Errorf("applying pack refs: %w", err)
	}

	if err := mgr.applyRepacking(ctx, lsn, logEntry); err != nil {
		return fmt.Errorf("applying repacking: %w", err)
	}

	if err := mgr.applyCommitGraphs(ctx, lsn, logEntry); err != nil {
		return fmt.Errorf("applying the commit graph: %w", err)
	}

	return nil
}

func (mgr *TransactionManager) applyPackRefs(ctx context.Context, lsn LSN, logEntry *gitalypb.LogEntry) error {
	if logEntry.Housekeeping.PackRefs == nil {
		return nil
	}

	span, _ := tracing.StartSpanIfHasParent(mgr.ctx, "transaction.applyPackRefs", nil)
	defer span.Finish()

	repositoryPath := mgr.getAbsolutePath(logEntry.RelativePath)
	// Remove packed-refs lock. While we shouldn't be producing any new stale locks, it makes sense to have
	// this for historic state until we're certain none of the repositories contain stale locks anymore.
	// This clean up is not needed afterward.
	if err := mgr.removePackedRefsLocks(ctx, repositoryPath); err != nil {
		return fmt.Errorf("applying pack-refs: %w", err)
	}

	packedRefsPath := filepath.Join(repositoryPath, "packed-refs")
	// Replace the packed-refs file.
	if err := os.Remove(packedRefsPath); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("removing existing pack-refs: %w", err)
		}
	}
	if err := os.Link(
		filepath.Join(walFilesPathForLSN(mgr.stateDirectory, lsn), "packed-refs"),
		packedRefsPath,
	); err != nil {
		return fmt.Errorf("linking new packed-refs: %w", err)
	}

	modifiedDirs := map[string]struct{}{}
	// Prune loose references. The log entry carries the list of fully qualified references to prune.
	for _, ref := range logEntry.Housekeeping.PackRefs.PrunedRefs {
		path := filepath.Join(repositoryPath, string(ref))
		if err := os.Remove(path); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return structerr.New("pruning loose reference: %w", err).WithMetadata("ref", path)
			}
		}
		modifiedDirs[filepath.Dir(path)] = struct{}{}
	}

	syncer := safe.NewSyncer()
	// Traverse all modified dirs back to the root "refs" dir of the repository. Remove any empty directory
	// along the way. It prevents leaving empty dirs around after a loose ref is pruned. `git-pack-refs`
	// command does dir removal for us, but in staginge repository during preparation stage. In the actual
	// repository,  we need to do it ourselves.
	rootRefDir := filepath.Join(repositoryPath, "refs")
	for dir := range modifiedDirs {
		for dir != rootRefDir {
			if isEmpty, err := isDirEmpty(dir); err != nil {
				// If a dir does not exist, it properly means a directory may already be deleted by a
				// previous interrupted attempt on applying the log entry. We simply ignore the error
				// and move up the directory hierarchy.
				if errors.Is(err, fs.ErrNotExist) {
					dir = filepath.Dir(dir)
					continue
				} else {
					return fmt.Errorf("checking empty ref dir: %w", err)
				}
			} else if !isEmpty {
				break
			}

			if err := os.Remove(dir); err != nil {
				return fmt.Errorf("removing empty ref dir: %w", err)
			}
			dir = filepath.Dir(dir)
		}
		// If there is any empty dir along the way, it's removed and dir pointer moves up until the dir
		// is not empty or reaching the root dir. That one should be fsynced to flush the dir removal.
		// If there is no empty dir, it stays at the dir of pruned refs, which also needs a flush.
		if err := syncer.Sync(dir); err != nil {
			return fmt.Errorf("sync dir: %w", err)
		}
	}

	// Sync the root of the repository to flush packed-refs replacement.
	if err := syncer.SyncParent(packedRefsPath); err != nil {
		return fmt.Errorf("sync parent: %w", err)
	}
	return nil
}

// applyRepacking applies the new packfile set and removed known pruned packfiles. New packfiles created by concurrent
// changes are kept intact.
func (mgr *TransactionManager) applyRepacking(ctx context.Context, lsn LSN, logEntry *gitalypb.LogEntry) error {
	if logEntry.Housekeeping.Repack == nil {
		return nil
	}

	span, _ := tracing.StartSpanIfHasParent(mgr.ctx, "transaction.applyRepacking", nil)
	defer span.Finish()

	repack := logEntry.Housekeeping.Repack
	repoPath := mgr.getAbsolutePath(logEntry.RelativePath)

	if err := mgr.replacePackfiles(repoPath, walFilesPathForLSN(mgr.stateDirectory, lsn), repack.NewFiles, repack.DeletedFiles); err != nil {
		return fmt.Errorf("applying packfiles into destination repository: %w", err)
	}

	// During migration to the new transaction system, loose objects might still exist here and there. So, this task
	// needs to clean up redundant loose objects. After the target repository runs repacking for the first time,
	// there shouldn't be any further loose objects. All of them exist in packfiles. Afterward, this command will
	// exist instantly. We can remove this run after the transaction system is fully applied.
	repo := mgr.repositoryFactory.Build(logEntry.RelativePath)
	var stderr bytes.Buffer
	if err := repo.ExecAndWait(ctx, git.Command{
		Name:  "prune-packed",
		Flags: []git.Option{git.Flag{Name: "--quiet"}},
	}, git.WithStderr(&stderr)); err != nil {
		return structerr.New("exec prune-packed: %w", err).WithMetadata("stderr", stderr.String())
	}

	if repack.IsFullRepack {
		if err := stats.UpdateFullRepackTimestamp(mgr.getAbsolutePath(logEntry.RelativePath), time.Now()); err != nil {
			return fmt.Errorf("updating repack timestamp: %w", err)
		}
	}

	if err := safe.NewSyncer().Sync(filepath.Join(repoPath, "objects")); err != nil {
		return fmt.Errorf("sync objects dir: %w", err)
	}
	return nil
}

// applyCommitGraphs replaces the existing commit-graph folder or monolithic file by the new commit-graph chain. The
// new chain can be identical to the existing chain, but the fee of checking is similar to the replacement fee. Thus, we
// can simply remove and link new directory over. Apart from commit-graph task, the repacking task also replaces the
// chain before verification.
func (mgr *TransactionManager) applyCommitGraphs(ctx context.Context, lsn LSN, logEntry *gitalypb.LogEntry) error {
	if logEntry.Housekeeping.WriteCommitGraphs == nil {
		return nil
	}

	span, _ := tracing.StartSpanIfHasParent(mgr.ctx, "transaction.applyCommitGraphs", nil)
	defer span.Finish()

	repoPath := mgr.getAbsolutePath(logEntry.RelativePath)
	if err := mgr.replaceCommitGraphs(repoPath, walFilesPathForLSN(mgr.stateDirectory, lsn)); err != nil {
		return fmt.Errorf("rewriting commit-graph: %w", err)
	}

	return nil
}

// replacePackfiles replaces the set of packfiles at the destination repository by the new set of packfiles from WAL
// directory. Any packfile outside the input deleted files are kept intact.
func (mgr *TransactionManager) replacePackfiles(repoPath string, walPath string, newFiles []string, deletedFiles []string) error {
	for _, file := range newFiles {
		if err := os.Link(
			filepath.Join(walPath, file),
			filepath.Join(repoPath, "objects", "pack", file),
		); err != nil {
			// A new resulting packfile might exist if the log entry is re-applied after a crash.
			if !errors.Is(err, os.ErrExist) {
				return fmt.Errorf("linking new packfile: %w", err)
			}
		}
	}

	for _, file := range deletedFiles {
		if err := os.Remove(filepath.Join(repoPath, "objects", "pack", file)); err != nil {
			// Repacking task is the only operation that touch an on-disk packfile. Other operations should
			// only create new packfiles. However, after a crash, a pre-existing packfile might be cleaned up
			// beforehand.
			if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("clean up repacked packfile: %w", err)
			}
		}
	}

	if err := safe.NewSyncer().Sync(filepath.Join(repoPath, "objects", "pack")); err != nil {
		return fmt.Errorf("sync objects/pack dir: %w", err)
	}

	return nil
}

func (mgr *TransactionManager) replaceCommitGraphs(repoPath string, walPath string) error {
	// Clean up and apply commit graphs.
	walGraphsDir := filepath.Join(walPath, "commit-graphs")
	if graphEntries, err := os.ReadDir(walGraphsDir); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reading dir: %w", err)
		}
	} else {
		if err := os.Remove(filepath.Join(repoPath, "objects", "info", "commit-graph")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("resetting commit-graph file: %w", err)
		}
		commitGraphsDir := filepath.Join(repoPath, "objects", "info", "commit-graphs")
		if err := os.RemoveAll(commitGraphsDir); err != nil {
			return fmt.Errorf("resetting commit-graphs dir: %w", err)
		}
		if err := os.Mkdir(commitGraphsDir, perm.PrivateDir); err != nil {
			return fmt.Errorf("creating commit-graphs dir: %w", err)
		}
		for _, entry := range graphEntries {
			if err := os.Link(
				filepath.Join(walGraphsDir, entry.Name()),
				filepath.Join(commitGraphsDir, entry.Name()),
			); err != nil {
				return fmt.Errorf("linking commit-graph entry: %w", err)
			}
		}
		if err := safe.NewSyncer().Sync(commitGraphsDir); err != nil {
			return fmt.Errorf("sync objects/pack dir: %w", err)
		}
		if err := safe.NewSyncer().SyncParent(commitGraphsDir); err != nil {
			return fmt.Errorf("sync objects/pack dir: %w", err)
		}
	}
	return nil
}

// isDirEmpty checks if a directory is empty.
func isDirEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer f.Close()

	// Read at most one entry from the directory. If we get EOF, the directory is empty
	if _, err = f.Readdirnames(1); errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

// deleteLogEntry deletes the log entry at the given LSN from the log.
func (mgr *TransactionManager) deleteLogEntry(lsn LSN) error {
	tmpDir, err := os.MkdirTemp(mgr.stagingDirectory, "")
	if err != nil {
		return fmt.Errorf("mkdir temp: %w", err)
	}

	logEntryPath := walFilesPathForLSN(mgr.stateDirectory, lsn)
	// We can't delete a directory atomically as we have to first delete all of its content.
	// If the deletion was interrupted, we'd be left with a corrupted log entry on the disk.
	// To perform the deletion atomically, we move the to be deleted log entry out from the
	// log into a temporary directory and sync the move. After that, the log entry is no longer
	// in the log, and we can delete the files without having to worry about the deletion being
	// interrupted and being left with a corrupted log entry.
	if err := os.Rename(logEntryPath, filepath.Join(tmpDir, "to_delete")); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	if err := safe.NewSyncer().SyncParent(logEntryPath); err != nil {
		return fmt.Errorf("sync file deletion: %w", err)
	}

	// With the log entry removed from the log, we can now delete the files. There's no need
	// to sync the deletions as the log entry is a temporary directory that will be removed
	// on start up if they are left around from a crash.
	if err := os.RemoveAll(tmpDir); err != nil {
		return fmt.Errorf("remove files: %w", err)
	}

	return nil
}

// readLogEntry returns the log entry from the given position in the log.
func (mgr *TransactionManager) readLogEntry(lsn LSN) (*gitalypb.LogEntry, error) {
	manifestBytes, err := os.ReadFile(manifestPath(walFilesPathForLSN(mgr.stateDirectory, lsn)))
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var logEntry gitalypb.LogEntry
	if err := proto.Unmarshal(manifestBytes, &logEntry); err != nil {
		return nil, fmt.Errorf("unmarshal manifest: %w", err)
	}

	return &logEntry, nil
}

// storeAppliedLSN stores the partition's applied LSN in the database.
func (mgr *TransactionManager) storeAppliedLSN(lsn LSN) error {
	return mgr.setKey(keyAppliedLSN(mgr.partitionID), lsn.toProto())
}

// setKey marshals and stores a given protocol buffer message into the database under the given key.
func (mgr *TransactionManager) setKey(key []byte, value proto.Message) error {
	marshaledValue, err := proto.Marshal(value)
	if err != nil {
		return fmt.Errorf("marshal value: %w", err)
	}

	writeBatch := mgr.db.NewWriteBatch()
	defer writeBatch.Cancel()

	if err := writeBatch.Set(key, marshaledValue); err != nil {
		return fmt.Errorf("set: %w", err)
	}

	return writeBatch.Flush()
}

// readKey reads a key from the database and unmarshals its value in to the destination protocol
// buffer message.
func (mgr *TransactionManager) readKey(key []byte, destination proto.Message) error {
	return mgr.db.View(func(txn DatabaseTransaction) error {
		item, err := txn.Get(key)
		if err != nil {
			return fmt.Errorf("get: %w", err)
		}

		return item.Value(func(value []byte) error { return proto.Unmarshal(value, destination) })
	})
}

// lowWaterMark returns the earliest LSN of log entries which should be kept in the database. Any log entries LESS than
// this mark are removed.
func (mgr *TransactionManager) lowWaterMark() LSN {
	mgr.mutex.Lock()
	defer mgr.mutex.Unlock()

	elm := mgr.committedEntries.Front()
	if elm == nil {
		return mgr.appliedLSN + 1
	}
	return elm.Value.(*committedEntry).lsn
}

// updateCommittedEntry updates the reader counter of the committed entry of the snapshot that this transaction depends on.
func (mgr *TransactionManager) updateCommittedEntry(snapshotLSN LSN) (*committedEntry, error) {
	// Since the goroutine doing this is holding the lock, the snapshotLSN shouldn't change and no new transactions
	// can be committed or added. That should guarantee .Back() is always the latest transaction and the one we're
	// using to base our snapshot on.
	if elm := mgr.committedEntries.Back(); elm != nil {
		entry := elm.Value.(*committedEntry)
		entry.snapshotReaders++
		return entry, nil
	}

	entry := &committedEntry{
		lsn:             snapshotLSN,
		snapshotReaders: 1,
	}

	mgr.committedEntries.PushBack(entry)

	return entry, nil
}

// walkCommittedEntries walks all committed entries after input transaction's snapshot LSN. It loads the content of the
// entry from disk and triggers the callback with entry content.
func (mgr *TransactionManager) walkCommittedEntries(transaction *Transaction, callback func(*gitalypb.LogEntry) error) error {
	for elm := mgr.committedEntries.Front(); elm != nil; elm = elm.Next() {
		committed := elm.Value.(*committedEntry)
		if committed.lsn <= transaction.snapshotLSN {
			continue
		}
		entry, err := mgr.readLogEntry(committed.lsn)
		if err != nil {
			return errCommittedEntryGone
		}
		// Transaction manager works on the partition level, including a repository and all of its pool
		// member repositories (if any). We need to filter log entries of the repository this
		// transaction targets.
		if entry.RelativePath != transaction.relativePath {
			continue
		}
		if err := callback(entry); err != nil {
			return fmt.Errorf("callback: %w", err)
		}
	}
	return nil
}

// cleanCommittedEntry reduces the snapshot readers counter of the committed entry. It also removes entries with no more
// readers at the head of the list.
func (mgr *TransactionManager) cleanCommittedEntry(entry *committedEntry) error {
	entry.snapshotReaders--

	elm := mgr.committedEntries.Front()
	for elm != nil {
		front := elm.Value.(*committedEntry)
		if front.snapshotReaders > 0 {
			// If the first entry had still some snapshot readers, that means
			// our transaction was not the oldest reader. We can't remove any entries
			// as they'll still be needed for conlict checking the older transactions.
			return nil
		}

		mgr.committedEntries.Remove(elm)
		elm = mgr.committedEntries.Front()
	}
	return nil
}

// keyAppliedLSN returns the database key storing a partition's last applied log entry's LSN.
func keyAppliedLSN(ptnID partitionID) []byte {
	return []byte(fmt.Sprintf("partition/%s/applied_lsn", ptnID.MarshalBinary()))
}
