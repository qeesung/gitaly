package transactions

import (
	"context"
	cryptorand "crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/prometheus/metrics"
)

// Manager handles reference transactions for Praefect. It is required in order
// for Praefect to handle transactions directly instead of having to reach out
// to reference transaction RPCs.
type Manager struct {
	txIdGenerator TransactionIdGenerator
	lock          sync.Mutex
	transactions  map[uint64]string
	counterMetric *prometheus.CounterVec
	delayMetric   metrics.HistogramVec
}

// TransactionIdGenerator is an interface for types that can generate transaction IDs.
type TransactionIdGenerator interface {
	// Id generates a new transaction identifier
	Id() uint64
}

type transactionIdGenerator struct {
	rand *rand.Rand
}

func newTransactionIdGenerator() *transactionIdGenerator {
	var seed [8]byte

	// Ignore any errors. In case we weren't able to generate a seed, the
	// best we can do is to just use the all-zero seed.
	cryptorand.Read(seed[:])
	source := rand.NewSource(int64(binary.LittleEndian.Uint64(seed[:])))

	return &transactionIdGenerator{
		rand: rand.New(source),
	}
}

func (t *transactionIdGenerator) Id() uint64 {
	return rand.Uint64()
}

// ManagerOpt is a self referential option for Manager
type ManagerOpt func(*Manager)

// WithCounterMetric is an option to set the counter Prometheus metric
func WithCounterMetric(counterMetric *prometheus.CounterVec) ManagerOpt {
	return func(mgr *Manager) {
		mgr.counterMetric = counterMetric
	}
}

// WithDelayMetric is an option to set the delay Prometheus metric
func WithDelayMetric(delayMetric metrics.HistogramVec) ManagerOpt {
	return func(mgr *Manager) {
		mgr.delayMetric = delayMetric
	}
}

// WithTransactionIdGenerator is an option to set the transaction ID generator
func WithTransactionIdGenerator(generator TransactionIdGenerator) ManagerOpt {
	return func(mgr *Manager) {
		mgr.txIdGenerator = generator
	}
}

// NewManager creates a new transactions Manager.
func NewManager(opts ...ManagerOpt) *Manager {
	mgr := &Manager{
		txIdGenerator: newTransactionIdGenerator(),
		transactions:  make(map[uint64]string),
		counterMetric: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"action"}),
		delayMetric:   prometheus.NewHistogramVec(prometheus.HistogramOpts{}, []string{"action"}),
	}

	for _, opt := range opts {
		opt(mgr)
	}

	return mgr
}

func (mgr *Manager) log(ctx context.Context) logrus.FieldLogger {
	return ctxlogrus.Extract(ctx).WithField("component", "transactions.Manager")
}

// CancelFunc is the transaction cancellation function returned by
// `RegisterTransaction`. Calling it will cause the transaction to be removed
// from the transaction manager.
type CancelFunc func()

// RegisterTransaction registers a new reference transaction for a set of nodes
// taking part in the transaction.
func (mgr *Manager) RegisterTransaction(ctx context.Context, nodes []string) (uint64, CancelFunc, error) {
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
	transactionID := mgr.txIdGenerator.Id()
	if _, ok := mgr.transactions[transactionID]; ok {
		return 0, nil, helper.ErrInternalf("transaction exists already")
	}
	mgr.transactions[transactionID] = nodes[0]

	mgr.log(ctx).WithFields(logrus.Fields{
		"transaction_id": transactionID,
		"nodes":          nodes,
	}).Debug("RegisterTransaction")

	mgr.counterMetric.WithLabelValues("registered").Inc()

	return transactionID, func() {
		mgr.cancelTransaction(transactionID)
	}, nil
}

func (mgr *Manager) cancelTransaction(transactionID uint64) {
	mgr.lock.Lock()
	defer mgr.lock.Unlock()
	delete(mgr.transactions, transactionID)
}

func (mgr *Manager) verifyTransaction(transactionID uint64, node string, hash []byte) error {
	// While the reference updates hash is not used yet, we already verify
	// it's there. At a later point, the hash will be used to verify that
	// all voting nodes agree on the same updates.
	if len(hash) != sha1.Size {
		return helper.ErrInvalidArgumentf("invalid reference hash: %q", hash)
	}

	mgr.lock.Lock()
	transaction, ok := mgr.transactions[transactionID]
	mgr.lock.Unlock()

	if !ok {
		return helper.ErrNotFound(fmt.Errorf("no such transaction: %d", transactionID))
	}

	if transaction != node {
		return helper.ErrInternalf("invalid node for transaction: %q", node)
	}

	return nil
}

// StartTransaction is called by a client who's starting a reference
// transaction. As we currently only have primary nodes which perform reference
// transactions, this function doesn't yet do anything of interest but will
// always instruct the node to commit, if given valid transaction parameters.
// In future, it will wait for all clients of a given transaction to start the
// transaction and perform a vote.
func (mgr *Manager) StartTransaction(ctx context.Context, transactionID uint64, node string, hash []byte) error {
	start := time.Now()
	defer func() {
		delay := time.Since(start)
		mgr.delayMetric.WithLabelValues("vote").Observe(delay.Seconds())
	}()

	mgr.counterMetric.WithLabelValues("started").Inc()

	mgr.log(ctx).WithFields(logrus.Fields{
		"transaction_id": transactionID,
		"node":           node,
		"hash":           hex.EncodeToString(hash),
	}).Debug("StartTransaction")

	if err := mgr.verifyTransaction(transactionID, node, hash); err != nil {
		mgr.log(ctx).WithFields(logrus.Fields{
			"transaction_id": transactionID,
			"node":           node,
			"hash":           hex.EncodeToString(hash),
		}).WithError(err).Error("StartTransaction: transaction invalid")
		mgr.counterMetric.WithLabelValues("invalid").Inc()
		return err
	}

	mgr.log(ctx).WithFields(logrus.Fields{
		"transaction_id": transactionID,
		"node":           node,
		"hash":           hex.EncodeToString(hash),
	}).Debug("StartTransaction: transaction committed")

	mgr.counterMetric.WithLabelValues("committed").Inc()

	return nil
}
