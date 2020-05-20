package transactions

import (
	"bytes"
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"sync"
)

var (
	ErrDuplicateNodes        = errors.New("transactions cannot have duplicate nodes")
	ErrMissingNodes          = errors.New("transaction requires at least one node")
	ErrTransactionVoteFailed = errors.New("transaction vote failed")
	ErrTransactionCanceled   = errors.New("transaction was canceled")
)

type transaction struct {
	doneCh   chan interface{}
	cancelCh chan interface{}

	lock  sync.Mutex
	votes map[string][]byte
}

func newTransaction(nodes []string) (*transaction, error) {
	if len(nodes) == 0 {
		return nil, ErrMissingNodes
	}

	votes := make(map[string][]byte, len(nodes))
	for _, node := range nodes {
		if _, ok := votes[node]; ok {
			return nil, ErrDuplicateNodes
		}
		votes[node] = nil
	}

	return &transaction{
		doneCh:   make(chan interface{}),
		cancelCh: make(chan interface{}),
		votes:    votes,
	}, nil
}

func (t *transaction) cancel() {
	close(t.cancelCh)
}

func (t *transaction) vote(node string, hash []byte) error {
	if len(hash) != sha1.Size {
		return fmt.Errorf("invalid reference hash: %q", hash)
	}

	t.lock.Lock()
	defer t.lock.Unlock()

	// Cast our vote. In case the node doesn't exist or has already cast a
	// vote, we need to abort.
	vote, ok := t.votes[node]
	if !ok {
		return fmt.Errorf("invalid node for transaction: %q", node)
	}
	if vote != nil {
		return fmt.Errorf("node already cast a note: %q", node)
	}
	t.votes[node] = hash

	// Count votes to see if we're done. If there are no more votes, then
	// we must notify other voters (and ourselves) by closing the `done`
	// channel.
	for _, vote := range t.votes {
		if vote == nil {
			return nil
		}
	}

	// As only the last voter may see that all participants have cast their
	// vote, this can really only be called by a single goroutine.
	close(t.doneCh)

	return nil
}

func (t *transaction) collectVotes(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.cancelCh:
		return ErrTransactionCanceled
	case <-t.doneCh:
		break
	}

	// Count votes to see whether we reached agreement or not. There should
	// be no need to lock as nobody will modify the votes anymore.
	var firstVote []byte
	for _, vote := range t.votes {
		if firstVote == nil {
			firstVote = vote
		} else if !bytes.Equal(firstVote, vote) {
			return ErrTransactionVoteFailed
		}
	}

	return nil
}
