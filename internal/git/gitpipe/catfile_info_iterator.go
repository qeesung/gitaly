package gitpipe

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
)

// CatfileInfoIterator is an iterator returned by the Revlist function.
type CatfileInfoIterator interface {
	ObjectIterator
	// Result returns the current item.
	Result() CatfileInfoResult
}

// NewCatfileInfoIterator returns a new CatfileInfoIterator for the given items.
func NewCatfileInfoIterator(ctx context.Context, items []CatfileInfoResult) CatfileInfoIterator {
	itemChan := make(chan CatfileInfoResult, len(items))
	for _, item := range items {
		itemChan <- item
	}
	close(itemChan)

	return &catfileInfoIterator{
		ctx: ctx,
		ch:  itemChan,
	}
}

type catfileInfoIterator struct {
	ctx    context.Context
	ch     <-chan CatfileInfoResult
	result CatfileInfoResult
}

func (it *catfileInfoIterator) Next() bool {
	if it.result.err != nil {
		return false
	}

	// Prioritize context cancellation errors so that we don't try to fetch results anymore when
	// the context is done.
	select {
	case <-it.ctx.Done():
		it.result = CatfileInfoResult{err: it.ctx.Err()}
		return false
	default:
	}

	select {
	case <-it.ctx.Done():
		it.result = CatfileInfoResult{err: it.ctx.Err()}
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

func (it *catfileInfoIterator) Err() error {
	return it.result.err
}

func (it *catfileInfoIterator) Result() CatfileInfoResult {
	return it.result
}

func (it *catfileInfoIterator) ObjectID() git.ObjectID {
	return it.result.ObjectID()
}

func (it *catfileInfoIterator) ObjectName() []byte {
	return it.result.ObjectName
}
