package cache

import (
	"context"
	"io"
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	diskcache "gitlab.com/gitlab-org/gitaly/v16/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestInvalidators(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	repo := &gitalypb.Repository{
		GitAlternateObjectDirectories: []string{"1"},
		GitObjectDirectory:            "1",
		GlProjectPath:                 "1",
		GlRepository:                  "1",
		RelativePath:                  "1",
		StorageName:                   "1",
	}

	for _, tc := range []struct {
		desc                  string
		invokeRPC             func(*testing.T, *grpc.ClientConn)
		expectedInvalidations []*gitalypb.Repository
	}{
		{
			desc: "streaming accessor does not invalidate cache",
			invokeRPC: func(t *testing.T, conn *grpc.ClientConn) {
				stream, err := gitalypb.NewRepositoryServiceClient(conn).GetConfig(ctx, &gitalypb.GetConfigRequest{
					Repository: repo,
				})
				require.NoError(t, err)

				_, err = stream.Recv()
				require.Equal(t, err, io.EOF)
			},
		},
		{
			desc: "streaming mutator invalidates cache",
			invokeRPC: func(t *testing.T, conn *grpc.ClientConn) {
				stream, err := gitalypb.NewSSHServiceClient(conn).SSHReceivePack(ctx)
				require.NoError(t, err)
				require.NoError(t, stream.Send(&gitalypb.SSHReceivePackRequest{
					Repository: repo,
				}))

				_, err = stream.Recv()
				require.Equal(t, err, io.EOF)
			},
			expectedInvalidations: []*gitalypb.Repository{repo},
		},
		{
			desc: "unary accessor does not invalidate cache",
			invokeRPC: func(t *testing.T, conn *grpc.ClientConn) {
				_, err := gitalypb.NewRepositoryServiceClient(conn).RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
					Repository: repo,
				})
				require.NoError(t, err)
			},
		},
		{
			desc: "unary maintainer does not invalidate cache",
			invokeRPC: func(t *testing.T, conn *grpc.ClientConn) {
				_, err := gitalypb.NewRepositoryServiceClient(conn).OptimizeRepository(ctx, &gitalypb.OptimizeRepositoryRequest{
					Repository: repo,
				})
				require.NoError(t, err)
			},
		},
		{
			desc: "unary mutator invalidates cache",
			invokeRPC: func(t *testing.T, conn *grpc.ClientConn) {
				_, err := gitalypb.NewRepositoryServiceClient(conn).WriteRef(ctx, &gitalypb.WriteRefRequest{
					Repository: repo,
				})
				require.NoError(t, err)
			},
			expectedInvalidations: []*gitalypb.Repository{repo},
		},
		{
			desc: "health check does not invalidate cache",
			invokeRPC: func(t *testing.T, conn *grpc.ClientConn) {
				_, err := grpc_health_v1.NewHealthClient(conn).Check(ctx, &grpc_health_v1.HealthCheckRequest{
					Service: "TestService",
				})
				require.NoError(t, err)
			},
		},
		{
			desc: "intercepted method does not invalidate cache",
			invokeRPC: func(t *testing.T, conn *grpc.ClientConn) {
				_, err := gitalypb.NewPraefectInfoServiceClient(conn).GetRepositoryMetadata(ctx, &gitalypb.GetRepositoryMetadataRequest{})
				require.NoError(t, err)
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			mockCache := newMockCache()

			server := grpc.NewServer(
				grpc.StreamInterceptor(StreamInvalidator(mockCache, protoregistry.GitalyProtoPreregistered, testhelper.SharedLogger(t))),
				grpc.UnaryInterceptor(UnaryInvalidator(mockCache, protoregistry.GitalyProtoPreregistered, testhelper.SharedLogger(t))),
			)

			service := &testService{
				requestCh: make(chan bool, 1),
			}
			gitalypb.RegisterSSHServiceServer(server, service)
			gitalypb.RegisterRepositoryServiceServer(server, service)
			gitalypb.RegisterPraefectInfoServiceServer(server, service)
			grpc_health_v1.RegisterHealthServer(server, service)

			listener, err := net.Listen("tcp", ":0")
			require.NoError(t, err)
			go func() {
				testhelper.MustServe(t, server, listener)
			}()
			t.Cleanup(server.Stop)

			conn, err := grpc.DialContext(
				ctx,
				listener.Addr().String(),
				grpc.WithBlock(),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			require.NoError(t, err)
			defer testhelper.MustClose(t, conn)

			tc.invokeRPC(t, conn)

			// Check that the RPC has been executed.
			require.True(t, <-service.requestCh)
			// Verify that the repository's cache was invalidated as expected.
			testhelper.ProtoEqual(t, tc.expectedInvalidations, mockCache.invalidatedRepos)
			// Verify that all leases that were created have ended.
			require.Equal(t, len(mockCache.invalidatedRepos), mockCache.endedLeases.count)
			// And furthermore, verify that we didn't see an error.
			require.Empty(t, MethodErrCount.Method)
		})
	}
}

// mockCache allows us to relay back via channel which repos are being
// invalidated in the cache
type mockCache struct {
	invalidatedRepos []*gitalypb.Repository
	endedLeases      *struct {
		sync.RWMutex
		count int
	}
}

func newMockCache() *mockCache {
	return &mockCache{
		endedLeases: &struct {
			sync.RWMutex
			count int
		}{},
	}
}

func (mc *mockCache) EndLease(_ context.Context) error {
	mc.endedLeases.Lock()
	defer mc.endedLeases.Unlock()
	mc.endedLeases.count++

	return nil
}

func (mc *mockCache) StartLease(_ context.Context, repo *gitalypb.Repository) (diskcache.LeaseEnder, error) {
	mc.invalidatedRepos = append(mc.invalidatedRepos, repo)
	return mc, nil
}

type testService struct {
	gitalypb.UnimplementedSSHServiceServer
	gitalypb.UnimplementedRepositoryServiceServer
	gitalypb.UnimplementedPraefectInfoServiceServer
	grpc_health_v1.UnimplementedHealthServer
	requestCh chan bool
}

func (ts *testService) SSHReceivePack(server gitalypb.SSHService_SSHReceivePackServer) error {
	_, err := server.Recv()
	ts.requestCh <- true
	return err
}

func (ts *testService) GetConfig(*gitalypb.GetConfigRequest, gitalypb.RepositoryService_GetConfigServer) error {
	ts.requestCh <- true
	return nil
}

func (ts *testService) WriteRef(context.Context, *gitalypb.WriteRefRequest) (*gitalypb.WriteRefResponse, error) {
	ts.requestCh <- true
	return &gitalypb.WriteRefResponse{}, nil
}

func (ts *testService) RepositoryExists(context.Context, *gitalypb.RepositoryExistsRequest) (*gitalypb.RepositoryExistsResponse, error) {
	ts.requestCh <- true
	return &gitalypb.RepositoryExistsResponse{}, nil
}

func (ts *testService) OptimizeRepository(context.Context, *gitalypb.OptimizeRepositoryRequest) (*gitalypb.OptimizeRepositoryResponse, error) {
	ts.requestCh <- true
	return &gitalypb.OptimizeRepositoryResponse{}, nil
}

func (ts *testService) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	ts.requestCh <- true
	return &grpc_health_v1.HealthCheckResponse{}, nil
}

func (ts *testService) GetRepositoryMetadata(context.Context, *gitalypb.GetRepositoryMetadataRequest) (*gitalypb.GetRepositoryMetadataResponse, error) {
	ts.requestCh <- true
	return &gitalypb.GetRepositoryMetadataResponse{}, nil
}
