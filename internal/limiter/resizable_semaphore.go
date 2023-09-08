package limiter

import (
	"container/list"
	"context"
	"sync"

	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
)

// resizableSemaphore struct models a semaphore with a dynamically adjustable size. It bounds the concurrent access to
// resources, allowing a certain level of concurrency. When the concurrency reaches the semaphore's capacity, the callers
// are blocked until a resource becomes available again. The size of the semaphore can be adjusted atomically at any time.
//
// Internally, it uses a doubly-linked list to manage waiters when the semaphore is full. Callers acquire the semaphore
// by invoking `Acquire()`, and release them by calling `Release()`. This struct ensures that the available slots are
// properly managed, and also handles the semaphore's current count and size. It processes resize requests and manages
// try requests and responses, ensuring smooth operation.
//
// This implementation is heavily inspired by "golang.org/x/sync/semaphore" package's implementation.
//
// Note: This struct is not intended to serve as a general-purpose data structure but is specifically designed for
// flexible concurrency control with resizable capacity.
type resizableSemaphore struct {
	sync.RWMutex
	// current represents the current concurrency access to the resources.
	current int64
	// size is the maximum capacity of the semaphore. It represents the maximum concurrency that the resources can
	// be accessed of the time.
	size int64
	// waiters is the list of waiters waiting for the resource in FIFO order.
	waiters *list.List
}

// waiter is a wrapper to be put into the waiting queue. When there is an available resource, the front waiter is pulled
// out and ready channel is closed.
type waiter struct {
	ready chan struct{}
}

// NewResizableSemaphore creates a new resizableSemaphore with the specified initial size.
func NewResizableSemaphore(size int64) *resizableSemaphore {
	return &resizableSemaphore{
		size:    size,
		waiters: list.New(),
	}
}

// Acquire allows the caller to acquire the semaphore. If the semaphore is full, the caller is blocked until there
// is an available slot or the context is canceled or the ticker ticks. If the context is canceled, context's error is
// returned. If the ticker ticks, ErrSemaphoreMaxWaitTime is returned. Otherwise, this function returns nil after
// acquired.
func (s *resizableSemaphore) Acquire(ctx context.Context, ticker helper.Ticker) error {
	ticker.Reset()
	defer ticker.Stop()

	s.Lock()
	if s.current < s.size {
		select {
		case <-ctx.Done():
			s.Unlock()
			return ctx.Err()
		case <-ticker.C():
			s.Unlock()
			return ErrMaxQueueSize
		default:
			s.current++
			s.Unlock()
			return nil
		}
	}

	w := &waiter{ready: make(chan struct{})}
	element := s.waiters.PushBack(w)
	s.Unlock()

	select {
	case <-ctx.Done():
		return s.stopWaiter(element, w, ctx.Err())
	case <-ticker.C():
		return s.stopWaiter(element, w, ErrMaxQueueTime)
	case <-w.ready:
		return nil
	}
}

func (s *resizableSemaphore) stopWaiter(element *list.Element, w *waiter, err error) error {
	s.Lock()
	select {
	case <-w.ready:
		// If the waiter is ready at the same time as the context cancellation, act as if this
		// waiter is not aware of the cancellation. At this point, the linked list item is
		// properly removed from the queue and the waiter is considered to acquire the
		// semaphore. Otherwise, there might be a race that makes Acquire() returns an error
		// even after the acquisition.
		err = nil
	default:
		isFront := s.waiters.Front() == element
		s.waiters.Remove(element)
		// If we're at the front and there're extra tokens left, notify next waiters in the
		// queue. If all waiters in the queue have the same context, this action is not
		// necessary because the rest will return an error anyway. Unfortunately, as we accept
		// ctx as an argument of Acquire(), it's possible for waiters to have different
		// contexts. Hence, we need to scan the waiter list, just in case.
		if isFront && s.current < s.size {
			s.notifyWaiters()
		}
	}
	s.Unlock()
	return err
}

// notifyWaiters scans the head of the linked list and removes waiters one by one if there is a slot. This function
// must only be called after the mutex of the semaphore is acquired.
func (s *resizableSemaphore) notifyWaiters() {
	for {
		element := s.waiters.Front()
		if element == nil {
			break
		}

		if s.current >= s.size {
			return
		}

		w := element.Value.(*waiter)
		s.current++
		s.waiters.Remove(element)
		close(w.ready)
	}
}

// TryAcquire attempts to acquire the semaphore without blocking. On success, it returns nil. On failure, it returns
// ErrSemaphoreFull and leaves the semaphore unchanged.
func (s *resizableSemaphore) TryAcquire() error {
	s.Lock()
	defer s.Unlock()

	// Technically, if the number of waiters is less than the number of available slots, the caller
	// of this function should be put to at the end of the queue. However, the queue always moved
	// up when a token is released or when a waiter's context is cancelled. Thus, as soon as there
	// are waiters in the queue, there is no chance for this caller to acquire the semaphore
	// without waiting.
	if s.current < s.size && s.waiters.Len() == 0 {
		s.current++
		return nil
	}
	return ErrMaxQueueSize
}

// Release releases the semaphore.
func (s *resizableSemaphore) Release() {
	s.Lock()
	s.current--
	// If the semaphore is resize to smaller than the number of acquirers, this number could go negative when all
	// the prior acquirers release the token.
	if s.current < 0 {
		s.current = 0
	}
	s.notifyWaiters()
	s.Unlock()
}

// Current returns the amount of current concurrent access to the semaphore.
func (s *resizableSemaphore) Current() int64 {
	s.RLock()
	defer s.RUnlock()
	return s.current
}

// Resize modifies the size of the semaphore.
func (s *resizableSemaphore) Resize(newSize int64) {
	s.Lock()
	s.size = newSize
	// When the size of the semaphore shrinks down, nothing to do:
	// - If the semaphore is fully occupied, all waiters continue to wait.
	// - If the new size is more than the number of acquirers, ...
	if newSize > s.current {
		s.notifyWaiters()
	}
	s.Unlock()
}
