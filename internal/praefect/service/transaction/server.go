package transaction

import (
	"context"

	"gitlab.com/gitlab-org/gitaly/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

type Server struct {
	gitalypb.UnimplementedRefTransactionServer

	txMgr *transactions.Manager
}

func NewServer(txMgr *transactions.Manager) gitalypb.RefTransactionServer {
	return &Server{
		txMgr: txMgr,
	}
}

// StartTransaction is called by a client who's starting a reference
// transaction, blocking until a vote across all participating nodes has been
// completed.
func (s *Server) StartTransaction(ctx context.Context, in *gitalypb.StartTransactionRequest) (*gitalypb.StartTransactionResponse, error) {
	err := s.txMgr.StartTransaction(ctx, in.TransactionId, in.Node, in.ReferenceUpdatesHash)
	if err != nil {
		return nil, err
	}

	return &gitalypb.StartTransactionResponse{
		State: gitalypb.StartTransactionResponse_COMMIT,
	}, nil
}
