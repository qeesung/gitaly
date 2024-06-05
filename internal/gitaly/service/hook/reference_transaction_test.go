package hook

import (
	"context"
	"crypto/sha1"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type mockTransactionRegistry struct {
	getFunc func(storage.TransactionID) (hook.Transaction, error)
}

func (m mockTransactionRegistry) Get(id storage.TransactionID) (hook.Transaction, error) {
	return m.getFunc(id)
}

type mockTransaction struct {
	hook.Transaction
	updateReferencesFunc         func(storagemgr.ReferenceUpdates)
	recordInitialReferenceValues func(context.Context, map[git.ReferenceName]git.ObjectID) error
	markDefaultBranchUpdated     func()
}

func (m mockTransaction) UpdateReferences(updates storagemgr.ReferenceUpdates) {
	m.updateReferencesFunc(updates)
}

func (m mockTransaction) RecordInitialReferenceValues(ctx context.Context, initialValues map[git.ReferenceName]git.ObjectID) error {
	return m.recordInitialReferenceValues(ctx, initialValues)
}

func (m mockTransaction) MarkDefaultBranchUpdated() {
	m.markDefaultBranchUpdated()
}

type testTransactionServer struct {
	gitalypb.UnimplementedRefTransactionServer
	handler func(in *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error)
}

func (s *testTransactionServer) VoteTransaction(ctx context.Context, in *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
	if s.handler != nil {
		return s.handler(in)
	}
	return nil, nil
}

func TestReferenceTransactionHookInvalidArgument(t *testing.T) {
	cfg := testcfg.Build(t)
	serverSocketPath := runHooksServer(t, cfg, nil)

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()
	ctx := testhelper.Context(t)

	stream, err := client.ReferenceTransactionHook(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&gitalypb.ReferenceTransactionHookRequest{}))
	_, err = stream.Recv()

	testhelper.RequireGrpcError(t, structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet), err)
}

func TestReferenceTransactionHook(t *testing.T) {
	stdin := []byte(fmt.Sprintf(
		`%[1]s %[2]s refs/heads/branch-1
%[2]s %[1]s refs/heads/branch-2
%[1]s %[2]s HEAD
`,
		gittest.DefaultObjectHash.ZeroOID,
		gittest.DefaultObjectHash.EmptyTreeOID,
	))

	testCases := []struct {
		desc                         string
		stdin                        []byte
		state                        gitalypb.ReferenceTransactionHookRequest_State
		voteResponse                 gitalypb.VoteTransactionResponse_TransactionState
		noTransaction                bool
		expectedErr                  error
		expectedResponse             *gitalypb.ReferenceTransactionHookResponse
		expectedReftxHash            []byte
		expectedReferenceUpdates     storagemgr.ReferenceUpdates
		expectedInitialValues        map[git.ReferenceName]git.ObjectID
		expectedDefaultBranchUpdated bool
	}{
		{
			desc:         "hook triggers transaction with default state",
			stdin:        stdin,
			voteResponse: gitalypb.VoteTransactionResponse_COMMIT,
			expectedResponse: &gitalypb.ReferenceTransactionHookResponse{
				ExitStatus: &gitalypb.ExitStatus{
					Value: 0,
				},
			},
			expectedReftxHash: stdin,
			expectedInitialValues: map[git.ReferenceName]git.ObjectID{
				"refs/heads/branch-1": gittest.DefaultObjectHash.ZeroOID,
				"refs/heads/branch-2": gittest.DefaultObjectHash.EmptyTreeOID,
			},
		},
		{
			desc:         "hook triggers transaction with explicit prepared state",
			stdin:        stdin,
			state:        gitalypb.ReferenceTransactionHookRequest_PREPARED,
			voteResponse: gitalypb.VoteTransactionResponse_COMMIT,
			expectedResponse: &gitalypb.ReferenceTransactionHookResponse{
				ExitStatus: &gitalypb.ExitStatus{
					Value: 0,
				},
			},
			expectedReftxHash: stdin,
			expectedInitialValues: map[git.ReferenceName]git.ObjectID{
				"refs/heads/branch-1": gittest.DefaultObjectHash.ZeroOID,
				"refs/heads/branch-2": gittest.DefaultObjectHash.EmptyTreeOID,
			},
		},
		{
			desc:          "hook triggers transaction with explicit prepared state without transaction",
			stdin:         stdin,
			state:         gitalypb.ReferenceTransactionHookRequest_PREPARED,
			voteResponse:  gitalypb.VoteTransactionResponse_COMMIT,
			noTransaction: true,
			expectedResponse: &gitalypb.ReferenceTransactionHookResponse{
				ExitStatus: &gitalypb.ExitStatus{
					Value: 0,
				},
			},
			expectedReftxHash: stdin,
		},
		{
			desc:  "hook does not trigger transaction with aborted state",
			stdin: stdin,
			state: gitalypb.ReferenceTransactionHookRequest_ABORTED,
			expectedResponse: &gitalypb.ReferenceTransactionHookResponse{
				ExitStatus: &gitalypb.ExitStatus{
					Value: 0,
				},
			},
		},
		{
			desc:  "hook triggers transaction with committed state",
			stdin: stdin,
			state: gitalypb.ReferenceTransactionHookRequest_COMMITTED,
			expectedResponse: &gitalypb.ReferenceTransactionHookResponse{
				ExitStatus: &gitalypb.ExitStatus{
					Value: 0,
				},
			},
			expectedReftxHash: stdin,
			expectedReferenceUpdates: storagemgr.ReferenceUpdates{
				"refs/heads/branch-1": {
					OldOID: gittest.DefaultObjectHash.ZeroOID,
					NewOID: gittest.DefaultObjectHash.EmptyTreeOID,
				},
				"refs/heads/branch-2": {
					OldOID: gittest.DefaultObjectHash.EmptyTreeOID,
					NewOID: gittest.DefaultObjectHash.ZeroOID,
				},
			},
			expectedDefaultBranchUpdated: true,
		},
		{
			desc:          "hook triggers transaction with committed state without transaction",
			stdin:         stdin,
			state:         gitalypb.ReferenceTransactionHookRequest_COMMITTED,
			noTransaction: true,
			expectedResponse: &gitalypb.ReferenceTransactionHookResponse{
				ExitStatus: &gitalypb.ExitStatus{
					Value: 0,
				},
			},
			expectedReftxHash: stdin,
		},
		{
			desc:              "hook fails with failed vote",
			stdin:             stdin,
			voteResponse:      gitalypb.VoteTransactionResponse_ABORT,
			expectedErr:       structerr.NewAborted("reference-transaction hook: error voting on transaction: transaction was aborted"),
			expectedReftxHash: stdin,
			expectedInitialValues: map[git.ReferenceName]git.ObjectID{
				"refs/heads/branch-1": gittest.DefaultObjectHash.ZeroOID,
				"refs/heads/branch-2": gittest.DefaultObjectHash.EmptyTreeOID,
			},
		},
		{
			desc:              "hook fails with stopped vote",
			stdin:             stdin,
			voteResponse:      gitalypb.VoteTransactionResponse_STOP,
			expectedErr:       structerr.NewFailedPrecondition("reference-transaction hook: error voting on transaction: transaction was stopped"),
			expectedReftxHash: stdin,
			expectedInitialValues: map[git.ReferenceName]git.ObjectID{
				"refs/heads/branch-1": gittest.DefaultObjectHash.ZeroOID,
				"refs/heads/branch-2": gittest.DefaultObjectHash.EmptyTreeOID,
			},
		},
		{
			desc:         "invalid change line",
			stdin:        []byte("invalid change_line"),
			voteResponse: gitalypb.VoteTransactionResponse_STOP,
			expectedErr:  structerr.NewInternal(`reference-transaction hook: parse changes: unexpected change line: "invalid change_line"`),
		},
		{
			desc:         "invalid old oid",
			stdin:        []byte(fmt.Sprintf("invalid %s refs/heads/main", gittest.DefaultObjectHash.EmptyTreeOID)),
			voteResponse: gitalypb.VoteTransactionResponse_STOP,
			expectedErr:  structerr.NewInternal(`reference-transaction hook: parse changes: parse old: invalid object ID: "invalid", expected length %d, got 7`, gittest.DefaultObjectHash.EncodedLen()),
		},
		{
			desc:         "invalid new oid",
			stdin:        []byte(fmt.Sprintf("%s invalid refs/heads/main", gittest.DefaultObjectHash.EmptyTreeOID)),
			voteResponse: gitalypb.VoteTransactionResponse_STOP,
			expectedErr:  structerr.NewInternal(`reference-transaction hook: parse changes: parse new: invalid object ID: "invalid", expected length %d, got 7`, gittest.DefaultObjectHash.EncodedLen()),
		},
	}

	transactionServer := &testTransactionServer{}
	grpcServer := grpc.NewServer()
	gitalypb.RegisterRefTransactionServer(grpcServer, transactionServer)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	backchannelConn, err := grpc.Dial(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer backchannelConn.Close()

	registry := backchannel.NewRegistry()
	backchannelID := registry.RegisterBackchannel(backchannelConn)

	errQ := make(chan error)
	go func() {
		errQ <- grpcServer.Serve(listener)
	}()
	defer func() {
		grpcServer.Stop()
		require.NoError(t, <-errQ)
	}()

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg := testcfg.Build(t)

			var reftxHash []byte
			transactionServer.handler = func(in *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
				reftxHash = in.ReferenceUpdatesHash
				return &gitalypb.VoteTransactionResponse{
					State: tc.voteResponse,
				}, nil
			}

			var actualReferenceUpdates storagemgr.ReferenceUpdates
			var actualInitialValues map[git.ReferenceName]git.ObjectID
			var defaultBranchUpdated bool
			txRegistry := mockTransactionRegistry{
				getFunc: func(storage.TransactionID) (hook.Transaction, error) {
					return mockTransaction{
						updateReferencesFunc: func(updates storagemgr.ReferenceUpdates) {
							actualReferenceUpdates = updates
						},
						recordInitialReferenceValues: func(_ context.Context, initialValues map[git.ReferenceName]git.ObjectID) error {
							actualInitialValues = initialValues
							return nil
						},
						markDefaultBranchUpdated: func() {
							defaultBranchUpdated = true
						},
					}, nil
				},
			}

			cfg.SocketPath = runHooksServerWithTransactionRegistry(t, cfg, nil, txRegistry, testserver.WithBackchannelRegistry(registry))
			ctx := testhelper.Context(t)

			repo, _ := gittest.CreateRepository(t, ctx, cfg)

			transactionID := storage.TransactionID(1)
			if tc.noTransaction {
				transactionID = 0
			}
			hooksPayload, err := git.NewHooksPayload(
				cfg,
				repo,
				gittest.DefaultObjectHash,
				&txinfo.Transaction{
					BackchannelID: backchannelID,
					ID:            1234,
					Node:          "node-1",
				},
				nil,
				git.ReferenceTransactionHook,
				featureflag.FromContext(ctx),
				transactionID,
			).Env()
			require.NoError(t, err)

			environment := []string{
				hooksPayload,
			}

			client, conn := newHooksClient(t, cfg.SocketPath)
			defer conn.Close()

			stream, err := client.ReferenceTransactionHook(ctx)
			require.NoError(t, err)
			require.NoError(t, stream.Send(&gitalypb.ReferenceTransactionHookRequest{
				Repository:           repo,
				State:                tc.state,
				EnvironmentVariables: environment,
			}))
			require.NoError(t, stream.Send(&gitalypb.ReferenceTransactionHookRequest{
				Stdin: tc.stdin,
			}))
			require.NoError(t, stream.CloseSend())

			resp, err := stream.Recv()
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
			testhelper.ProtoEqual(t, tc.expectedResponse, resp)

			var expectedReftxHash []byte
			if tc.expectedReftxHash != nil {
				hash := sha1.Sum(tc.stdin)
				expectedReftxHash = hash[:]
			}
			require.Equal(t, expectedReftxHash[:], reftxHash)

			require.Equal(t, tc.expectedReferenceUpdates, actualReferenceUpdates)
			require.Equal(t, tc.expectedInitialValues, actualInitialValues)
			require.Equal(t, tc.expectedDefaultBranchUpdated, defaultBranchUpdated)
		})
	}
}
