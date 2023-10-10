package transaction_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testTransactionServer struct {
	gitalypb.UnimplementedRefTransactionServer
	vote func(*gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error)
	stop func(*gitalypb.StopTransactionRequest) (*gitalypb.StopTransactionResponse, error)
}

func (s *testTransactionServer) VoteTransaction(ctx context.Context, in *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
	if s.vote != nil {
		return s.vote(in)
	}
	return nil, nil
}

func (s *testTransactionServer) StopTransaction(ctx context.Context, in *gitalypb.StopTransactionRequest) (*gitalypb.StopTransactionResponse, error) {
	if s.stop != nil {
		return s.stop(in)
	}
	return nil, nil
}

func TestPoolManager_Vote(t *testing.T) {
	cfg := testcfg.Build(t)

	transactionServer, transactionServerAddr := runTransactionServer(t, cfg)
	ctx := testhelper.Context(t)
	logger := testhelper.NewLogger(t)

	registry := backchannel.NewRegistry()
	backchannelConn, err := client.Dial(ctx, transactionServerAddr)
	require.NoError(t, err)
	defer backchannelConn.Close()

	backchannelID := registry.RegisterBackchannel(backchannelConn)

	manager := transaction.NewManager(cfg, logger, registry)

	for _, tc := range []struct {
		desc        string
		transaction txinfo.Transaction
		vote        voting.Vote
		phase       voting.Phase
		voteFn      func(*testing.T, *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error)
		expectedErr error
	}{
		{
			desc: "successful unknown vote",
			transaction: txinfo.Transaction{
				BackchannelID: backchannelID,
				ID:            1,
				Node:          "node",
			},
			vote:  voting.VoteFromData([]byte("foobar")),
			phase: voting.UnknownPhase,
			voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				require.Equal(t, uint64(1), request.TransactionId)
				require.Equal(t, "node", request.Node)
				require.Equal(t, request.ReferenceUpdatesHash, voting.VoteFromData([]byte("foobar")).Bytes())
				require.Equal(t, gitalypb.VoteTransactionRequest_UNKNOWN_PHASE, request.Phase)

				return &gitalypb.VoteTransactionResponse{
					State: gitalypb.VoteTransactionResponse_COMMIT,
				}, nil
			},
		},
		{
			desc: "successful prepared vote",
			transaction: txinfo.Transaction{
				BackchannelID: backchannelID,
				ID:            1,
				Node:          "node",
			},
			vote:  voting.VoteFromData([]byte("foobar")),
			phase: voting.Prepared,
			voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				require.Equal(t, uint64(1), request.TransactionId)
				require.Equal(t, "node", request.Node)
				require.Equal(t, request.ReferenceUpdatesHash, voting.VoteFromData([]byte("foobar")).Bytes())
				require.Equal(t, gitalypb.VoteTransactionRequest_PREPARED_PHASE, request.Phase)

				return &gitalypb.VoteTransactionResponse{
					State: gitalypb.VoteTransactionResponse_COMMIT,
				}, nil
			},
		},
		{
			desc: "successful committed vote",
			transaction: txinfo.Transaction{
				BackchannelID: backchannelID,
				ID:            1,
				Node:          "node",
			},
			vote:  voting.VoteFromData([]byte("foobar")),
			phase: voting.Committed,
			voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				require.Equal(t, uint64(1), request.TransactionId)
				require.Equal(t, "node", request.Node)
				require.Equal(t, request.ReferenceUpdatesHash, voting.VoteFromData([]byte("foobar")).Bytes())
				require.Equal(t, gitalypb.VoteTransactionRequest_COMMITTED_PHASE, request.Phase)

				return &gitalypb.VoteTransactionResponse{
					State: gitalypb.VoteTransactionResponse_COMMIT,
				}, nil
			},
		},
		{
			desc: "successful synchronized vote",
			transaction: txinfo.Transaction{
				BackchannelID: backchannelID,
				ID:            1,
				Node:          "node",
			},
			vote:  voting.VoteFromData([]byte("foobar")),
			phase: voting.Synchronized,
			voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				require.Equal(t, uint64(1), request.TransactionId)
				require.Equal(t, "node", request.Node)
				require.Equal(t, request.ReferenceUpdatesHash, voting.VoteFromData([]byte("foobar")).Bytes())
				require.Equal(t, gitalypb.VoteTransactionRequest_SYNCHRONIZED_PHASE, request.Phase)

				return &gitalypb.VoteTransactionResponse{
					State: gitalypb.VoteTransactionResponse_COMMIT,
				}, nil
			},
		},
		{
			desc: "aborted vote",
			transaction: txinfo.Transaction{
				BackchannelID: backchannelID,
			},
			voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				return &gitalypb.VoteTransactionResponse{
					State: gitalypb.VoteTransactionResponse_ABORT,
				}, nil
			},
			expectedErr: errors.New("transaction was aborted"),
		},
		{
			desc: "stopped vote",
			transaction: txinfo.Transaction{
				BackchannelID: backchannelID,
			},
			voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				return &gitalypb.VoteTransactionResponse{
					State: gitalypb.VoteTransactionResponse_STOP,
				}, nil
			},
			expectedErr: errors.New("transaction was stopped"),
		},
		{
			desc: "erroneous vote",
			transaction: txinfo.Transaction{
				BackchannelID: backchannelID,
			},
			voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				return nil, status.Error(codes.Internal, "foobar")
			},
			expectedErr: status.Error(codes.Internal, "foobar"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			transactionServer.vote = func(request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				return tc.voteFn(t, request)
			}

			err := manager.Vote(ctx, tc.transaction, tc.vote, tc.phase)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func TestPoolManager_Stop(t *testing.T) {
	cfg := testcfg.Build(t)

	transactionServer, transactionServerAddr := runTransactionServer(t, cfg)
	ctx := testhelper.Context(t)
	logger := testhelper.NewLogger(t)

	registry := backchannel.NewRegistry()
	backchannelConn, err := client.Dial(ctx, transactionServerAddr)
	require.NoError(t, err)
	defer backchannelConn.Close()

	backchannelID := registry.RegisterBackchannel(backchannelConn)

	manager := transaction.NewManager(cfg, logger, registry)

	for _, tc := range []struct {
		desc        string
		transaction txinfo.Transaction
		stopFn      func(*testing.T, *gitalypb.StopTransactionRequest) (*gitalypb.StopTransactionResponse, error)
		expectedErr error
	}{
		{
			desc: "successful stop",
			transaction: txinfo.Transaction{
				BackchannelID: backchannelID,
				ID:            1,
				Node:          "node",
			},
			stopFn: func(t *testing.T, request *gitalypb.StopTransactionRequest) (*gitalypb.StopTransactionResponse, error) {
				require.Equal(t, uint64(1), request.TransactionId)
				return &gitalypb.StopTransactionResponse{}, nil
			},
		},
		{
			desc: "erroneous stop",
			transaction: txinfo.Transaction{
				BackchannelID: backchannelID,
			},
			stopFn: func(t *testing.T, request *gitalypb.StopTransactionRequest) (*gitalypb.StopTransactionResponse, error) {
				return nil, status.Error(codes.Internal, "foobar")
			},
			expectedErr: status.Error(codes.Internal, "foobar"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			transactionServer.stop = func(request *gitalypb.StopTransactionRequest) (*gitalypb.StopTransactionResponse, error) {
				return tc.stopFn(t, request)
			}

			err := manager.Stop(ctx, tc.transaction)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func runTransactionServer(t *testing.T, cfg config.Cfg) (*testTransactionServer, string) {
	transactionServer := &testTransactionServer{}
	cfg.ListenAddr = "localhost:0" // pushes gRPC to listen on the TCP address
	addr := testserver.RunGitalyServer(t, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterRefTransactionServer(srv, transactionServer)
	}, testserver.WithDisablePraefect())
	return transactionServer, addr
}
