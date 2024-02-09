package limiter

import (
	"container/list"
	"context"
	"errors"
	"sync"

	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
)

// resizableSemaphore struct models a semaphore with a dynamically adjustable size. It bounds the concurrent access to
// resources, allowing a certain level of concurrency. When the concurrency reaches the semaphore's capacity, the callers
// are blocked until a resource becomes available again. The size of the semaphore can be adjusted atomically at any time.
// When the semaphore gets resized to a size smaller than the current number of resources acquired, the semaphore is
// considered to be full. The "leftover" acquirers can still keep the resource until they release the semaphore. The
// semaphore cannot be acquired until the amount of acquirers fall under the size again.
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
	sync.Mutex
	// current represents the current concurrency access to the resources.
	current uint
	// leftover accounts for the number of extra acquirers when the size shrinks down.
	leftover uint
	// size is the maximum capacity of the semaphore. It represents the maximum number of concurrent accesses allowed
	// to the resource at the current time.
	size uint
	// waiters is a FIFO list of waiters waiting for the resource.
	waiters *list.List
}

// waiter is a wrapper to be put into the waiting queue. When there is an available resource, the front waiter is pulled
// out and ready channel is closed.
type waiter struct {
	ready chan struct{}
}

// NewResizableSemaphore creates a new resizableSemaphore with the specified initial size.
func NewResizableSemaphore(size uint) *resizableSemaphore {
	return &resizableSemaphore{
		size:    size,
		waiters: list.New(),
	}
}

// Acquire allows the caller to acquire the semaphore. If the semaphore is full, the caller is blocked until there
// is an available slot or the context is canceled or the context exceeds the deadline. If the context exceeds the
// deadline, ErrMaxQueueTime is returned. If the context is canceled, context's error is returned. Otherwise,
// this function returns nil after acquired.
func (s *resizableSemaphore) Acquire(ctx context.Context) error {
	s.Lock()
	if s.count() < s.size {
		select {
		case <-ctx.Done():
			s.Unlock()
			return s.contextError(ctx)
		default:
			s.current++
			s.Unlock()
			return nil
		}
	}

	w := &waiter{ready: make(chan struct{})}

	var element *list.Element
	if featureflag.UseResizableSemaphoreLifoStrategy.IsEnabled(ctx) {
		element = s.waiters.PushFront(w)
	} else {
		element = s.waiters.PushBack(w)
	}
	s.Unlock()

	select {
	case <-ctx.Done():
		return s.stopWaiter(element, w, s.contextError(ctx))
	case <-w.ready:
		return nil
	}
}

func (s *resizableSemaphore) stopWaiter(element *list.Element, w *waiter, err error) error {
	s.Lock()
	defer s.Unlock()

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
		// If we're at the front and there are extra slots left, notify next waiters in the
		// queue. If all waiters in the queue have the same context, this action is not
		// necessary because the rest will return an error anyway. Unfortunately, as we accept
		// ctx as an argument of Acquire(), it's possible for waiters to have different
		// contexts. Hence, we need to scan the waiter list, just in case.
		if isFront {
			s.notifyWaiters()
		}
	}
	return err
}

func (s *resizableSemaphore) contextError(ctx context.Context) error {
	err := ctx.Err()
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrMaxQueueTime
	}
	return err
}

// notifyWaiters scans from the head of the s.waiters linked list, removing waiters until there are no free slots. This
// function must only be called after the mutex of s is acquired.
func (s *resizableSemaphore) notifyWaiters() {
	for {
		element := s.waiters.Front()
		if element == nil {
			break
		}

		if s.count() >= s.size {
			return
		}

		w := element.Value.(*waiter)
		s.current++
		s.waiters.Remove(element)
		close(w.ready)
	}
}

// TryAcquire attempts to acquire the semaphore without blocking. On success, it returns nil. On failure, it returns
// ErrMaxQueueSize and leaves the semaphore unchanged.
func (s *resizableSemaphore) TryAcquire() error {
	s.Lock()
	defer s.Unlock()

	// Technically, if the number of waiters is less than the number of available slots, the caller
	// of this function should be put to at the end of the queue. However, the queue always moved
	// up when a slot is available or when a waiter's context is cancelled. Thus, as soon as there
	// are waiters in the queue, there is no chance for this caller to acquire the semaphore
	// without waiting.
	if s.count() < s.size && s.waiters.Len() == 0 {
		s.current++
		return nil
	}
	return ErrMaxQueueSize
}

// Release releases the semaphore.
func (s *resizableSemaphore) Release() {
	s.Lock()
	defer s.Unlock()
	// Deduct leftover first, because we want to release the remaining extra slots that were acquired before
	// the semaphore was shrunk. The semaphore can be acquired again when current < size and leftover = 0.
	// ┌────────── size ────────────────┐
	// ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ⧅ ⧅ ⧅ ⧅ ⧅ ⧅
	// └─────────── current  ───────────┴─ leftover ┘
	if s.leftover > 0 {
		s.leftover--
	} else {
		s.current--
	}
	s.notifyWaiters()
}

// Count returns the number of concurrent accesses allowed by the semaphore.
func (s *resizableSemaphore) Count() int {
	s.Lock()
	defer s.Unlock()
	return int(s.count())
}

func (s *resizableSemaphore) count() uint {
	return s.current + s.leftover
}

// Resize modifies the maximum number of concurrent accesses allowed by the semaphore.
func (s *resizableSemaphore) Resize(newSize uint) {
	s.Lock()
	defer s.Unlock()

	if newSize == s.size {
		return
	}

	s.size = newSize
	currentCount := s.count()
	if newSize > currentCount {
		// Case 1: The semaphore is full. There is no leftover. The current and leftover stays intact.
		// ┌─────────────── New size ────────────────┐
		// ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ □ □ □ □ □ □ □
		// └───────── current  ─────────┘
		// └──────  Previous size  ─────┘
		//
		// Case 2: The semaphore is full. The leftover might exceed the previous size.
		// ┌────────────────── New size ──────────────────────────┐
		// ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ⧅ ⧅ ⧅ ⧅ ⧅ ⧅ □ □ □ □ □
		// └─────────── current  ───────────┴─ leftover ┘
		// └───────  Previous size  ────────┘
		//
		// Case 3: The semaphore is not full. It's not feasible to have leftover because leftover is deducted
		// before current. If the semaphore's size grows after shrinking down, the leftover is properly
		// restructured.
		// ┌─────────────── New size ────────────────┐
		// ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ □ □ □ □ □ □ □
		// └────────── current ─────────┘       │
		// └──────────  Previous size  ─────────┘
		//
		// Case 4: The semaphore is not full but the new size is less than the previous size. The queue stays
		// idle. It's not necessary to notify the waiters,
		// ┌─────────── New size ───────────┐
		// ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ □ □ □ □ □ □ □
		// └────────── current ─────────┘       │
		// └──────────  Previous size  ─────────┘
		//
		// In either case, the new size covers the current and leftover. No need to continue accounting for
		// leftover. We also need to notify the waiters to move up the queues if there are available slots. If
		// there isn't (case 4), no need to notify the waiters, but the function returns immediately.
		s.leftover = 0
		s.current = currentCount
		s.notifyWaiters()
	} else {
		// Case 1: The semaphore is full. There is no leftover.
		// ┌──────── New size ──────────┐
		// ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■
		// └───────────── current  ───────────────┘
		// └───────────  Previous size  ──────────┘
		//
		// Case 2: The semaphore is full. There are some leftovers.
		// ┌──────── New size ──────────┐
		// ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ⧅ ⧅ ⧅ ⧅ ⧅ ⧅
		// └───────────── current  ───────────────┴─ leftover ┘
		// └───────────  Previous size  ──────────┘
		//
		// Case 3: The semaphore is not full. Similar to case 3 above, there shouldn't be any leftover.
		// ┌────── New size ────────┐
		// ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ □ □ □ □
		// └────────── current  ──────────┘       │
		// └──────────  Previous size  ───────────┘
		//
		// Case 4: The new size is equal to the current count. The semaphore is either saturated or not, the
		// leftover is reset.
		// ┌────────── New size ──────────┐
		// ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ ■ □ □ □ □
		// └────────── current  ──────────┘       │
		// └──────────  Previous size  ───────────┘
		//
		// In all of the above cases, the semaphore sets the current to the new size and convert the rest to
		// leftover. There isn't any new slot, hence no need to notify the waiters.
		s.current = newSize
		s.leftover = currentCount - newSize
	}
}
