package gitpipe

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
)

// CatfileObjectIterator is an iterator returned by the Revlist function.
type CatfileObjectIterator interface {
	ObjectIterator
	// Result returns the current item.
	Result() CatfileObjectResult
}

// NewCatfileObjectIterator returns a new CatfileObjectIterator for the given items.
func NewCatfileObjectIterator(ctx context.Context, items []CatfileObjectResult) CatfileObjectIterator {
	itemChan := make(chan CatfileObjectResult, len(items))
	for _, item := range items {
		itemChan <- item
	}
	close(itemChan)

	return &catfileObjectIterator{
		ctx: ctx,
		ch:  itemChan,
	}
}

type catfileObjectIterator struct {
	ctx    context.Context
	ch     <-chan CatfileObjectResult
	result CatfileObjectResult
}

func (it *catfileObjectIterator) Next() bool {
	if it.result.err != nil {
		return false
	}

	// Prioritize context cancellation errors so that we don't try to fetch results anymore when
	// the context is done.
	select {
	case <-it.ctx.Done():
		it.result = CatfileObjectResult{err: it.ctx.Err()}
		return false
	default:
	}

	select {
	case <-it.ctx.Done():
		it.result = CatfileObjectResult{err: it.ctx.Err()}
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

func (it *catfileObjectIterator) Err() error {
	return it.result.err
}

func (it *catfileObjectIterator) Result() CatfileObjectResult {
	return it.result
}

func (it *catfileObjectIterator) ObjectID() git.ObjectID {
	return it.result.ObjectID()
}

func (it *catfileObjectIterator) ObjectName() []byte {
	return it.result.ObjectName
}
