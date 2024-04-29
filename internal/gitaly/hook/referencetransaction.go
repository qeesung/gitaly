package hook

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
)

//nolint:revive // This is unintentionally missing documentation.
func (m *GitLabHookManager) ReferenceTransactionHook(ctx context.Context, state ReferenceTransactionState, env []string, stdin io.Reader) error {
	payload, err := git.HooksPayloadFromEnv(env)
	if err != nil {
		return fmt.Errorf("extracting hooks payload: %w", err)
	}

	objectHash, err := git.ObjectHashByFormat(payload.ObjectFormat)
	if err != nil {
		return fmt.Errorf("looking up object hash: %w", err)
	}

	changes, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("reading stdin from request: %w", err)
	}

	var tx Transaction
	if payload.TransactionID > 0 {
		tx, err = m.txRegistry.Get(payload.TransactionID)
		if err != nil {
			return fmt.Errorf("get transaction: %w", err)
		}
	}

	var phase voting.Phase
	switch state {
	// We're voting in prepared state as this is the only stage in Git's reference transaction
	// which allows us to abort the transaction.
	case ReferenceTransactionPrepared:
		phase = voting.Prepared

		if tx != nil {
			updates, _, err := parseChanges(ctx, objectHash, bytes.NewReader(changes))
			if err != nil {
				return fmt.Errorf("parse changes: %w", err)
			}

			initialValues := map[git.ReferenceName]git.ObjectID{}
			for reference, update := range updates {
				initialValues[reference] = update.OldOID
			}

			// Only record the initial values of the reference in the prepare step as this
			// change hasn't yet been committed.
			if err := tx.RecordInitialReferenceValues(ctx, initialValues); err != nil {
				return fmt.Errorf("record initial reference value: %w", err)
			}
		}
	// We're also voting in committed state to tell Praefect we've actually persisted the
	// changes. This is necessary as some RPCs fail return errors in the response body rather
	// than as an error code. Praefect can't tell if these RPCs have failed. Voting on committed
	// ensure Praefect sees either a missing vote or that the RPC did commit the changes.
	case ReferenceTransactionCommitted:
		phase = voting.Committed

		if tx != nil {
			updates, defaultBranchUpdated, err := parseChanges(ctx, objectHash, bytes.NewReader(changes))
			if err != nil {
				return fmt.Errorf("parse changes: %w", err)
			}

			tx.UpdateReferences(updates)

			if defaultBranchUpdated {
				tx.MarkDefaultBranchUpdated()
			}
		}
	default:
		return nil
	}

	// When deleting references, git has to delete them both in the packed-refs backend as well
	// as any loose refs -- if only the loose ref was deleted, it would potentially unshadow the
	// value contained in the packed-refs file and vice versa. As a result, git will create two
	// transactions when any ref exists in both backends: one session to force-delete all
	// existing refs in the packed-refs backend, and then one transaction to update all loose
	// refs. This is problematic for us, as our voting logic now requires all nodes to have the
	// same packed state, which we do not and cannot guarantee.
	//
	// We're lucky though and can fix this quite easily: git only needs to cope with unshadowing
	// refs when deleting loose refs, so it will only ever _delete_ refs from the packed-refs
	// backend and never _update_ any refs. And if such a force-deletion happens, the same
	// deletion will also get queued to the loose backend no matter whether the loose ref exists
	// or not given that it must be locked during the whole transaction. As such, we can easily
	// recognize those packed-refs cleanups: all queued ref updates are force deletions.
	//
	// The workaround is thus clear: we simply do not cast a vote on any reference transaction
	// which consists only of force-deletions -- the vote will instead only happen on the loose
	// backend transaction, which contains the full record of all refs which are to be updated.
	if isForceDeletionsOnly(objectHash, bytes.NewReader(changes)) {
		return nil
	}

	hash := sha1.Sum(changes)

	if err := m.voteOnTransaction(ctx, hash, phase, payload); err != nil {
		return fmt.Errorf("error voting on transaction: %w", err)
	}

	return nil
}

// parseChanges parses the changes from the reader. All updates to references lacking a 'refs/' prefix are ignored. These
// are the various pseudo reference like ORIG_HEAD but also HEAD. See the documentation of the reference-transaction hook
// for details on the format: https://git-scm.com/docs/githooks#_reference_transaction
func parseChanges(ctx context.Context, objectHash git.ObjectHash, changes io.Reader) (storagemgr.ReferenceUpdates, bool, error) {
	scanner := bufio.NewScanner(changes)
	defaultBranchUpdated := false

	updates := storagemgr.ReferenceUpdates{}
	for scanner.Scan() {
		line := scanner.Text()
		components := strings.Split(line, " ")
		if len(components) != 3 {
			return nil, defaultBranchUpdated, fmt.Errorf("unexpected change line: %q", line)
		}

		reference := git.ReferenceName(components[2])
		if !strings.HasPrefix(reference.String(), "refs/") {
			// We want to track default branch updates, so that the
			// transaction manager can be notified about it.
			defaultBranchUpdated = reference.String() == "HEAD"
			continue
		}

		update := storagemgr.ReferenceUpdate{}

		var err error
		update.OldOID, err = objectHash.FromHex(components[0])
		if err != nil {
			return nil, defaultBranchUpdated, fmt.Errorf("parse old: %w", err)
		}

		update.NewOID, err = objectHash.FromHex(components[1])
		if err != nil {
			return nil, defaultBranchUpdated, fmt.Errorf("parse new: %w", err)
		}

		updates[reference] = update
	}

	return updates, defaultBranchUpdated, nil
}

// isForceDeletionsOnly determines whether the given changes only consist of force-deletions.
func isForceDeletionsOnly(objectHash git.ObjectHash, changes io.Reader) bool {
	// forceDeletionPrefix is the prefix of a queued reference transaction which deletes a
	// reference without checking its current value.
	forceDeletionPrefix := fmt.Sprintf("%[1]s %[1]s ", objectHash.ZeroOID)

	scanner := bufio.NewScanner(changes)

	for scanner.Scan() {
		line := scanner.Bytes()

		if bytes.HasPrefix(line, []byte(forceDeletionPrefix)) {
			continue
		}

		return false
	}

	return true
}
