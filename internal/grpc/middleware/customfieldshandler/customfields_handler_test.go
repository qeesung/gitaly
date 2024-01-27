package customfieldshandler

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/ref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func createNewServer(t *testing.T, cfg config.Cfg, logger log.Logger) *grpc.Server {
	t.Helper()

	opts := []grpc.ServerOption{
		grpc.ChainStreamInterceptor(
			StreamInterceptor,
			logger.StreamServerInterceptor(
				log.DefaultInterceptorLogger(logger),
				log.WithTimestampFormat(log.LogTimestampFormat),
				log.WithFiledProducers(FieldsProducer),
			),
		),
		grpc.ChainUnaryInterceptor(
			UnaryInterceptor,
			logger.UnaryServerInterceptor(
				log.DefaultInterceptorLogger(logger),
				log.WithTimestampFormat(log.LogTimestampFormat),
				log.WithFiledProducers(FieldsProducer),
			),
		),
	}

	server := grpc.NewServer(opts...)

	gitCommandFactory := gittest.NewCommandFactory(t, cfg)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	gitalypb.RegisterRefServiceServer(server, ref.NewServer(&service.Dependencies{
		Logger:             logger,
		StorageLocator:     config.NewLocator(cfg),
		GitCmdFactory:      gitCommandFactory,
		TransactionManager: transaction.NewManager(cfg, logger, backchannel.NewRegistry()),
		CatfileCache:       catfileCache,
	}))

	return server
}

func getBufDialer(listener *bufconn.Listener) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, url string) (net.Conn, error) {
		return listener.Dial()
	}
}

func TestInterceptor(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	logger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(logger)

	s := createNewServer(t, cfg, logger)
	defer s.Stop()

	bufferSize := 1024 * 1024
	listener := bufconn.Listen(bufferSize)
	go testhelper.MustServe(t, s, listener)

	tests := []struct {
		name            string
		performRPC      func(t *testing.T, ctx context.Context, client gitalypb.RefServiceClient)
		expectedLogData map[string]interface{}
	}{
		{
			name: "Unary",
			performRPC: func(t *testing.T, ctx context.Context, client gitalypb.RefServiceClient) {
				req := &gitalypb.RefExistsRequest{Repository: repo, Ref: []byte("refs/foo")}

				_, err := client.RefExists(ctx, req)
				require.NoError(t, err)
			},
			expectedLogData: map[string]interface{}{
				// Until we bump our minimum required Git version to v2.36.0 we are
				// forced to carry a version check whenever we spawn Git commands.
				// The command count here is thus 2 because of the additional
				// git-version(1) command. Note that the next command count remains
				// 1 though: the version is cached, and consequentially we don't
				// re-run git-version(1).
				//
				// This test will break again as soon as we bump the minimum Git
				// version and thus remove the version check.
				"command.count": 2,
			},
		},
		{
			name: "Stream",
			performRPC: func(t *testing.T, ctx context.Context, client gitalypb.RefServiceClient) {
				req := &gitalypb.ListRefsRequest{Repository: repo, Patterns: [][]byte{[]byte("refs/heads/")}}

				stream, err := client.ListRefs(ctx, req)
				require.NoError(t, err)

				for {
					_, err := stream.Recv()
					if errors.Is(err, io.EOF) {
						break
					}
					require.NoError(t, err)
				}
			},
			expectedLogData: map[string]interface{}{
				"command.count": 1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hook.Reset()

			conn, err := grpc.DialContext(ctx, "", grpc.WithContextDialer(getBufDialer(listener)), grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			defer conn.Close()

			client := gitalypb.NewRefServiceClient(conn)

			tt.performRPC(t, ctx, client)

			logEntries := hook.AllEntries()
			require.Len(t, logEntries, 2) // 1 for the starting RPC call, 1 for finishing it
			for expectedLogKey, expectedLogValue := range tt.expectedLogData {
				require.Contains(t, logEntries[1].Data, expectedLogKey)
				require.Equal(t, logEntries[1].Data[expectedLogKey], expectedLogValue)
			}
		})
	}
}

func TestFieldsProducer(t *testing.T) {
	ctx := testhelper.Context(t)

	ctx = log.InitContextCustomFields(ctx)
	fields := log.CustomFieldsFromContext(ctx)
	fields.RecordMax("stub", 42)

	require.Equal(t, log.Fields{"stub": 42}, FieldsProducer(ctx, nil))
}
