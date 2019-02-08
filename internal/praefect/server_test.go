package praefect_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/mwitkow/grpc-proxy/proxy"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/praefect"
	"google.golang.org/grpc"
)

func TestServerRouting(t *testing.T) {
	prf := praefect.NewServer(nil, testLogger{t})

	listener, port := listenAvailPort(t)
	t.Logf("proxy listening on port %d", port)
	defer listener.Close()

	errQ := make(chan error)

	go func() {
		errQ <- prf.Start(listener)
	}()

	// dial client to proxy
	cc := dialLocalPort(t, port, false)
	defer cc.Close()
	gCli := gitalypb.NewRepositoryServiceClient(cc)

	mCli, _, cleanup := newMockDownstream(t)
	defer cleanup() // clean up mock downstream server resources

	prf.RegisterNode("test", mCli)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	_, err := gCli.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{})
	require.NoError(t, err)

	err = prf.Shutdown(ctx)
	require.NoError(t, err)
	require.NoError(t, <-errQ)
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

type testLogger struct {
	testing.TB
}

func (tl testLogger) Debugf(format string, args ...interface{}) {
	tl.TB.Logf(format, args...)
}

// initializes and returns a client to downstream server, downstream server, and cleanup function
func newMockDownstream(tb testing.TB) (*grpc.ClientConn, gitalypb.RepositoryServiceServer, func()) {
	// setup mock server
	m := &mockRepoSvc{
		srv: grpc.NewServer(),
	}
	gitalypb.RegisterRepositoryServiceServer(m.srv, m)
	lis, port := listenAvailPort(tb)

	// dial praefect to backend service
	cc := dialLocalPort(tb, port, true)

	errQ := make(chan error)

	go func() {
		errQ <- m.srv.Serve(lis)
	}()

	cleanup := func() {
		m.srv.GracefulStop()
		lis.Close()
		cc.Close()
		require.NoError(tb, <-errQ)
	}

	return cc, m, cleanup
}
