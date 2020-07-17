package transactions

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrTransactionVoteFailed indicates the transaction didn't reach quorum.
	ErrTransactionVoteFailed = errors.New("transaction did not reach quorum")
	// ErrTransactionCanceled indicates the transaction was canceled before
	// reaching quorum.
	ErrTransactionCanceled = errors.New("transaction was canceled")
)

// voteResult represents the outcome of a transaction for a single voter.
type voteResult int

const (
	// voteUndecided means that the voter either didn't yet show up or that
	// the vote couldn't yet be decided due to there being no majority yet.
	voteUndecided voteResult = iota
	// voteCommitted means that the voter committed his vote.
	voteCommitted
	// voteAborted means that the voter aborted his vote.
	voteAborted
)

type vote [sha1.Size]byte

func voteFromHash(hash []byte) (vote, error) {
	var vote vote

	if len(hash) != sha1.Size {
		return vote, fmt.Errorf("invalid voting hash: %q", hash)
	}

	copy(vote[:], hash)
	return vote, nil
}

func (v vote) isEmpty() bool {
	return v == vote{}
}

// subtransaction is a single session where voters are voting for a certain outcome.
type subtransaction struct {
	doneCh   chan interface{}
	cancelCh chan interface{}

	threshold uint

	lock         sync.RWMutex
	votersByNode map[string]*Voter
	voteCounts   map[vote]uint
}

func newSubtransaction(voters []Voter, threshold uint) (*subtransaction, error) {
	votersByNode := make(map[string]*Voter, len(voters))
	for _, voter := range voters {
		voter := voter // rescope loop variable
		votersByNode[voter.Name] = &voter
	}

	return &subtransaction{
		doneCh:       make(chan interface{}),
		cancelCh:     make(chan interface{}),
		threshold:    threshold,
		votersByNode: votersByNode,
		voteCounts:   make(map[vote]uint, len(voters)),
	}, nil
}

func (t *subtransaction) cancel() map[string]bool {
	t.lock.Lock()
	defer t.lock.Unlock()

	results := make(map[string]bool, len(t.votersByNode))
	for node, voter := range t.votersByNode {
		// If a voter didn't yet show up or is still undecided, we need
		// to mark it as failed so it won't get the idea of committing
		// the transaction at a later point anymore.
		if voter.result == voteUndecided {
			voter.result = voteAborted
		}
		results[node] = voter.result == voteCommitted
	}

	close(t.cancelCh)

	return results
}

func (t *subtransaction) vote(node string, hash []byte) error {
	vote, err := voteFromHash(hash)
	if err != nil {
		return err
	}

	t.lock.Lock()
	defer t.lock.Unlock()

	// Cast our vote. In case the node doesn't exist or has already cast a
	// vote, we need to abort.
	voter, ok := t.votersByNode[node]
	if !ok {
		return fmt.Errorf("invalid node for transaction: %q", node)
	}
	if !voter.vote.isEmpty() {
		return fmt.Errorf("node already cast a vote: %q", node)
	}
	voter.vote = vote

	oldCount := t.voteCounts[vote]
	newCount := oldCount + voter.Votes
	t.voteCounts[vote] = newCount

	// If the threshold was reached before already, we mustn't try to
	// signal the other voters again.
	if oldCount >= t.threshold {
		return nil
	}

	// If we've just crossed the threshold, signal all voters that the
	// voting has concluded.
	if newCount >= t.threshold {
		close(t.doneCh)
		return nil
	}

	// If any other vote has already reached the threshold, we mustn't try
	// to notify voters again.
	for _, count := range t.voteCounts {
		if count >= t.threshold {
			return nil
		}
	}

	// If any of the voters didn't yet cast its vote, we need to wait for
	// them.
	for _, voter := range t.votersByNode {
		if voter.vote.isEmpty() {
			return nil
		}
	}

	// Otherwise, signal voters that all votes were gathered.
	close(t.doneCh)
	return nil
}

func (t *subtransaction) collectVotes(ctx context.Context, node string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.cancelCh:
		return ErrTransactionCanceled
	case <-t.doneCh:
		break
	}

	t.lock.RLock()
	defer t.lock.RUnlock()

	voter, ok := t.votersByNode[node]
	if !ok {
		return fmt.Errorf("invalid node for transaction: %q", node)
	}

	if voter.result != voteUndecided {
		return fmt.Errorf("voter has already settled on an outcome: %q", node)
	}

	// See if our vote crossed the threshold. As there can be only one vote
	// exceeding it, we know we're the winner in that case.
	if t.voteCounts[voter.vote] < t.threshold {
		voter.result = voteAborted
		return fmt.Errorf("%w: got %d/%d votes", ErrTransactionVoteFailed, t.voteCounts[voter.vote], t.threshold)
	}

	voter.result = voteCommitted
	return nil
}

func (t *subtransaction) getResult(node string) (voteResult, error) {
	t.lock.RLock()
	defer t.lock.RUnlock()

	voter, ok := t.votersByNode[node]
	if !ok {
		return voteAborted, fmt.Errorf("invalid node for transaction: %q", node)
	}

	return voter.result, nil
}
