package praefect

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/log"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/conn"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/mock"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/internal/server/auth"
	gitalyserver "gitlab.com/gitlab-org/gitaly/internal/service/server"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// TestServerSimpleUnaryUnary verifies that the Praefect server is capable of
// routing a specific unary request to and unary response from a backend server
func TestServerSimpleUnaryUnary(t *testing.T) {
	testCases := []struct {
		name string

		// callback is the actual RPC implementation
		callback simpleUnaryUnaryCallback

		// all inputs and outputs for RPC SimpleUnaryUnary
		request      *mock.SimpleRequest
		expectResp   *mock.SimpleResponse
		expectErrStr string
	}{
		{
			name:     "simple request with response",
			callback: callbackIncrement,
			request: &mock.SimpleRequest{
				Value: 1,
			},
			expectResp: &mock.SimpleResponse{
				Value: 2,
			},
		},
	}

	gz := proto.FileDescriptor("mock.proto")
	fd, err := protoregistry.ExtractFileDescriptor(gz)
	if err != nil {
		panic(err)
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			const (
				storagePrimary = "default"
			)

			conf := config.Config{
				Nodes: []*models.Node{
					&models.Node{
						ID:             1,
						Storage:        "praefect-internal-1",
						DefaultPrimary: true,
					},
					&models.Node{
						ID:      2,
						Storage: "praefect-internal-2",
					}},
			}

			datastore := NewMemoryDatastore(conf)

			logEntry := log.Default()

			clientCC := conn.NewClientConnections()

			for id, nodeStorage := range datastore.storageNodes.m {
				backend, cleanup := newMockDownstream(t, tt.callback)
				defer cleanup() // clean up mock downstream server resources

				clientCC.RegisterNode(nodeStorage.Storage, backend)
				nodeStorage.Address = backend
				datastore.storageNodes.m[id] = nodeStorage
			}

			coordinator := NewCoordinator(logEntry, datastore, clientCC, fd)

			replmgr := NewReplMgr(
				storagePrimary,
				logEntry,
				datastore,
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
			defer listener.Close()

			errQ := make(chan error)

			go func() {
				errQ <- prf.Start(listener)
			}()

			// dial client to praefect
			cc := dialLocalPort(t, port, false)
			defer cc.Close()
			cli := mock.NewSimpleServiceClient(cc)

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			resp, err := cli.SimpleUnaryUnary(ctx, tt.request)
			if err != nil {
				require.EqualError(t, err, tt.expectErrStr)
			}
			require.Equal(t, tt.expectResp, resp)

			err = prf.Shutdown(ctx)
			require.NoError(t, err)
			require.NoError(t, <-errQ)
		})
	}
}

func TestGitalyServerInfo(t *testing.T) {
	conf := config.Config{
		Nodes: []*models.Node{
			&models.Node{
				ID:             1,
				Storage:        "praefect-internal-1",
				DefaultPrimary: true,
			},
			&models.Node{
				ID:      2,
				Storage: "praefect-internal-2",
			}},
	}
	cc, srv := runFullPraefectServer(t, conf)
	defer srv.s.Stop()

	client := gitalypb.NewServerServiceClient(cc)

	ctx, cancel := testhelper.Context()
	defer cancel()

	metadata, err := client.ServerInfo(ctx, &gitalypb.ServerInfoRequest{})
	require.NoError(t, err)
	require.Len(t, metadata.GetStorageStatuses(), len(conf.Nodes))

	for _, storageStatus := range metadata.GetStorageStatuses() {
		require.NotNil(t, storageStatus, "none of the storage statuses should be nil")
	}
}

func TestHealthCheck(t *testing.T) {
	cc, srv := runFullPraefectServer(t, config.Config{})
	defer srv.s.Stop()

	ctx, cancel := testhelper.Context()
	defer cancel()

	client := healthpb.NewHealthClient(cc)
	_, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
}

func runFullPraefectServer(t *testing.T, conf config.Config) (*grpc.ClientConn, *Server) {
	datastore := NewMemoryDatastore(conf)

	logEntry := log.Default()

	clientCC := conn.NewClientConnections()
	for id, nodeStorage := range datastore.storageNodes.m {
		_, backend := runInternalGitalyServer(t)

		clientCC.RegisterNode(nodeStorage.Storage, backend)
		nodeStorage.Address = backend
		datastore.storageNodes.m[id] = nodeStorage
	}

	coordinator := NewCoordinator(logEntry, datastore, clientCC, protoregistry.GitalyProtoFileDescriptors...)

	replmgr := NewReplMgr(
		"",
		logEntry,
		datastore,
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

	go func() {
		errQ <- prf.Start(listener)
	}()

	// dial client to praefect
	cc := dialLocalPort(t, port, false)

	return cc, prf
}

func runInternalGitalyServer(t *testing.T) (*grpc.Server, string) {
	streamInt := []grpc.StreamServerInterceptor{auth.StreamServerInterceptor()}
	unaryInt := []grpc.UnaryServerInterceptor{auth.UnaryServerInterceptor()}

	server := testhelper.NewTestGrpcServer(t, streamInt, unaryInt)
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()

	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	gitalypb.RegisterServerServiceServer(server, gitalyserver.NewServer())

	go server.Serve(listener)

	return server, "unix://" + serverSocketPath
}

func callbackIncrement(_ context.Context, req *mock.SimpleRequest) (*mock.SimpleResponse, error) {
	return &mock.SimpleResponse{
		Value: req.Value + 1,
	}, nil
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

// initializes and returns a client to downstream server, downstream server, and cleanup function
func newMockDownstream(tb testing.TB, callback simpleUnaryUnaryCallback) (string, func()) {
	// setup mock server
	m := &mockSvc{
		simpleUnaryUnary: callback,
	}

	srv := grpc.NewServer()
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
