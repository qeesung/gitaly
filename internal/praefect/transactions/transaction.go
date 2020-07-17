package transactions

import (
	"context"
	"errors"
	"sync"
)

var (
	// ErrDuplicateNodes indicates a transaction was registered with two
	// voters having the same name.
	ErrDuplicateNodes = errors.New("transactions cannot have duplicate nodes")
	// ErrMissingNodes indicates a transaction was registered with no voters.
	ErrMissingNodes = errors.New("transaction requires at least one node")
	// ErrInvalidThreshold indicates a transaction was registered with an
	// invalid threshold that may either allow for multiple different
	// quorums or none at all.
	ErrInvalidThreshold = errors.New("transaction has invalid threshold")
	// ErrSubtransactionFailed indicates a vote was cast on a
	// subtransaction which failed already.
	ErrSubtransactionFailed = errors.New("subtransaction has failed")
)

// Voter is a participant in a given transaction that may cast a vote.
type Voter struct {
	// Name of the voter, usually Gitaly's storage name.
	Name string
	// Votes is the number of votes available to this voter in the voting
	// process. `0` means the outcome of the vote will not be influenced by
	// this voter.
	Votes uint

	vote   vote
	result voteResult
}

// transaction is a session where a set of voters votes on one or more
// subtransactions. Subtransactions are a sequence of sessions, where each node
// needs to go through the same sequence and agree on the same thing in the end
// in order to have the complete transaction succeed.
type transaction struct {
	threshold uint
	voters    []Voter

	lock            sync.Mutex
	subtransactions []*subtransaction
}

func newTransaction(voters []Voter, threshold uint) (*transaction, error) {
	if len(voters) == 0 {
		return nil, ErrMissingNodes
	}

	var totalVotes uint
	votersByNode := make(map[string]interface{}, len(voters))
	for _, voter := range voters {
		if _, ok := votersByNode[voter.Name]; ok {
			return nil, ErrDuplicateNodes
		}
		votersByNode[voter.Name] = nil
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
		threshold: threshold,
		voters:    voters,
	}, nil
}

func (t *transaction) cancel() map[string]bool {
	t.lock.Lock()
	defer t.lock.Unlock()

	results := make(map[string]bool, len(t.voters))

	// We need to collect outcomes of all subtransactions. If any of the
	// subtransactions failed, then the overall transaction failed for that
	// node as well. Otherwise, if all subtransactions for the node
	// succeeded, the transaction did as well.
	for _, subtransaction := range t.subtransactions {
		for voter, result := range subtransaction.cancel() {
			// If there already is an entry indicating failure, keep it.
			if didSucceed, ok := results[voter]; ok && !didSucceed {
				continue
			}
			results[voter] = result
		}
	}

	return results
}

func (t *transaction) countSubtransactions() int {
	return len(t.subtransactions)
}

// getOrCreateSubtransaction gets an ongoing subtransaction on which the given
// node hasn't yet voted on or creates a new one if the node has succeeded on
// all subtransactions. In case the node has failed on any of the
// subtransactions, an error will be returned.
func (t *transaction) getOrCreateSubtransaction(node string) (*subtransaction, error) {
	t.lock.Lock()
	defer t.lock.Unlock()

	for _, subtransaction := range t.subtransactions {
		result, err := subtransaction.getResult(node)
		if err != nil {
			return nil, err
		}

		switch result {
		case voteUndecided:
			// An undecided vote means we should vote on this one.
			return subtransaction, nil
		case voteCommitted:
			// If we have committed this subtransaction, we're good
			// to go.
			continue
		case voteAborted:
			// If the subtransaction was aborted, then we need to
			// fail as we cannot proceed if the path leading to the
			// end result has intermittent failures.
			return nil, ErrSubtransactionFailed
		}
	}

	// If we arrive here, then we know that all the node has voted and
	// reached quorum on all subtransactions. We can thus create a new one.
	subtransaction, err := newSubtransaction(t.voters, t.threshold)
	if err != nil {
		return nil, err
	}

	t.subtransactions = append(t.subtransactions, subtransaction)

	return subtransaction, nil
}

func (t *transaction) vote(ctx context.Context, node string, hash []byte) error {
	subtransaction, err := t.getOrCreateSubtransaction(node)
	if err != nil {
		return err
	}

	if err := subtransaction.vote(node, hash); err != nil {
		return err
	}

	return subtransaction.collectVotes(ctx, node)
}
