package hook

import (
	"context"
	"fmt"
	"sync"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
)

// ReferenceUpdate denotes a single reference update to be made.
type ReferenceUpdate struct {
	Ref    git.ReferenceName
	OldOID git.ObjectID
	NewOID git.ObjectID
}

// ProcReceiveHandler provides the mechanism for RPCs which invoked
// git-receive-pack(1) to interact with the proc-receive hook. It provides
// access to a list of references that a transaction is attempting to update,
// and functions to accept or reject individual updates.
type ProcReceiveHandler interface {
	// TransactionID provides the storage.TransactionID associated with the
	// handler.
	TransactionID() storage.TransactionID

	// Atomic denotes whether the push was atomic.
	Atomic() bool

	// PushOptions provides the set of push options provided to the proc-receive hook.
	PushOptions() []string

	// ReferenceUpdates provides the reference updates to be made.
	ReferenceUpdates() []ReferenceUpdate

	// AcceptUpdate tells the registry to accept a given reference update.
	AcceptUpdate(referenceName git.ReferenceName) error
	// RejectUpdate tells the registry to reject a given reference update, along
	// with a reason.
	RejectUpdate(referenceName git.ReferenceName, reason string) error

	// Close must be called to clean up the proc-receive hook. If the user
	// of the handler encounters an error, it should be transferred to the
	// hook too.
	Close(rpcErr error) error
}

// ProcReceiveRegistry is the registry which provides the proc-receive handlers
// (https://git-scm.com/docs/githooks#proc-receive) against a provided transaction ID.
//
// The registry allows RPCs which perform commands that execute git-receive-pack(1)
// to hook into the proc-receive handler. The RPC must register itself with the
// registry by calling RegisterWaiter(), this provides a channel where the handler
// will be provided along with a registry cleanup function.
//
// When the handler for the associated transaction ID is added to the registry
// via the Transmit() function, it will be propagated to the channel.
type ProcReceiveRegistry struct {
	waiters map[storage.TransactionID]chan<- ProcReceiveHandler
	m       sync.Mutex
}

// NewProcReceiveRegistry creates a new registry by allocating the required
// variables.
func NewProcReceiveRegistry() *ProcReceiveRegistry {
	return &ProcReceiveRegistry{
		waiters: make(map[storage.TransactionID]chan<- ProcReceiveHandler),
	}
}

// RegisterWaiter registers a waiter against the provided transactionID.
// The function returns a channel to obtain the handler, a cleanup function
// which must be called and an error if any.
func (r *ProcReceiveRegistry) RegisterWaiter(id storage.TransactionID) (<-chan ProcReceiveHandler, func(), error) {
	r.m.Lock()
	defer r.m.Unlock()

	if _, ok := r.waiters[id]; ok {
		return nil, nil, fmt.Errorf("cannot register id: %d again", id)
	}

	ch := make(chan ProcReceiveHandler)
	r.waiters[id] = ch

	cleanup := func() {
		r.m.Lock()
		defer r.m.Unlock()

		delete(r.waiters, id)
	}

	return ch, cleanup, nil
}

// Transmit transmits a handler to its waiter.
func (r *ProcReceiveRegistry) Transmit(ctx context.Context, handler ProcReceiveHandler) error {
	r.m.Lock()
	defer r.m.Unlock()

	ch, ok := r.waiters[handler.TransactionID()]
	if !ok {
		return fmt.Errorf("no waiters for id: %d", handler.TransactionID())
	}

	// It is possible that the RPC (waiter) returned because receive-pack
	// returned an error. In such scenarios, we don't want to block indefinitely.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ch <- handler:
	}

	return nil
}
