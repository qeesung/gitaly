package praefect

import (
	"context"
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/bootstrap/starter"
	gconfig "gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/internal/server"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/promtest"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestServerFactory(t *testing.T) {
	gitalyServerFactory := server.NewGitalyServerFactory(nil)
	defer gitalyServerFactory.Stop()

	// start gitaly serving on public endpoint
	gitalyListener, err := net.Listen(starter.TCP, ":0")
	require.NoError(t, err)
	defer func() { require.NoError(t, gitalyListener.Close()) }()
	go gitalyServerFactory.Serve(gitalyListener, false)

	// start gitaly serving on internal endpoint
	gitalyInternalSocketPath := gconfig.GitalyInternalSocketPath()
	defer func() { require.NoError(t, os.RemoveAll(gitalyInternalSocketPath)) }()
	gitalyInternalListener, err := net.Listen(starter.Unix, gitalyInternalSocketPath)
	require.NoError(t, err)
	defer func() { require.NoError(t, gitalyInternalListener.Close()) }()
	go gitalyServerFactory.Serve(gitalyInternalListener, false)

	gitalyAddr, err := starter.ComposeEndpoint(gitalyListener.Addr().Network(), gitalyListener.Addr().String())
	require.NoError(t, err)

	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "praefect",
				Nodes: []*config.Node{
					{
						DefaultPrimary: true,
						Storage:        gconfig.Config.Storages[0].Name,
						Address:        gitalyAddr,
						Token:          gconfig.Config.Auth.Token,
					},
				},
			},
		},
	}

	repo, repoPath, cleanup := testhelper.NewTestRepo(t)
	defer cleanup()
	repo.StorageName = conf.VirtualStorages[0].Name // storage must be re-written to virtual to be properly redirected by praefect
	revision := text.ChompBytes(testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "rev-parse", "HEAD"))

	logger := testhelper.DiscardTestEntry(t)
	queue := datastore.NewMemoryReplicationEventQueue(conf)
	nodeMgr, err := nodes.NewManager(logger, conf, nil, queue, &promtest.MockHistogramVec{})
	require.NoError(t, err)
	txMgr := transactions.NewManager()
	registry := protoregistry.GitalyProtoPreregistered
	coordinator := NewCoordinator(queue, nodeMgr, txMgr, conf, registry)

	checkOwnRegisteredServices := func(ctx context.Context, t *testing.T, cc *grpc.ClientConn) healthpb.HealthClient {
		healthClient := healthpb.NewHealthClient(cc)
		resp, err := healthClient.Check(ctx, &healthpb.HealthCheckRequest{})
		require.NoError(t, err)
		require.Equal(t, healthpb.HealthCheckResponse_SERVING, resp.Status)
		return healthClient
	}

	checkProxyingOntoGitaly := func(ctx context.Context, t *testing.T, cc *grpc.ClientConn) {
		commitClient := gitalypb.NewCommitServiceClient(cc)
		resp, err := commitClient.CommitLanguages(ctx, &gitalypb.CommitLanguagesRequest{Repository: repo, Revision: []byte(revision)})
		require.NoError(t, err)
		require.Len(t, resp.Languages, 4)
	}

	t.Run("insecure", func(t *testing.T) {
		praefectServerFactory := NewServerFactory(conf, logger, coordinator.StreamDirector, nodeMgr, txMgr, queue, registry)
		defer praefectServerFactory.Stop()

		listener, err := net.Listen(starter.TCP, ":0")
		require.NoError(t, err)
		defer func() { require.NoError(t, listener.Close()) }()

		go praefectServerFactory.Serve(listener, false)

		praefectAddr, err := starter.ComposeEndpoint(listener.Addr().Network(), listener.Addr().String())
		require.NoError(t, err)

		cc, err := client.Dial(praefectAddr, nil)
		require.NoError(t, err)
		defer func() { require.NoError(t, cc.Close()) }()

		ctx, cancel := testhelper.Context()
		defer cancel()

		t.Run("handles registered RPCs", func(t *testing.T) {
			checkOwnRegisteredServices(ctx, t, cc)
		})

		t.Run("proxies RPCs onto gitaly server", func(t *testing.T) {
			checkProxyingOntoGitaly(ctx, t, cc)
		})
	})

	t.Run("stops all listening servers", func(t *testing.T) {
		ctx, cancel := testhelper.Context()
		defer cancel()

		// start with tcp listener
		praefectServerFactory := NewServerFactory(conf, logger, coordinator.StreamDirector, nodeMgr, txMgr, queue, registry)
		defer praefectServerFactory.Stop()

		tcpListener, err := net.Listen(starter.TCP, ":0")
		require.NoError(t, err)
		defer tcpListener.Close()

		go praefectServerFactory.Serve(tcpListener, false)

		praefectTCPAddr, err := starter.ComposeEndpoint(tcpListener.Addr().Network(), tcpListener.Addr().String())
		require.NoError(t, err)

		tcpCC, err := client.Dial(praefectTCPAddr, nil)
		require.NoError(t, err)
		defer func() { require.NoError(t, tcpCC.Close()) }()

		tcpHealthClient := checkOwnRegisteredServices(ctx, t, tcpCC)

		// start with socket listener
		socketPath := testhelper.GetTemporaryGitalySocketFileName()
		defer func() { require.NoError(t, os.RemoveAll(socketPath)) }()
		socketListener, err := net.Listen(starter.Unix, socketPath)
		require.NoError(t, err)
		defer socketListener.Close()

		go praefectServerFactory.Serve(socketListener, false)

		praefectSocketAddr, err := starter.ComposeEndpoint(socketListener.Addr().Network(), socketListener.Addr().String())
		require.NoError(t, err)

		socketCC, err := client.Dial(praefectSocketAddr, nil)
		require.NoError(t, err)
		defer func() { require.NoError(t, socketCC.Close()) }()

		unixHealthClient := checkOwnRegisteredServices(ctx, t, socketCC)

		praefectServerFactory.GracefulStop()

		_, err = tcpHealthClient.Check(ctx, nil)
		require.Error(t, err)

		_, err = unixHealthClient.Check(ctx, nil)
		require.Error(t, err)
	})
}
