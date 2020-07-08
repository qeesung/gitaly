package transactions

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrDuplicateNodes        = errors.New("transactions cannot have duplicate nodes")
	ErrMissingNodes          = errors.New("transaction requires at least one node")
	ErrInvalidThreshold      = errors.New("transaction has invalid threshold")
	ErrTransactionVoteFailed = errors.New("transaction did not reach quorum")
	ErrTransactionCanceled   = errors.New("transaction was canceled")
)

// Voter is a participant in a given transaction that may cast a vote.
type Voter struct {
	// Name of the voter, usually Gitaly's storage name.
	Name string
	// Votes is the number of votes available to this voter in the voting
	// process. `0` means the outcome of the vote will not be influenced by
	// this voter.
	Votes uint

	vote vote
}

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

type transaction struct {
	doneCh   chan interface{}
	cancelCh chan interface{}

	threshold uint

	lock         sync.RWMutex
	votersByNode map[string]*Voter
	voteCounts   map[vote]uint
}

func newTransaction(voters []Voter, threshold uint) (*transaction, error) {
	if len(voters) == 0 {
		return nil, ErrMissingNodes
	}

	var totalVotes uint
	votersByNode := make(map[string]*Voter, len(voters))

	for _, voter := range voters {
		if _, ok := votersByNode[voter.Name]; ok {
			return nil, ErrDuplicateNodes
		}

		voter := voter // rescope loop variable
		votersByNode[voter.Name] = &voter
		totalVotes += voter.Votes
	}

	// If the given threshold is smaller than the total votes, then we
	// cannot ever reach quorum.
	if totalVotes < threshold {
		return nil, ErrInvalidThreshold
	}

	// If the threshold is less or equal than half of all node's votes,
	// it's possible to reach multiple different quorums that settle on
	// different outcomes.
	if threshold*2 <= totalVotes {
		return nil, ErrInvalidThreshold
	}

	return &transaction{
		doneCh:       make(chan interface{}),
		cancelCh:     make(chan interface{}),
		threshold:    threshold,
		votersByNode: votersByNode,
		voteCounts:   make(map[vote]uint, len(votersByNode)),
	}, nil
}

func (t *transaction) cancel() {
	close(t.cancelCh)
}

func (t *transaction) vote(node string, hash []byte) error {
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

func (t *transaction) collectVotes(ctx context.Context, node string) error {
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

	// See if our vote crossed the threshold. As there can be only one vote
	// exceeding it, we know we're the winner in that case.
	if t.voteCounts[voter.vote] < t.threshold {
		return fmt.Errorf("%w: got %d/%d votes", ErrTransactionVoteFailed, t.voteCounts[voter.vote], t.threshold)
	}

	return nil
}
