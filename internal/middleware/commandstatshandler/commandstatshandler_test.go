//go:build !gitaly_test_sha256

package commandstatshandler

import (
	"context"
	"io"
	"net"
	"testing"

	grpcmw "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcmwlogrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/ref"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/log"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func createNewServer(t *testing.T, cfg config.Cfg, logger *logrus.Logger) *grpc.Server {
	logrusEntry := logrus.NewEntry(logger).WithField("test", t.Name())

	opts := []grpc.ServerOption{
		grpc.StreamInterceptor(grpcmw.ChainStreamServer(
			StreamInterceptor,
			grpcmwlogrus.StreamServerInterceptor(logrusEntry,
				grpcmwlogrus.WithTimestampFormat(log.LogTimestampFormat),
				grpcmwlogrus.WithMessageProducer(log.MessageProducer(grpcmwlogrus.DefaultMessageProducer, FieldsProducer))),
		)),
		grpc.UnaryInterceptor(grpcmw.ChainUnaryServer(
			UnaryInterceptor,
			grpcmwlogrus.UnaryServerInterceptor(logrusEntry,
				grpcmwlogrus.WithTimestampFormat(log.LogTimestampFormat),
				grpcmwlogrus.WithMessageProducer(log.MessageProducer(grpcmwlogrus.DefaultMessageProducer, FieldsProducer))),
		)),
	}

	server := grpc.NewServer(opts...)

	gitCommandFactory := gittest.NewCommandFactory(t, cfg)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	gitalypb.RegisterRefServiceServer(server, ref.NewServer(
		config.NewLocator(cfg),
		gitCommandFactory,
		transaction.NewManager(cfg, backchannel.NewRegistry()),
		catfileCache,
	))

	return server
}

func getBufDialer(listener *bufconn.Listener) func(context.Context, string) (net.Conn, error) {
	return func(ctx context.Context, url string) (net.Conn, error) {
		return listener.Dial()
	}
}

func TestInterceptor(t *testing.T) {
	cfg, repo, _ := testcfg.BuildWithRepo(t)

	logger, hook := test.NewNullLogger()

	s := createNewServer(t, cfg, logger)
	defer s.Stop()

	bufferSize := 1024 * 1024
	listener := bufconn.Listen(bufferSize)
	go func() {
		err := s.Serve(listener)
		require.NoError(t, err)
	}()

	ctx := testhelper.Context(t)

	tests := []struct {
		name            string
		performRPC      func(ctx context.Context, client gitalypb.RefServiceClient)
		expectedLogData map[string]interface{}
	}{
		{
			name: "Unary",
			performRPC: func(ctx context.Context, client gitalypb.RefServiceClient) {
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
			performRPC: func(ctx context.Context, client gitalypb.RefServiceClient) {
				req := &gitalypb.FindAllBranchNamesRequest{Repository: repo}

				stream, err := client.FindAllBranchNames(ctx, req)
				require.NoError(t, err)

				for {
					_, err := stream.Recv()
					if err == io.EOF {
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

			tt.performRPC(ctx, client)

			logEntries := hook.AllEntries()
			require.Len(t, logEntries, 1)
			for expectedLogKey, expectedLogValue := range tt.expectedLogData {
				require.Contains(t, logEntries[0].Data, expectedLogKey)
				require.Equal(t, logEntries[0].Data[expectedLogKey], expectedLogValue)
			}
		})
	}
}

func TestFieldsProducer(t *testing.T) {
	ctx := testhelper.Context(t)

	ctx = command.InitContextStats(ctx)
	stats := command.StatsFromContext(ctx)
	stats.RecordMax("stub", 42)

	require.Equal(t, logrus.Fields{"stub": 42}, FieldsProducer(ctx))
}
