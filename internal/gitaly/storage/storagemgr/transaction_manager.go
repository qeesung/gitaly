package storagemgr

import (
	"bytes"
	"container/list"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dgraph-io/badger/v4"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/repoutil"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
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

// LSN is a log sequence number that points to a specific position in the partition's write-ahead log.
type LSN uint64

// toProto returns the protobuf representation of LSN for serialization purposes.
func (lsn LSN) toProto() *gitalypb.LSN {
	return &gitalypb.LSN{Value: uint64(lsn)}
}

// String returns a string representation of the LSN.
func (lsn LSN) String() string {
	return strconv.FormatUint(uint64(lsn), 10)
}

// ReferenceUpdate describes the state of a reference's old and new tip in an update.
type ReferenceUpdate struct {
	// OldOID is the old OID the reference is expected to point to prior to updating it.
	// If the reference does not point to the old value, the reference verification fails.
	OldOID git.ObjectID
	// NewOID is the new desired OID to point the reference to.
	NewOID git.ObjectID
}

// DefaultBranchUpdate provides the information to update the default branch of the repo.
type DefaultBranchUpdate struct {
	// Reference is the reference to update the default branch to.
	Reference git.ReferenceName
}

// CustomHooksUpdate models an update to the custom hooks.
type CustomHooksUpdate struct {
	// CustomHooksTAR contains the custom hooks as a TAR. The TAR contains a `custom_hooks`
	// directory which contains the hooks. Setting the update with nil `custom_hooks_tar` clears
	// the hooks from the repository.
	CustomHooksTAR []byte
}

// repositoryCreation models a repository creation in a transaction.
type repositoryCreation struct {
	// objectHash defines the object format the repository is created with.
	objectHash git.ObjectHash
}

// runHousekeeping models housekeeping tasks. It is supposed to handle housekeeping tasks for repositories
// such as the cleanup of unneeded files and optimizations for the repository's data structures.
type runHousekeeping struct {
	packRefs *runPackRefs
}

// runPackRefs models refs packing housekeeping task. It packs heads and tags for efficient repository access.
type runPackRefs struct {
	// PrunedRefs contain a list of references pruned by the `git-pack-refs` command. They are used
	// for comparing to the ref list of the destination repository
	PrunedRefs map[git.ReferenceName]struct{}
}

// ReferenceUpdates contains references to update. Reference name is used as the key and the value
// is the expected old tip and the desired new tip.
type ReferenceUpdates map[git.ReferenceName]ReferenceUpdate

// alternateUpdate models an update to the repository's alternates file.
type alternateUpdate struct {
	// relativePath is the relative path of the repository to link the transasction's target repository to.
	// If not set, the transaction's target repository is disconnected from its current alternate.
	relativePath string
}

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
	// result is where the outcome of the transaction is sent ot by TransactionManager once it
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

	skipVerificationFailures bool
	initialReferenceValues   map[git.ReferenceName]git.ObjectID
	referenceUpdates         []ReferenceUpdates
	defaultBranchUpdate      *DefaultBranchUpdate
	customHooksUpdate        *CustomHooksUpdate
	repositoryCreation       *repositoryCreation
	deleteRepository         bool
	includedObjects          map[git.ObjectID]struct{}
	alternateUpdate          *alternateUpdate
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

		if txn.stagingDirectory != "" {
			if err := os.RemoveAll(txn.stagingDirectory); err != nil {
				return fmt.Errorf("remove staging directory: %w", err)
			}
		}

		if !txn.readOnly {
			mgr.mutex.Lock()
			defer mgr.mutex.Unlock()
			mgr.cleanCommittedEntry(entry)
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
		case txn.defaultBranchUpdate != nil:
			return errReadOnlyDefaultBranchUpdate
		case txn.customHooksUpdate != nil:
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
		txn.defaultBranchUpdate != nil ||
		txn.customHooksUpdate != nil ||
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

// SetDefaultBranch sets the default branch as part of the transaction. If SetDefaultBranch is called
// multiple times, only the changes from the latest invocation take place. The reference is validated
// to exist.
func (txn *Transaction) SetDefaultBranch(new git.ReferenceName) {
	txn.defaultBranchUpdate = &DefaultBranchUpdate{Reference: new}
}

// SetCustomHooks sets the custom hooks as part of the transaction. If SetCustomHooks is called multiple
// times, only the changes from the latest invocation take place. The custom hooks are extracted as is and
// are not validated. Setting a nil hooksTAR removes the hooks from the repository.
func (txn *Transaction) SetCustomHooks(customHooksTAR []byte) {
	txn.customHooksUpdate = &CustomHooksUpdate{CustomHooksTAR: customHooksTAR}
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

// IncludeObject includes the given object and its dependencies in the transaction's logged pack file even
// if the object is unreachable from the references.
func (txn *Transaction) IncludeObject(oid git.ObjectID) {
	if txn.includedObjects == nil {
		txn.includedObjects = map[git.ObjectID]struct{}{}
	}

	txn.includedObjects[oid] = struct{}{}
}

// UpdateAlternate stages an update for the transaction's target repository's 'objects/info/alternates' file.
// If a relative path is provided, the target repository is linked to the given repository when the
// transaction commits. If the relative path is an empty string, the target is unlinked from the current alternate.
func (txn *Transaction) UpdateAlternate(relativePath string) {
	txn.alternateUpdate = &alternateUpdate{relativePath}
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
	// entry is the pointer to the corresponding log entry.
	entry *gitalypb.LogEntry
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
	// housekeepingManager access to the housekeeping.Manager.
	housekeepingManager housekeeping.Manager

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
	housekeepingManager housekeeping.Manager,
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
		initialized:          make(chan struct{}),
		snapshotLocks:        make(map[LSN]*snapshotLock),
		stateDirectory:       stateDir,
		stagingDirectory:     stagingDir,
		housekeepingManager:  housekeepingManager,
		awaitingTransactions: make(map[LSN]resultChannel),
		committedEntries:     list.New(),
	}
}

// resultChannel represents a future that will yield the result of a transaction once its
// outcome has been decided.
type resultChannel chan error

// commit queues the transaction for processing and returns once the result has been determined.
func (mgr *TransactionManager) commit(ctx context.Context, transaction *Transaction) error {
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

	if err := mgr.setupStagingRepository(ctx, transaction); err != nil {
		return fmt.Errorf("setup staging repository: %w", err)
	}

	if err := mgr.stageHooks(ctx, transaction); err != nil {
		return fmt.Errorf("stage hooks: %w", err)
	}

	if err := mgr.packObjects(ctx, transaction); err != nil {
		return fmt.Errorf("pack objects: %w", err)
	}

	if err := mgr.prepareHousekeeping(ctx, transaction); err != nil {
		return fmt.Errorf("preparing housekeeping: %w", err)
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

// stageRepositoryCreation determines the repository's state following a creation. It reads the repository's
// complete state and stages it into the transaction for committing.
func (mgr *TransactionManager) stageRepositoryCreation(ctx context.Context, transaction *Transaction) error {
	objectHash, err := transaction.snapshotRepository.ObjectHash(ctx)
	if err != nil {
		return fmt.Errorf("object hash: %w", err)
	}

	transaction.repositoryCreation = &repositoryCreation{
		objectHash: objectHash,
	}

	head, err := transaction.snapshotRepository.HeadReference(ctx)
	if err != nil {
		return fmt.Errorf("head reference: %w", err)
	}

	transaction.SetDefaultBranch(head)

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
		transaction.SetCustomHooks(customHooks.Bytes())
	}

	if alternate, err := readAlternatesFile(mgr.getAbsolutePath(transaction.snapshotRepository.GetRelativePath())); err != nil {
		if !errors.Is(err, errNoAlternate) {
			return fmt.Errorf("read alternates file: %w", err)
		}

		// Repository had no alternate.
	} else {
		alternateObjectsDir, err := filepath.Rel(
			mgr.getAbsolutePath(transaction.snapshot.prefix),
			mgr.getAbsolutePath(
				filepath.Join(transaction.snapshot.prefix, transaction.relativePath, "objects", alternate),
			),
		)
		if err != nil {
			return fmt.Errorf("rel alternate relative path: %w", err)
		}

		transaction.UpdateAlternate(filepath.Dir(alternateObjectsDir))
	}

	return nil
}

// setupStagingRepository sets a repository that is used to stage the transaction. The staging repository
// has the quarantine applied so the objects are available for packing and verifying the references.
func (mgr *TransactionManager) setupStagingRepository(ctx context.Context, transaction *Transaction) error {
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

// stageHooks extracts the new hooks, if any, into <stagingDirectory>/custom_hooks. This is ensures the TAR
// is valid prior to committing the transaction. The hooks files on the disk are also used to compute a vote
// for Praefect.
func (mgr *TransactionManager) stageHooks(ctx context.Context, transaction *Transaction) error {
	if transaction.customHooksUpdate == nil || len(transaction.customHooksUpdate.CustomHooksTAR) == 0 {
		return nil
	}

	if err := repoutil.ExtractHooks(
		ctx,
		mgr.logger,
		bytes.NewReader(transaction.customHooksUpdate.CustomHooksTAR),
		transaction.stagingDirectory,
		false,
	); err != nil {
		return fmt.Errorf("extract hooks: %w", err)
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
			Args:  []string{filepath.Join(transaction.walFilesPath(), "objects.pack")},
		}, git.WithStdin(packReader), git.WithStdout(&stdout), git.WithStderr(&stderr)); err != nil {
			return structerr.New("index pack: %w", err).WithMetadata("stderr", stderr.String())
		}

		matches := packPrefixRegexp.FindStringSubmatch(stdout.String())
		if len(matches) != 2 {
			return structerr.New("unexpected index-pack output").WithMetadata("stdout", stdout.String())
		}

		// Sync the files and the directory entries so everything is flushed to the disk prior
		// to moving on to committing the log entry. This way we only have to flush the directory
		// move when we move the staged files into the log.
		if err := safe.NewSyncer().SyncRecursive(transaction.walFilesPath()); err != nil {
			return fmt.Errorf("sync recursive: %w", err)
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
	if err := mgr.preparePackRefs(ctx, transaction); err != nil {
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

	if err := os.Chmod(filepath.Join(filepath.Join(repoPath, "packed-refs")), perm.SharedReadOnlyFile); err != nil {
		return fmt.Errorf("modifying permission of pack-refs file")
	}

	// Copy the resulting packed-refs file to the WAL directory.
	if err := os.Link(
		filepath.Join(filepath.Join(repoPath, "packed-refs")),
		filepath.Join(transaction.walFilesPath(), "packed-refs"),
	); err != nil {
		return fmt.Errorf("copying packed-refs file to WAL directory: %w", err)
	}
	if err := safe.NewSyncer().Sync(transaction.walFilesPath()); err != nil {
		return fmt.Errorf("sync: %w", err)
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

		if err := mgr.processTransaction(); err != nil {
			return fmt.Errorf("process transaction: %w", err)
		}
	}
}

// processTransaction waits for a transaction and processes it by verifying and
// logging it.
func (mgr *TransactionManager) processTransaction() (returnedErr error) {
	var cleanUps []func() error
	defer func() {
		for _, cleanUp := range cleanUps {
			if err := cleanUp(); err != nil && returnedErr == nil {
				returnedErr = fmt.Errorf("clean up: %w", err)
			}
		}
	}()

	var transaction *Transaction
	select {
	case transaction = <-mgr.admissionQueue:
		// The Transaction does not finish itself anymore once it has been admitted for
		// processing. This avoids the Transaction concurrently removing the staged state
		// while the manager is still operating on it. We thus need to defer its finishing.
		cleanUps = append(cleanUps, transaction.finish)
	case <-mgr.ctx.Done():
	}

	// Return if the manager was stopped. The select is indeterministic so this guarantees
	// the manager stops the processing even if there are transactions in the queue.
	if err := mgr.ctx.Err(); err != nil {
		return err
	}

	if err := func() (commitErr error) {
		repositoryExists, err := mgr.doesRepositoryExist(transaction.relativePath)
		if err != nil {
			return fmt.Errorf("does repository exist: %w", err)
		}

		logEntry := &gitalypb.LogEntry{
			RelativePath: transaction.relativePath,
		}

		if transaction.repositoryCreation != nil {
			if repositoryExists {
				return ErrRepositoryAlreadyExists
			}

			logEntry.RepositoryCreation = &gitalypb.LogEntry_RepositoryCreation{
				ObjectFormat: transaction.repositoryCreation.objectHash.ProtoFormat,
			}
		} else if !repositoryExists {
			return ErrRepositoryNotFound
		}

		if logEntry.AlternateUpdate, err = mgr.verifyAlternateUpdate(transaction); err != nil {
			return fmt.Errorf("verify alternate update: %w", err)
		}

		logEntry.ReferenceTransactions, err = mgr.verifyReferences(mgr.ctx, transaction)
		if err != nil {
			return fmt.Errorf("verify references: %w", err)
		}

		if transaction.defaultBranchUpdate != nil {
			if err := mgr.verifyDefaultBranchUpdate(mgr.ctx, transaction); err != nil {
				return fmt.Errorf("verify default branch update: %w", err)
			}

			logEntry.DefaultBranchUpdate = &gitalypb.LogEntry_DefaultBranchUpdate{
				ReferenceName: []byte(transaction.defaultBranchUpdate.Reference),
			}
		}

		if transaction.customHooksUpdate != nil {
			logEntry.CustomHooksUpdate = &gitalypb.LogEntry_CustomHooksUpdate{
				CustomHooksTar: transaction.customHooksUpdate.CustomHooksTAR,
			}
		}

		nextLSN := mgr.appendedLSN + 1
		var shouldStoreWALFiles bool

		if transaction.packPrefix != "" {
			shouldStoreWALFiles = true
			logEntry.PackPrefix = transaction.packPrefix
		}

		if transaction.deleteRepository {
			logEntry.RepositoryDeletion = &gitalypb.LogEntry_RepositoryDeletion{}
		}

		if transaction.runHousekeeping != nil {
			shouldStoreWALFiles = true
			housekeepingEntry, err := mgr.verifyHousekeeping(mgr.ctx, transaction)
			if err != nil {
				return fmt.Errorf("verifying pack refs: %w", err)
			}
			logEntry.Housekeeping = housekeepingEntry
		}

		if shouldStoreWALFiles {
			removeFiles, err := mgr.storeWALFiles(mgr.ctx, nextLSN, transaction)
			cleanUps = append(cleanUps, func() error {
				// The transaction's files might have been moved successfully in to the log.
				// If anything fails before the transaction is committed, the files must be removed as otherwise
				// they would occupy the slot of the next log entry. If this can't be done, the TransactionManager
				// will exit with an error. The files will be cleaned up on restart and no further processing is
				// allowed until that happens.
				if commitErr != nil {
					return removeFiles()
				}

				return nil
			})

			if err != nil {
				return fmt.Errorf("store wal files: %w", err)
			}
		}

		return mgr.appendLogEntry(nextLSN, logEntry)
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

	// The LSN of the last appended log entry is determined from the LSN of the latest entry in the log and
	// the latest applied log entry. If there is a log entry, it is the latest appended log entry. If there are no
	// log entries, the latest log entry must have been applied to the repository and pruned away, meaning the LSN
	// of the last appended log entry is the same as the LSN if the last applied log entry.
	//
	// As the LSNs in the keys are encoded in big endian, the latest log entry can be found by taking
	// the first key when iterating the log entry key space in reverse.
	if err := mgr.db.View(func(txn DatabaseTransaction) error {
		logPrefix := keyPrefixLogEntries(mgr.partitionID)

		iterator := txn.NewIterator(badger.IteratorOptions{Reverse: true, Prefix: logPrefix})
		defer iterator.Close()

		mgr.appendedLSN = mgr.appliedLSN

		// The iterator seeks to a key that is greater than or equal than sought key. Since we are doing a reverse
		// seek, we need to add 0xff to the prefix so the first iterated key is the latest log entry.
		if iterator.Seek(append(logPrefix, 0xff)); iterator.Valid() {
			mgr.appendedLSN = LSN(binary.BigEndian.Uint64(bytes.TrimPrefix(iterator.Item().Key(), logPrefix)))
		}

		return nil
	}); err != nil {
		return fmt.Errorf("determine appended LSN: %w", err)
	}

	if err := mgr.createStateDirectory(); err != nil {
		return fmt.Errorf("create state directory: %w", err)
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

	if err := mgr.removeStaleWALFiles(mgr.ctx, mgr.appendedLSN); err != nil {
		return fmt.Errorf("remove stale packs: %w", err)
	}

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
func (mgr *TransactionManager) getAbsolutePath(relativePath string) string {
	return filepath.Join(mgr.storagePath, relativePath)
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
func (mgr *TransactionManager) removeStaleWALFiles(ctx context.Context, appendedLSN LSN) error {
	// Log entries are appended one by one to the log. If a write is interrupted, the only possible stale
	// pack would be for the next LSN. Remove the pack if it exists.
	possibleStaleFilesPath := walFilesPathForLSN(mgr.stateDirectory, appendedLSN+1)
	if _, err := os.Stat(possibleStaleFilesPath); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove: %w", err)
		}

		return nil
	}

	if err := os.RemoveAll(possibleStaleFilesPath); err != nil {
		return fmt.Errorf("remove all: %w", err)
	}

	// Sync the parent directory to flush the file deletion.
	if err := safe.NewSyncer().SyncParent(possibleStaleFilesPath); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	return nil
}

// storeWALFiles moves the transaction's logged files from the staging directory to their destination in the log.
// It returns a function, even on errors, that must be called to clean up the files if committing the log entry
// fails.
func (mgr *TransactionManager) storeWALFiles(ctx context.Context, lsn LSN, transaction *Transaction) (func() error, error) {
	removeFiles := func() error { return nil }

	destinationPath := walFilesPathForLSN(mgr.stateDirectory, lsn)
	if err := os.Rename(
		transaction.walFilesPath(),
		destinationPath,
	); err != nil {
		return removeFiles, fmt.Errorf("move wal files: %w", err)
	}

	removeFiles = func() error {
		if err := os.RemoveAll(destinationPath); err != nil {
			return fmt.Errorf("remove wal files: %w", err)
		}

		return nil
	}

	// Sync the parent directory. The pack's contents are synced when the pack file is computed.
	if err := safe.NewSyncer().Sync(filepath.Dir(destinationPath)); err != nil {
		return removeFiles, fmt.Errorf("sync: %w", err)
	}

	return removeFiles, nil
}

// walFilesPathForLSN returns an absolute path to a given log entry's WAL files.
func walFilesPathForLSN(stateDir string, lsn LSN) string {
	return filepath.Join(stateDir, "wal", lsn.String())
}

// packFilePath returns a log entry's pack file's absolute path in the wal files directory.
func packFilePath(walFiles string) string {
	return filepath.Join(walFiles, "transaction.pack")
}

// verifyAlternateUpdate verifies the staged alternate update.
func (mgr *TransactionManager) verifyAlternateUpdate(transaction *Transaction) (*gitalypb.LogEntry_AlternateUpdate, error) {
	if transaction.alternateUpdate == nil {
		return nil, nil
	}

	repositoryPath := mgr.getAbsolutePath(transaction.relativePath)
	alternates, err := stats.ReadAlternatesFile(repositoryPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("read alternates file: %w", err)
		}

		// No alternates file existed.
	}

	if transaction.alternateUpdate.relativePath == "" {
		if len(alternates) == 0 {
			return nil, errNoAlternate
		}

		return &gitalypb.LogEntry_AlternateUpdate{}, nil
	}

	if len(alternates) > 0 {
		return nil, errAlternateAlreadyLinked
	}

	alternateRelativePath, err := storage.ValidateRelativePath(mgr.storagePath, transaction.alternateUpdate.relativePath)
	if err != nil {
		return nil, fmt.Errorf("validate relative path: %w", err)
	}

	if alternateRelativePath == transaction.relativePath {
		return nil, errAlternatePointsToSelf
	}

	alternateRepositoryPath := mgr.getAbsolutePath(alternateRelativePath)
	if err := storage.ValidateGitDirectory(alternateRepositoryPath); err != nil {
		return nil, fmt.Errorf("validate git directory: %w", err)
	}

	if _, err := readAlternatesFile(alternateRepositoryPath); !errors.Is(err, errNoAlternate) {
		if err == nil {
			return nil, errAlternateHasAlternate
		}

		return nil, fmt.Errorf("read alternates file: %w", err)
	}

	alternatePath, err := filepath.Rel(
		filepath.Join(transaction.relativePath, "objects"),
		filepath.Join(alternateRelativePath, "objects"),
	)
	if err != nil {
		return nil, fmt.Errorf("rel: %w", err)
	}

	return &gitalypb.LogEntry_AlternateUpdate{
		Path: alternatePath,
	}, nil
}

// verifyReferences verifies that the references in the transaction apply on top of the already accepted
// reference changes. The old tips in the transaction are verified against the current actual tips.
// It returns the write-ahead log entry for the reference transactions successfully verified.
func (mgr *TransactionManager) verifyReferences(ctx context.Context, transaction *Transaction) ([]*gitalypb.LogEntry_ReferenceTransaction, error) {
	if len(transaction.referenceUpdates) == 0 {
		return nil, nil
	}

	conflictingReferenceUpdates := map[git.ReferenceName]struct{}{}
	for referenceName, update := range transaction.flattenReferenceTransactions() {
		if err := git.ValidateReference(string(referenceName)); err != nil {
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

	repo, err := mgr.repositoryFactory.Build(snapshot.relativePath(relativePath)).Quarantine(tx.quarantineDirectory)
	if err != nil {
		return fmt.Errorf("quarantine: %w", err)
	}

	for _, referenceTransaction := range referenceTransactions {
		updater, err := mgr.prepareReferenceTransaction(ctx, referenceTransaction.Changes, repo)
		if err != nil {
			return fmt.Errorf("prepare reference transaction: %w", err)
		}

		if err := updater.Commit(); err != nil {
			return fmt.Errorf("commit: %w", err)
		}
	}

	return nil
}

// verifyDefaultBranchUpdate verifies the default branch reference update. This is done by first checking if it is one of
// the references in the current transaction which is not scheduled to be deleted. If not, we check if its a valid reference
// name in the repository. We don't do reference name validation because any reference going through the transaction manager
// has name validation and we can rely on that.
func (mgr *TransactionManager) verifyDefaultBranchUpdate(ctx context.Context, transaction *Transaction) error {
	referenceName := transaction.defaultBranchUpdate.Reference

	if err := git.ValidateReference(referenceName.String()); err != nil {
		return InvalidReferenceFormatError{ReferenceName: referenceName}
	}

	return nil
}

// verifyHousekeeping verifies if all included housekeeping tasks can be performed. Although it's feasible for multiple
// housekeeping tasks running at the same time, it's not guaranteed they are conflict-free. So, we need to ensure there
// is no other concurrent housekeeping task. Each sub-task also needs specific verification.
func (mgr *TransactionManager) verifyHousekeeping(ctx context.Context, transaction *Transaction) (*gitalypb.LogEntry_Housekeeping, error) {
	mgr.mutex.Lock()
	defer mgr.mutex.Unlock()

	// Check for any concurrent housekeeping between this transaction's snapshot LSN and the latest appended LSN.
	elm := mgr.committedEntries.Front()
	for elm != nil {
		entry := elm.Value.(*committedEntry)
		if entry.lsn > transaction.snapshotLSN && entry.entry.RelativePath == transaction.relativePath {
			if entry.entry.GetHousekeeping() != nil {
				return nil, errHousekeepingConflictConcurrent
			}
			if entry.entry.GetRepositoryDeletion() != nil {
				return nil, errConflictRepositoryDeletion
			}
		}
		elm = elm.Next()
	}

	packRefsEntry, err := mgr.verifyPackRefs(mgr.ctx, transaction)
	if err != nil {
		return nil, fmt.Errorf("verifying pack refs: %w", err)
	}

	return &gitalypb.LogEntry_Housekeeping{
		PackRefs: packRefsEntry,
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

	objectHash, err := transaction.stagingRepository.ObjectHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("object hash: %w", err)
	}
	packRefs := transaction.runHousekeeping.packRefs

	// Check for any concurrent ref deletion between this transaction's snapshot LSN to the end.
	elm := mgr.committedEntries.Front()
	for elm != nil {
		entry := elm.Value.(*committedEntry)
		if entry.lsn > transaction.snapshotLSN && entry.entry.RelativePath == transaction.relativePath {
			for _, refTransaction := range entry.entry.ReferenceTransactions {
				for _, change := range refTransaction.Changes {
					if objectHash.IsZeroOID(git.ObjectID(change.GetNewOid())) {
						// Oops, there is a reference deletion. Bail out.
						return nil, errPackRefsConflictRefDeletion
					}
					// Ref update. Remove the updated ref from the list of pruned refs so that the
					// new OID in loose reference shadows the outdated OID in packed-refs.
					delete(packRefs.PrunedRefs, git.ReferenceName(change.GetReferenceName()))
				}
			}
		}
		elm = elm.Next()
	}

	var prunedRefs [][]byte
	for ref := range packRefs.PrunedRefs {
		prunedRefs = append(prunedRefs, []byte(ref))
	}
	return &gitalypb.LogEntry_Housekeeping_PackRefs{
		PrunedRefs: prunedRefs,
	}, nil
}

// applyDefaultBranchUpdate applies the default branch update to the repository from the log entry.
func (mgr *TransactionManager) applyDefaultBranchUpdate(ctx context.Context, logEntry *gitalypb.LogEntry) error {
	if logEntry.DefaultBranchUpdate == nil {
		return nil
	}

	var stderr bytes.Buffer
	if err := mgr.repositoryFactory.Build(logEntry.RelativePath).ExecAndWait(ctx, git.Command{
		Name: "symbolic-ref",
		Args: []string{"HEAD", string(logEntry.DefaultBranchUpdate.ReferenceName)},
	}, git.WithStderr(&stderr), git.WithDisabledHooks()); err != nil {
		return structerr.New("exec symbolic-ref: %w", err).WithMetadata("stderr", stderr.String())
	}

	return nil
}

// prepareReferenceTransaction prepares a reference transaction with `git update-ref`. It leaves committing
// or aborting up to the caller. Either should be called to clean up the process. The process is cleaned up
// if an error is returned.
func (mgr *TransactionManager) prepareReferenceTransaction(ctx context.Context, changes []*gitalypb.LogEntry_ReferenceTransaction_Change, repository *localrepo.Repo) (*updateref.Updater, error) {
	// This section runs git-update-ref(1), but could fail due to existing
	// reference locks. So we create a function which can be called again
	// post cleanup of stale reference locks.
	updateFunc := func() (*updateref.Updater, error) {
		updater, err := updateref.New(ctx, repository, updateref.WithDisabledTransactions(), updateref.WithNoDeref())
		if err != nil {
			return nil, fmt.Errorf("new: %w", err)
		}

		if err := updater.Start(); err != nil {
			return nil, fmt.Errorf("start: %w", err)
		}

		for _, change := range changes {
			if err := updater.Update(git.ReferenceName(change.ReferenceName), git.ObjectID(change.NewOid), ""); err != nil {
				return nil, fmt.Errorf("update %q: %w", change.ReferenceName, err)
			}
		}

		if err := updater.Prepare(); err != nil {
			return nil, fmt.Errorf("prepare: %w", err)
		}

		return updater, nil
	}

	// If git-update-ref(1) runs without issues, our work here is done.
	updater, err := updateFunc()
	if err == nil {
		return updater, nil
	}

	// If we get an error due to existing stale reference locks, we should clear it up
	// and retry running git-update-ref(1).
	if errors.Is(err, updateref.ErrPackedRefsLocked) || errors.As(err, &updateref.AlreadyLockedError{}) {
		repositoryPath := mgr.getAbsolutePath(repository.GetRelativePath())

		// Before clearing stale reference locks, we add should ensure that housekeeping doesn't
		// run git-pack-refs(1), which could create new reference locks. So we add an inhibitor.
		success, cleanup, err := mgr.housekeepingManager.AddPackRefsInhibitor(ctx, repositoryPath)
		if !success {
			return nil, fmt.Errorf("add pack-refs inhibitor: %w", err)
		}
		defer cleanup()

		// We ask housekeeping to cleanup stale reference locks. We don't add a grace period, because
		// transaction manager is the only process which writes into the repository, so it is safe
		// to delete these locks.
		if err := mgr.housekeepingManager.CleanStaleData(ctx, repository, housekeeping.OnlyStaleReferenceLockCleanup(0)); err != nil {
			return nil, fmt.Errorf("running reflock cleanup: %w", err)
		}

		// Remove possible locks and temporary files covering `packed-refs`.
		if err := mgr.removePackedRefsLocks(mgr.ctx, repositoryPath); err != nil {
			return nil, fmt.Errorf("remove stale packed-refs locks: %w", err)
		}

		// We try a second time, this should succeed. If not, there is something wrong and
		// we return the error.
		//
		// Do note, that we've already added an inhibitor above, so git-pack-refs(1) won't run
		// again until we return from this function so ideally this should work, but in case it
		// doesn't we return the error.
		return updateFunc()
	}

	return nil, err
}

// appendLogEntry appends the transaction to the write-ahead log. References that failed verification are skipped and thus not
// logged nor applied later.
func (mgr *TransactionManager) appendLogEntry(nextLSN LSN, logEntry *gitalypb.LogEntry) error {
	if err := mgr.storeLogEntry(nextLSN, logEntry); err != nil {
		return fmt.Errorf("set log entry: %w", err)
	}

	mgr.mutex.Lock()
	mgr.appendedLSN = nextLSN
	mgr.snapshotLocks[nextLSN] = &snapshotLock{applied: make(chan struct{})}
	mgr.committedEntries.PushBack(&committedEntry{
		lsn:   nextLSN,
		entry: logEntry,
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

	if logEntry.RepositoryDeletion != nil {
		// If the repository is being deleted, just delete it without any other changes given
		// they'd all be removed anyway. Reapplying the other changes after a crash would also
		// not work if the repository was successfully deleted before the crash.
		if err := mgr.applyRepositoryDeletion(ctx, logEntry); err != nil {
			return fmt.Errorf("apply repository deletion: %w", err)
		}
	} else {
		if err := mgr.applyRepositoryCreation(ctx, logEntry); err != nil {
			return fmt.Errorf("apply repository creation: %w", err)
		}

		if err := mgr.applyAlternateUpdate(logEntry); err != nil {
			return fmt.Errorf("apply alternate update: %w", err)
		}

		if logEntry.PackPrefix != "" {
			if err := mgr.applyPackFile(ctx, lsn, logEntry); err != nil {
				return fmt.Errorf("apply pack file: %w", err)
			}
		}

		if err := mgr.applyReferenceTransactions(ctx, logEntry); err != nil {
			return fmt.Errorf("apply reference transactions: %w", err)
		}

		if err := mgr.applyDefaultBranchUpdate(ctx, logEntry); err != nil {
			return fmt.Errorf("writing default branch: %w", err)
		}

		if err := mgr.applyCustomHooks(ctx, logEntry); err != nil {
			return fmt.Errorf("apply custom hooks: %w", err)
		}

		if err := mgr.applyHousekeeping(ctx, lsn, logEntry); err != nil {
			return fmt.Errorf("apply housekeeping: %w", err)
		}
	}

	if err := mgr.storeAppliedLSN(lsn); err != nil {
		return fmt.Errorf("set applied LSN: %w", err)
	}

	if err := mgr.deleteLogEntry(lsn); err != nil {
		return fmt.Errorf("deleting log entry: %w", err)
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

// applyAlternateUpdate applies the logged update to the 'objects/info/alternates' file.
func (mgr *TransactionManager) applyAlternateUpdate(entry *gitalypb.LogEntry) error {
	switch {
	case entry.AlternateUpdate == nil:
		return nil
	case entry.AlternateUpdate.Path == "":
		return mgr.applyAlternateUnlink(entry)
	default:
		return mgr.applyAlternateLink(entry)
	}
}

// applyAlternateUnlink unlinks a repository from its alternate. Prior to doing so, it links
// object and pack files from the alternate into the repository.
func (mgr *TransactionManager) applyAlternateUnlink(entry *gitalypb.LogEntry) error {
	repositoryPath := mgr.getAbsolutePath(entry.RelativePath)
	alternatePath, err := readAlternatesFile(repositoryPath)
	if err != nil {
		if errors.Is(err, errNoAlternate) {
			// Partial application of this log entry has already deleted the alternate link.
			return nil
		}

		return fmt.Errorf("read alternates file: %w", err)
	}

	repositoryObjectsDir := filepath.Join(repositoryPath, "objects")
	alternateObjectsDir := filepath.Join(repositoryObjectsDir, alternatePath)

	entries, err := os.ReadDir(alternateObjectsDir)
	if err != nil {
		return fmt.Errorf("read alternate objects dir: %w", err)
	}

	syncer := safe.NewSyncer()
	for _, subDir := range entries {
		if !subDir.IsDir() || !(len(subDir.Name()) == 2 || subDir.Name() == "pack") {
			// Only look in objects/<xx> and objects/pack for files.
			continue
		}

		sourceDir := filepath.Join(alternateObjectsDir, subDir.Name())
		objects, err := os.ReadDir(sourceDir)
		if err != nil {
			return fmt.Errorf("read subdirectory: %w", err)
		}

		if len(objects) == 0 {
			// Don't create empty directories
			continue
		}

		destinationDir := filepath.Join(repositoryObjectsDir, subDir.Name())

		info, err := subDir.Info()
		if err != nil {
			return fmt.Errorf("subdirectory info: %w", err)
		}

		if err := os.Mkdir(destinationDir, info.Mode().Perm()); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create subdirectory: %w", err)
		}

		for _, objectFile := range objects {
			if !objectFile.Type().IsRegular() {
				continue
			}

			if err := os.Link(
				filepath.Join(sourceDir, objectFile.Name()),
				filepath.Join(destinationDir, objectFile.Name()),
			); err != nil && !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("link object: %w", err)
			}
		}

		if err := syncer.Sync(destinationDir); err != nil {
			return fmt.Errorf("sync subdirectory: %w", err)
		}
	}

	if err := syncer.Sync(repositoryObjectsDir); err != nil {
		return fmt.Errorf("sync object directory: %w", err)
	}

	alternatesFilePath := stats.AlternatesFilePath(repositoryPath)
	if err := os.Remove(alternatesFilePath); err != nil {
		return fmt.Errorf("remove: %w", err)
	}

	if err := syncer.SyncParent(alternatesFilePath); err != nil {
		return fmt.Errorf("sync parent: %w", err)
	}

	return nil
}

func (mgr *TransactionManager) applyAlternateLink(entry *gitalypb.LogEntry) error {
	alternatesFilePath := stats.AlternatesFilePath(mgr.getAbsolutePath(entry.RelativePath))

	// When we apply the log entry here, we'd create the file. If we then crash before explicitly
	// syncing the file or the directory, it could be that the directory entry was synced to the
	// disk but not the contents of the file. This could lead to the file existing after a crash
	// with invalid contents. Reapplying the log entry could then fail as the the file already exists.
	// Remove the possible file before proceeding.
	if err := os.Remove(alternatesFilePath); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("remove: %w", err)
		}

		// The alternates file did not exist.
	}

	if err := os.WriteFile(
		alternatesFilePath,
		[]byte(entry.AlternateUpdate.Path),
		perm.PrivateFile,
	); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	syncer := safe.NewSyncer()
	if err := syncer.Sync(alternatesFilePath); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	if err := syncer.SyncParent(alternatesFilePath); err != nil {
		return fmt.Errorf("sync parent: %w", err)
	}

	return nil
}

// applyReferenceTransactions applies the applies the reference transactions to the repository.
func (mgr *TransactionManager) applyReferenceTransactions(ctx context.Context, logEntry *gitalypb.LogEntry) error {
	if len(logEntry.ReferenceTransactions) == 0 {
		return nil
	}

	for _, referenceTransaction := range logEntry.ReferenceTransactions {
		updater, err := mgr.prepareReferenceTransaction(ctx,
			referenceTransaction.Changes,
			mgr.repositoryFactory.Build(logEntry.RelativePath),
		)
		if err != nil {
			return fmt.Errorf("prepare reference transaction: %w", err)
		}

		if err := updater.Commit(); err != nil {
			return fmt.Errorf("commit transaction: %w", err)
		}
	}

	return nil
}

// applyRepositoryCreation applies a repository creation by creating a repository.
func (mgr *TransactionManager) applyRepositoryCreation(ctx context.Context, entry *gitalypb.LogEntry) error {
	if entry.RepositoryCreation == nil {
		return nil
	}

	repositoryPath := mgr.getAbsolutePath(entry.RelativePath)

	// Start with a clean state if we are reapplying the log entry after interruption.
	if err := os.RemoveAll(repositoryPath); err != nil {
		return fmt.Errorf("remove all: %w", err)
	}

	if err := mgr.createRepository(ctx, repositoryPath, entry.RepositoryCreation.ObjectFormat); err != nil {
		return fmt.Errorf("create repository: %w", err)
	}

	syncer := safe.NewSyncer()
	if err := syncer.SyncRecursive(repositoryPath); err != nil {
		return fmt.Errorf("sync recursive: %w", err)
	}

	if err := syncer.SyncParent(repositoryPath); err != nil {
		return fmt.Errorf("sync parent: %w", err)
	}

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

// applyRepositoryDeletion deletes the repository.
func (mgr *TransactionManager) applyRepositoryDeletion(ctx context.Context, logEntry *gitalypb.LogEntry) error {
	repositoryPath := mgr.getAbsolutePath(logEntry.RelativePath)
	if err := os.RemoveAll(repositoryPath); err != nil {
		return fmt.Errorf("remove repository: %w", err)
	}

	if err := safe.NewSyncer().Sync(filepath.Dir(repositoryPath)); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	return nil
}

// applyPackFile unpacks the objects from the pack file into the repository if the log entry
// has an associated pack file. This is done by hard linking the pack and index from the
// log into the repository's object directory.
func (mgr *TransactionManager) applyPackFile(ctx context.Context, lsn LSN, logEntry *gitalypb.LogEntry) error {
	packDirectory := filepath.Join(mgr.getAbsolutePath(logEntry.RelativePath), "objects", "pack")
	for _, fileExtension := range []string{
		".pack",
		".idx",
		".rev",
	} {
		if err := os.Link(
			filepath.Join(walFilesPathForLSN(mgr.stateDirectory, lsn), "objects"+fileExtension),
			filepath.Join(packDirectory, logEntry.PackPrefix+fileExtension),
		); err != nil {
			if !errors.Is(err, fs.ErrExist) {
				return fmt.Errorf("link file: %w", err)
			}

			// The file already existing means that we've already linked it in place or a repack
			// has resulted in the exact same file. No need to do anything about it.
		}
	}

	// Sync the new directory entries created.
	if err := safe.NewSyncer().Sync(packDirectory); err != nil {
		return fmt.Errorf("sync: %w", err)
	}

	return nil
}

// applyCustomHooks applies the custom hooks to the repository from the log entry. The hooks are extracted at
// `<repo>/custom_hooks`. The custom hooks are fsynced prior to returning so it is safe to delete the log entry
// afterwards.
func (mgr *TransactionManager) applyCustomHooks(ctx context.Context, logEntry *gitalypb.LogEntry) error {
	if logEntry.CustomHooksUpdate == nil {
		return nil
	}

	destinationDir := filepath.Join(mgr.getAbsolutePath(logEntry.RelativePath), repoutil.CustomHooksDir)
	if err := os.RemoveAll(destinationDir); err != nil {
		return fmt.Errorf("remove directory: %w", err)
	}

	if err := os.Mkdir(destinationDir, perm.PrivateDir); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	if err := repoutil.ExtractHooks(ctx, mgr.logger, bytes.NewReader(logEntry.CustomHooksUpdate.CustomHooksTar), destinationDir, true); err != nil {
		return fmt.Errorf("extract hooks: %w", err)
	}

	// TAR doesn't sync the extracted files so do it manually here.
	syncer := safe.NewSyncer()
	if err := syncer.SyncRecursive(destinationDir); err != nil {
		return fmt.Errorf("sync hooks: %w", err)
	}

	// Sync the parent directory as well.
	if err := syncer.SyncParent(destinationDir); err != nil {
		return fmt.Errorf("sync hook directory: %w", err)
	}

	return nil
}

// applyHousekeeping applies housekeeping results to the target repository.
func (mgr *TransactionManager) applyHousekeeping(ctx context.Context, lsn LSN, logEntry *gitalypb.LogEntry) error {
	if logEntry.Housekeeping == nil {
		return nil
	}
	repositoryPath := mgr.getAbsolutePath(logEntry.RelativePath)
	if logEntry.Housekeeping.PackRefs != nil {
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
	return mgr.deleteKey(keyLogEntry(mgr.partitionID, lsn))
}

// readLogEntry returns the log entry from the given position in the log.
func (mgr *TransactionManager) readLogEntry(lsn LSN) (*gitalypb.LogEntry, error) {
	var logEntry gitalypb.LogEntry
	key := keyLogEntry(mgr.partitionID, lsn)

	if err := mgr.readKey(key, &logEntry); err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}

	return &logEntry, nil
}

// storeLogEntry stores the log entry in the partition's write-ahead log at the given LSN.
func (mgr *TransactionManager) storeLogEntry(lsn LSN, entry *gitalypb.LogEntry) error {
	return mgr.setKey(keyLogEntry(mgr.partitionID, lsn), entry)
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

// deleteKey deletes a key from the database.
func (mgr *TransactionManager) deleteKey(key []byte) error {
	return mgr.db.Update(func(txn DatabaseTransaction) error {
		if err := txn.Delete(key); err != nil {
			return fmt.Errorf("delete: %w", err)
		}

		return nil
	})
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
		// The log entry is left nil. This doesn't matter as the conflict checking only
		// needs it when checking for conflicts with transactions committed after we took
		// our snapshot.
		//
		// This `committedEntry` only exists to record the `snapshotReaders` at this LSN.
	}

	mgr.committedEntries.PushBack(entry)

	return entry, nil
}

// cleanCommittedEntry reduces the snapshot readers counter of the committed entry. It also removes entries with no more
// readers at the head of the list.
func (mgr *TransactionManager) cleanCommittedEntry(entry *committedEntry) {
	entry.snapshotReaders--

	elm := mgr.committedEntries.Front()
	for elm != nil {
		front := elm.Value.(*committedEntry)
		if front.snapshotReaders > 0 {
			// If the first entry had still some snapshot readers, that means
			// our transaction was not the oldest reader. We can't remove any entries
			// as they'll still be needed for conlict checking the older transactions.
			return
		}
		mgr.committedEntries.Remove(elm)
		elm = mgr.committedEntries.Front()
	}
}

// keyAppliedLSN returns the database key storing a partition's last applied log entry's LSN.
func keyAppliedLSN(ptnID partitionID) []byte {
	return []byte(fmt.Sprintf("partition/%s/applied_lsn", ptnID.MarshalBinary()))
}

// keyLogEntry returns the database key storing a partition's log entry at a given LSN.
func keyLogEntry(ptnID partitionID, lsn LSN) []byte {
	marshaledIndex := make([]byte, binary.Size(lsn))
	binary.BigEndian.PutUint64(marshaledIndex, uint64(lsn))
	return []byte(fmt.Sprintf("%s%s", keyPrefixLogEntries(ptnID), marshaledIndex))
}

// keyPrefixLogEntries returns the key prefix holding repository's write-ahead log entries.
func keyPrefixLogEntries(ptnID partitionID) []byte {
	return []byte(fmt.Sprintf("partition/%s/log/entry/", ptnID.MarshalBinary()))
}
