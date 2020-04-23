package transaction

import (
	"context"
	"crypto/sha1"
	"fmt"
	"math/rand"
	"sync"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type Server struct {
	gitalypb.UnimplementedRefTransactionServer

	lock         sync.Mutex
	transactions map[uint64]string
}

func NewServer() gitalypb.RefTransactionServer {
	return &Server{
		transactions: make(map[uint64]string),
	}
}

// RegisterTransaction registers a new reference transaction for a set of nodes
// taking part in the transaction.
func (s *Server) RegisterTransaction(ctx context.Context, in *gitalypb.RegisterTransactionRequest) (*gitalypb.RegisterTransactionResponse, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	transactionID := rand.Uint64()
	if _, ok := s.transactions[transactionID]; ok {
		return nil, helper.ErrInternalf("transaction exists already")
	}

	// We only accept a single node in transactions right now, which is
	// usually the primary. This limitation will be lifted at a later point
	// to allow for real transaction voting and multi-phase commits.
	if len(in.Nodes) != 1 {
		return nil, helper.ErrInvalidArgumentf("transaction requires exactly one node")
	}
	s.transactions[transactionID] = in.Nodes[0]

	return &gitalypb.RegisterTransactionResponse{
		TransactionId: transactionID,
	}, nil
}

// StartTransaction is called by a client who's starting a reference
// transaction. As we currently only have primary nodes which perform reference
// transactions, this RPC doesn't yet do anything of interest but will
// immediately return "COMMIT". In future, the RPC will wait for all clients of
// a given transaction to start the transaction and perform a vote.
func (s *Server) StartTransaction(ctx context.Context, in *gitalypb.StartTransactionRequest) (*gitalypb.StartTransactionResponse, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	// While the reference updates hash is not used yet, we already verify
	// it's there. At a later point, the hash will be used to verify that
	// all voting nodes agree on the same updates.
	if len(in.ReferenceUpdatesHash) != sha1.Size {
		return nil, helper.ErrInvalidArgumentf("invalid reference hash: %q", in.ReferenceUpdatesHash)
	}

	transaction, ok := s.transactions[in.TransactionId]
	if !ok {
		return nil, helper.ErrNotFound(fmt.Errorf("no such transaction: %d", in.TransactionId))
	}

	if transaction != in.Node {
		return nil, helper.ErrInternalf("invalid node for transaction: %q", in.Node)
	}

	// Currently, only the primary node will perform transactions. We can
	// thus just delete the transaction as soon as it starts the
	// transaction and instruct it to commit.
	delete(s.transactions, in.TransactionId)

	return &gitalypb.StartTransactionResponse{
		State: gitalypb.StartTransactionResponse_COMMIT,
	}, nil
}

// CancelTransaction cancels a registered transaction, removing all data
// associated with it.
func (s *Server) CancelTransaction(ctx context.Context, in *gitalypb.CancelTransactionRequest) (*gitalypb.CancelTransactionResponse, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	_, ok := s.transactions[in.TransactionId]
	if !ok {
		return nil, helper.ErrNotFound(fmt.Errorf("no such transaction: %d", in.TransactionId))
	}
	delete(s.transactions, in.TransactionId)

	return &gitalypb.CancelTransactionResponse{}, nil
}
