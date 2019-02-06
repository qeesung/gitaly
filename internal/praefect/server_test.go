package praefect_test

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/mwitkow/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/praefect"
	"google.golang.org/grpc"
)

func TestServerRouting(t *testing.T) {
	loxSrv := praefect.NewServer(nil, testLogger{t})

	listener, port := listenAvailPort(t)
	t.Logf("proxy listening on port %d", port)
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	errQ := make(chan error)

	go func() {
		errQ <- loxSrv.Start(ctx, listener)
	}()

	cc, err := dialLocalPort(t, port)
	if err != nil {
		t.Fatalf("client unable to dial to local port %d: %s", port, err)
	}
	defer cc.Close()

	mCli, _, cleanup := newMockDownstream(t)
	defer cleanup() // clean up mock downstream server resources

	loxSrv.RegisterNode("test", mCli)

	gCli := gitalypb.NewRepositoryServiceClient(cc)
	resp, err := gCli.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{})
	if err != nil {
		t.Fatalf("unable to invoke RPC on proxy downstream: %s", err)
	}

	t.Logf("CalculateChecksum response: %#v", resp)

	cancel()
	t.Logf("Server teminated: %s", <-errQ)
}

func listenAvailPort(tb testing.TB) (net.Listener, int) {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		tb.Fatalf("unable to listen on available TCP port: %q", err)
	}

	return listener, listener.Addr().(*net.TCPAddr).Port
}

func dialLocalPort(tb testing.TB, port int) (*grpc.ClientConn, error) {
	return client.Dial(
		fmt.Sprintf("tcp://localhost:%d", port),
		[]grpc.DialOption{
			grpc.WithBlock(),
			grpc.WithDefaultCallOptions(grpc.CallCustomCodec(proxy.Codec())),
		},
	)
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

	// set up client to mock server
	cc, err := dialLocalPort(tb, port)
	if err != nil {
		tb.Fatalf("unable to dial to mock downstream: %s", err)
	}

	cleanup := func() {
		lis.Close()
		m.srv.GracefulStop()
		cc.Close()
	}

	errQ := make(chan error)

	go func() {
		errQ <- m.srv.Serve(lis)
	}()

	return cc, m, cleanup
}
