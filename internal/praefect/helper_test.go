package praefect

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/golang/protobuf/protoc-gen-go/descriptor"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/client"
	internalauth "gitlab.com/gitlab-org/gitaly/internal/config/auth"
	"gitlab.com/gitlab-org/gitaly/internal/log"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/conn"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/mock"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/server/auth"
	"gitlab.com/gitlab-org/gitaly/internal/service/repository"
	gitalyserver "gitlab.com/gitlab-org/gitaly/internal/service/server"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
)

func waitUntil(t *testing.T, ch <-chan struct{}, timeout time.Duration) {
	select {
	case <-ch:
		break
	case <-time.After(timeout):
		t.Errorf("timed out waiting for channel after %s", timeout)
	}
}

// generates a praefect configuration with the specified number of backend
// nodes
func testConfig(backends int) config.Config {
	cfg := config.Config{
		VirtualStorageName: "praefect",
	}

	var nodes []*models.Node

	for i := 0; i < backends; i++ {
		n := &models.Node{
			ID:      i,
			Storage: fmt.Sprintf("praefect-internal-%d", i),
			Token:   fmt.Sprintf("%d", i),
		}

		if i == 0 {
			n.DefaultPrimary = true
		}

		nodes = append(nodes, n)
	}

	cfg.Nodes = nodes

	return cfg
}

// setupServer wires all praefect dependencies together via dependency
// injection
func setupServer(t testing.TB, conf config.Config, clientCC *conn.ClientConnections, l *logrus.Entry, fds []*descriptor.FileDescriptorProto) (*datastore.MemoryDatastore, *Server) {
	var (
		ds          = datastore.NewInMemory(conf)
		coordinator = NewCoordinator(l, ds, clientCC, conf, fds...)
	)

	var defaultNode *models.Node
	for _, n := range conf.Nodes {
		if n.DefaultPrimary {
			defaultNode = n
		}
	}
	require.NotNil(t, defaultNode)

	replmgr := NewReplMgr(
		defaultNode.Storage,
		l,
		ds,
		clientCC,
	)
	server := NewServer(
		coordinator,
		replmgr,
		nil,
		l,
		clientCC,
		conf,
	)

	return ds, server
}

// runPraefectServer runs a praefect server with the provided mock servers.
// Each mock server is keyed by the corresponding index of the node in the
// config.Nodes. There must be a 1-to-1 mapping between backend server and
// configured storage node.
func runPraefectServerWithMock(t *testing.T, conf config.Config, backends map[int]mock.SimpleServiceServer) (mock.SimpleServiceClient, *Server, testhelper.Cleanup) {
	clientCC := conn.NewClientConnections()
	var cleanups []testhelper.Cleanup

	for i, node := range conf.Nodes {
		backend, ok := backends[i]
		require.True(t, ok, "missing backend server for node %d", i)

		backendAddr, cleanup := newMockDownstream(t, node.Token, backend)
		cleanups = append(cleanups, cleanup)

		clientCC.RegisterNode(node.Storage, backendAddr, node.Token)
		node.Address = backendAddr
		conf.Nodes[i] = node
	}

	_, prf := setupServer(t, conf, clientCC, log.Default(), []*descriptor.FileDescriptorProto{mustLoadProtoReg(t)})

	require.Equal(t, len(backends), len(conf.Nodes),
		"mock server count doesn't match config nodes")

	listener, port := listenAvailPort(t)
	t.Logf("praefect listening on port %d", port)

	errQ := make(chan error)

	go func() {
		errQ <- prf.Start(listener)
	}()

	// dial client to praefect
	cc := dialLocalPort(t, port, false)

	cleanup := func() {
		for _, cu := range cleanups {
			cu()
		}
		require.NoError(t, cc.Close())
		require.NoError(t, listener.Close())

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		require.NoError(t, prf.Shutdown(ctx))
	}

	return mock.NewSimpleServiceClient(cc), prf, cleanup
}

// runPraefectServerWithGitaly runs a praefect server with actual Gitaly nodes
func runPraefectServerWithGitaly(t *testing.T, conf config.Config) (*grpc.ClientConn, *Server, testhelper.Cleanup) {
	clientCC := conn.NewClientConnections()
	var cleanups []testhelper.Cleanup

	for i, node := range conf.Nodes {
		_, backendAddr, cleanup := runInternalGitalyServer(t, node.Token)
		cleanups = append(cleanups, cleanup)

		clientCC.RegisterNode(node.Storage, backendAddr, node.Token)
		node.Address = backendAddr
		conf.Nodes[i] = node
	}

	ds := datastore.NewInMemory(conf)
	logEntry := log.Default()

	coordinator := NewCoordinator(logEntry, ds, clientCC, conf, protoregistry.GitalyProtoFileDescriptors...)

	replmgr := NewReplMgr(
		"",
		logEntry,
		ds,
		clientCC,
	)

	prf := NewServer(
		coordinator,
		replmgr,
		nil,
		logEntry,
		clientCC,
		conf,
	)

	listener, port := listenAvailPort(t)
	t.Logf("proxy listening on port %d", port)

	errQ := make(chan error)
	ctx, cancel := testhelper.Context()

	go func() { errQ <- prf.Start(listener) }()
	go func() { errQ <- replmgr.ProcessBacklog(ctx) }()

	// dial client to praefect
	cc := dialLocalPort(t, port, false)

	cleanup := func() {
		for _, cu := range cleanups {
			cu()
		}

		ctx, _ := context.WithTimeout(ctx, time.Second)
		require.NoError(t, prf.Shutdown(ctx))
		require.NoError(t, <-errQ)

		cancel()
		require.Error(t, context.Canceled, <-errQ)
	}

	return cc, prf, cleanup
}

func runInternalGitalyServer(t *testing.T, token string) (*grpc.Server, string, func()) {
	streamInt := []grpc.StreamServerInterceptor{auth.StreamServerInterceptor(internalauth.Config{Token: token})}
	unaryInt := []grpc.UnaryServerInterceptor{auth.UnaryServerInterceptor(internalauth.Config{Token: token})}

	server := testhelper.NewTestGrpcServer(t, streamInt, unaryInt)
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()

	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	rubyServer := &rubyserver.Server{}
	require.NoError(t, rubyServer.Start())

	gitalypb.RegisterServerServiceServer(server, gitalyserver.NewServer())
	gitalypb.RegisterRepositoryServiceServer(server, repository.NewServer(rubyServer))

	errQ := make(chan error)

	go func() {
		errQ <- server.Serve(listener)
	}()

	cleanup := func() {
		rubyServer.Stop()
		server.Stop()
		require.NoError(t, <-errQ)
	}

	return server, "unix://" + serverSocketPath, cleanup
}

func mustLoadProtoReg(t testing.TB) *descriptor.FileDescriptorProto {
	gz, _ := (*mock.SimpleRequest)(nil).Descriptor()
	fd, err := protoregistry.ExtractFileDescriptor(gz)
	require.NoError(t, err)
	return fd
}

func listenAvailPort(tb testing.TB) (net.Listener, int) {
	listener, err := net.Listen("tcp", ":0")
	require.NoError(tb, err)

	return listener, listener.Addr().(*net.TCPAddr).Port
}

func dialLocalPort(tb testing.TB, port int, backend bool) *grpc.ClientConn {
	opts := []grpc.DialOption{
		grpc.WithBlock(),
	}
	if backend {
		opts = append(
			opts,
			grpc.WithDefaultCallOptions(grpc.CallCustomCodec(proxy.Codec())),
		)
	}

	cc, err := client.Dial(
		fmt.Sprintf("tcp://localhost:%d", port),
		opts,
	)
	require.NoError(tb, err)

	return cc
}

func newMockDownstream(tb testing.TB, token string, m mock.SimpleServiceServer) (string, func()) {
	srv := grpc.NewServer(grpc.UnaryInterceptor(auth.UnaryServerInterceptor(internalauth.Config{Token: token})))
	mock.RegisterSimpleServiceServer(srv, m)

	// client to backend service
	lis, port := listenAvailPort(tb)

	errQ := make(chan error)

	go func() {
		errQ <- srv.Serve(lis)
	}()

	cleanup := func() {
		srv.GracefulStop()
		lis.Close()

		// If the server is shutdown before Serve() is called on it
		// the Serve() calls will return the ErrServerStopped
		if err := <-errQ; err != nil && err != grpc.ErrServerStopped {
			require.NoError(tb, err)
		}
	}

	return fmt.Sprintf("tcp://localhost:%d", port), cleanup
}
