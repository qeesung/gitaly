package transaction_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/metadata"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testTransactionServer struct {
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
	testhelper.NewFeatureSets([]featureflag.FeatureFlag{
		featureflag.BackchannelVoting,
	}).Run(t, func(t *testing.T, ctx context.Context) {
		cfg := testcfg.Build(t)

		transactionServer, praefect := runTransactionServer(t, cfg)

		registry := backchannel.NewRegistry()
		if featureflag.IsEnabled(ctx, featureflag.BackchannelVoting) {
			backchannelConn, err := client.Dial(ctx, praefect.ListenAddr, nil, nil)
			require.NoError(t, err)
			defer backchannelConn.Close()
			praefect = metadata.PraefectServer{BackchannelID: registry.RegisterBackchannel(backchannelConn)}
		}

		manager := transaction.NewManager(cfg, registry)

		for _, tc := range []struct {
			desc        string
			transaction metadata.Transaction
			vote        transaction.Vote
			voteFn      func(*testing.T, *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error)
			expectedErr error
		}{
			{
				desc: "successful vote",
				transaction: metadata.Transaction{
					ID:   1,
					Node: "node",
				},
				vote: transaction.VoteFromData([]byte("foobar")),
				voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
					require.Equal(t, uint64(1), request.TransactionId)
					require.Equal(t, "node", request.Node)
					require.Equal(t, request.ReferenceUpdatesHash, transaction.VoteFromData([]byte("foobar")).Bytes())

					return &gitalypb.VoteTransactionResponse{
						State: gitalypb.VoteTransactionResponse_COMMIT,
					}, nil
				},
			},
			{
				desc: "aborted vote",
				voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
					return &gitalypb.VoteTransactionResponse{
						State: gitalypb.VoteTransactionResponse_ABORT,
					}, nil
				},
				expectedErr: errors.New("transaction was aborted"),
			},
			{
				desc: "stopped vote",
				voteFn: func(t *testing.T, request *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
					return &gitalypb.VoteTransactionResponse{
						State: gitalypb.VoteTransactionResponse_STOP,
					}, nil
				},
				expectedErr: errors.New("transaction was stopped"),
			},
			{
				desc: "erroneous vote",
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

				err := manager.Vote(ctx, tc.transaction, praefect, tc.vote)
				require.Equal(t, tc.expectedErr, err)
			})
		}
	})
}

func TestPoolManager_Stop(t *testing.T) {
	testhelper.NewFeatureSets([]featureflag.FeatureFlag{
		featureflag.BackchannelVoting,
	}).Run(t, func(t *testing.T, ctx context.Context) {
		cfg := testcfg.Build(t)

		transactionServer, praefect := runTransactionServer(t, cfg)

		registry := backchannel.NewRegistry()
		if featureflag.IsEnabled(ctx, featureflag.BackchannelVoting) {
			backchannelConn, err := client.Dial(ctx, praefect.ListenAddr, nil, nil)
			require.NoError(t, err)
			defer backchannelConn.Close()
			praefect = metadata.PraefectServer{BackchannelID: registry.RegisterBackchannel(backchannelConn)}
		}

		manager := transaction.NewManager(cfg, registry)

		for _, tc := range []struct {
			desc        string
			transaction metadata.Transaction
			stopFn      func(*testing.T, *gitalypb.StopTransactionRequest) (*gitalypb.StopTransactionResponse, error)
			expectedErr error
		}{
			{
				desc: "successful stop",
				transaction: metadata.Transaction{
					ID:   1,
					Node: "node",
				},
				stopFn: func(t *testing.T, request *gitalypb.StopTransactionRequest) (*gitalypb.StopTransactionResponse, error) {
					require.Equal(t, uint64(1), request.TransactionId)
					return &gitalypb.StopTransactionResponse{}, nil
				},
			},
			{
				desc: "erroneous stop",
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

				err := manager.Stop(ctx, tc.transaction, praefect)
				require.Equal(t, tc.expectedErr, err)
			})
		}
	})
}

func runTransactionServer(t *testing.T, cfg config.Cfg) (*testTransactionServer, metadata.PraefectServer) {
	transactionServer := &testTransactionServer{}
	cfg.ListenAddr = ":0" // pushes gRPC to listen on the TCP address
	addr := testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterRefTransactionServer(srv, transactionServer)
	}, testserver.WithDisablePraefect())

	praefect := metadata.PraefectServer{
		ListenAddr: addr,
		Token:      cfg.Auth.Token,
	}

	return transactionServer, praefect
}
