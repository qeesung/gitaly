//go:build !gitaly_test_sha256

package server

import (
	"testing"

	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v15/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/version"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
)

func TestGitalyServerInfo(t *testing.T) {
	cfg := testcfg.Build(t)

	cfg.Storages = append(cfg.Storages, config.Storage{Name: "broken", Path: "/does/not/exist"})

	addr := runServer(t, cfg, testserver.WithDisablePraefect())

	client := newServerClient(t, addr)
	ctx := testhelper.Context(t)

	require.NoError(t, storage.WriteMetadataFile(cfg.Storages[0].Path))
	metadata, err := storage.ReadMetadataFile(cfg.Storages[0].Path)
	require.NoError(t, err)

	c, err := client.ServerInfo(ctx, &gitalypb.ServerInfoRequest{})
	require.NoError(t, err)

	require.Equal(t, version.GetVersion(), c.GetServerVersion())

	gitVersion, err := gittest.NewCommandFactory(t, cfg).GitVersion(ctx)
	require.NoError(t, err)
	require.Equal(t, gitVersion.String(), c.GetGitVersion())

	require.Len(t, c.GetStorageStatuses(), len(cfg.Storages))
	require.True(t, c.GetStorageStatuses()[0].Readable)
	require.True(t, c.GetStorageStatuses()[0].Writeable)
	require.NotEmpty(t, c.GetStorageStatuses()[0].FsType)
	require.Equal(t, uint32(1), c.GetStorageStatuses()[0].ReplicationFactor)

	require.False(t, c.GetStorageStatuses()[1].Readable)
	require.False(t, c.GetStorageStatuses()[1].Writeable)
	require.Equal(t, metadata.GitalyFilesystemID, c.GetStorageStatuses()[0].FilesystemId)
	require.Equal(t, uint32(1), c.GetStorageStatuses()[1].ReplicationFactor)
}

func runServer(t *testing.T, cfg config.Cfg, opts ...testserver.GitalyServerOpt) string {
	return testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterServerServiceServer(srv, NewServer(deps.GetGitCmdFactory(), deps.GetCfg().Storages))
	}, opts...)
}

func TestServerNoAuth(t *testing.T) {
	cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{Auth: auth.Config{Token: "some"}}))

	addr := runServer(t, cfg)

	conn, err := grpc.Dial(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { testhelper.MustClose(t, conn) })
	ctx := testhelper.Context(t)

	client := gitalypb.NewServerServiceClient(conn)
	_, err = client.ServerInfo(ctx, &gitalypb.ServerInfoRequest{})

	testhelper.RequireGrpcCode(t, err, codes.Unauthenticated)
}

func newServerClient(t *testing.T, serverSocketPath string) gitalypb.ServerServiceClient {
	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(testhelper.RepositoryAuthToken)),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { testhelper.MustClose(t, conn) })

	return gitalypb.NewServerServiceClient(conn)
}
