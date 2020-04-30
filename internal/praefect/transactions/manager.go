package transactions

import (
	"crypto/sha1"
	"fmt"
	"math/rand"
	"sync"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
)

// Manager handles reference transactions for Praefect. It is required in order
// for Praefect to handle transactions directly instead of having to reach out
// to reference transaction RPCs.
type Manager struct {
	lock         sync.Mutex
	transactions map[uint64]string
}

// NewManager creates a new transactions Manager.
func NewManager() *Manager {
	return &Manager{
		transactions: make(map[uint64]string),
	}
}

// CancelFunc is the transaction cancellation function returned by
// `RegisterTransaction`. Calling it will cause the transaction to be removed
// from the transaction manager.
type CancelFunc func()

// RegisterTransaction registers a new reference transaction for a set of nodes
// taking part in the transaction.
func (mgr *Manager) RegisterTransaction(nodes []string) (uint64, CancelFunc, error) {
	mgr.lock.Lock()
	defer mgr.lock.Unlock()

	// We only accept a single node in transactions right now, which is
	// usually the primary. This limitation will be lifted at a later point
	// to allow for real transaction voting and multi-phase commits.
	if len(nodes) != 1 {
		return 0, nil, helper.ErrInvalidArgumentf("transaction requires exactly one node")
	}

	// Use a random transaction ID. Using monotonic incrementing counters
	// that reset on restart of Praefect would be suboptimal, as the chance
	// for collisions is a lot higher in case Praefect restarts when Gitaly
	// nodes still have in-flight transactions.
	transactionID := rand.Uint64()
	if _, ok := mgr.transactions[transactionID]; ok {
		return 0, nil, helper.ErrInternalf("transaction exists already")
	}
	mgr.transactions[transactionID] = nodes[0]

	return transactionID, func() {
		mgr.cancelTransaction(transactionID)
	}, nil
}

func (mgr *Manager) cancelTransaction(transactionID uint64) {
	mgr.lock.Lock()
	defer mgr.lock.Unlock()
	delete(mgr.transactions, transactionID)
}

// StartTransaction is called by a client who's starting a reference
// transaction. As we currently only have primary nodes which perform reference
// transactions, this function doesn't yet do anything of interest but will
// always instruct the node to commit, if given valid transaction parameters.
// In future, it will wait for all clients of a given transaction to start the
// transaction and perform a vote.
func (mgr *Manager) StartTransaction(transactionID uint64, node string, hash []byte) error {
	mgr.lock.Lock()
	defer mgr.lock.Unlock()

	// While the reference updates hash is not used yet, we already verify
	// it's there. At a later point, the hash will be used to verify that
	// all voting nodes agree on the same updates.
	if len(hash) != sha1.Size {
		return helper.ErrInvalidArgumentf("invalid reference hash: %q", hash)
	}

	transaction, ok := mgr.transactions[transactionID]
	if !ok {
		return helper.ErrNotFound(fmt.Errorf("no such transaction: %d", transactionID))
	}

	if transaction != node {
		return helper.ErrInternalf("invalid node for transaction: %q", node)
	}

	return nil
}
