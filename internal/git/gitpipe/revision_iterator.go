package gitpipe

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
)

// RevisionIterator is an iterator returned by the Revlist function.
type RevisionIterator interface {
	ObjectIterator
	// Result returns the current item.
	Result() RevisionResult
}

// NewRevisionIterator returns a new RevisionIterator for the given items.
func NewRevisionIterator(ctx context.Context, items []RevisionResult) RevisionIterator {
	itemChan := make(chan RevisionResult, len(items))
	for _, item := range items {
		itemChan <- item
	}
	close(itemChan)

	return &revisionIterator{
		ctx: ctx,
		ch:  itemChan,
	}
}

type revisionIterator struct {
	ctx    context.Context
	ch     <-chan RevisionResult
	result RevisionResult
}

func (it *revisionIterator) Next() bool {
	if it.result.err != nil {
		return false
	}

	// Prioritize context cancellation errors so that we don't try to fetch results anymore when
	// the context is done.
	select {
	case <-it.ctx.Done():
		it.result = RevisionResult{err: it.ctx.Err()}
		return false
	default:
	}

	select {
	case <-it.ctx.Done():
		it.result = RevisionResult{err: it.ctx.Err()}
		return false
	case result, ok := <-it.ch:
		if !ok {
			return false
		}

		it.result = result
		if result.err != nil {
			return false
		}

		return true
	}
}

func (it *revisionIterator) Err() error {
	return it.result.err
}

func (it *revisionIterator) Result() RevisionResult {
	return it.result
}

func (it *revisionIterator) ObjectID() git.ObjectID {
	return it.result.OID
}

func (it *revisionIterator) ObjectName() []byte {
	return it.result.ObjectName
}
