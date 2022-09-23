//go:build !gitaly_test_sha256

package proxy_test

import (
	"context"
	"net"
	"testing"

	grpcmw "github.com/grpc-ecosystem/go-grpc-middleware"
	grpcmwtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/fieldextractors"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/sentryhandler"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/grpc_testing"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func newListener(tb testing.TB) net.Listener {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(tb, err, "must be able to allocate a port for listener")

	return listener
}

func newBackendPinger(tb testing.TB, ctx context.Context) (*grpc.ClientConn, *interceptPinger) {
	ip := &interceptPinger{}

	srvr := grpc.NewServer()
	listener := newListener(tb)

	grpc_testing.RegisterTestServiceServer(srvr, ip)

	done := make(chan struct{})
	go func() {
		defer close(done)
		require.NoError(tb, srvr.Serve(listener))
	}()

	cc, err := grpc.DialContext(
		ctx,
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithDefaultCallOptions(
			grpc.ForceCodec(proxy.NewCodec()),
		),
	)
	require.NoError(tb, err)

	tb.Cleanup(func() {
		srvr.GracefulStop()
		require.NoError(tb, cc.Close())
		<-done
	})

	return cc, ip
}

func newProxy(tb testing.TB, ctx context.Context, director proxy.StreamDirector, svc, method string) *grpc.ClientConn {
	proxySrvr := grpc.NewServer(
		grpc.ForceServerCodec(proxy.NewCodec()),
		grpc.StreamInterceptor(
			grpcmw.ChainStreamServer(
				// context tags usage is required by sentryhandler.StreamLogHandler
				grpcmwtags.StreamServerInterceptor(grpcmwtags.WithFieldExtractorForInitialReq(fieldextractors.FieldExtractor)),
				// sentry middleware to capture errors
				sentryhandler.StreamLogHandler,
			),
		),
		grpc.UnknownServiceHandler(proxy.TransparentHandler(director)),
	)
	proxy.RegisterService(proxySrvr, director, svc, method)

	done := make(chan struct{})
	listener := newListener(tb)
	go func() {
		defer close(done)
		require.NoError(tb, proxySrvr.Serve(listener))
	}()

	proxyCC, err := grpc.DialContext(
		ctx,
		listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(tb, err)

	tb.Cleanup(func() {
		proxySrvr.GracefulStop()
		require.NoError(tb, proxyCC.Close())
		<-done
	})

	return proxyCC
}

// interceptPinger allows an RPC to be intercepted with a custom
// function defined in each unit test
type interceptPinger struct {
	grpc_testing.UnimplementedTestServiceServer

	fullDuplexCall      func(grpc_testing.TestService_FullDuplexCallServer) error
	emptyCall           func(context.Context, *grpc_testing.Empty) (*grpc_testing.Empty, error)
	unaryCall           func(context.Context, *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error)
	streamingOutputCall func(*grpc_testing.StreamingOutputCallRequest, grpc_testing.TestService_StreamingOutputCallServer) error
}

func (ip *interceptPinger) FullDuplexCall(stream grpc_testing.TestService_FullDuplexCallServer) error {
	return ip.fullDuplexCall(stream)
}

func (ip *interceptPinger) EmptyCall(ctx context.Context, req *grpc_testing.Empty) (*grpc_testing.Empty, error) {
	return ip.emptyCall(ctx, req)
}

func (ip *interceptPinger) UnaryCall(ctx context.Context, req *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
	return ip.unaryCall(ctx, req)
}

func (ip *interceptPinger) StreamingOutputCall(req *grpc_testing.StreamingOutputCallRequest, stream grpc_testing.TestService_StreamingOutputCallServer) error {
	return ip.streamingOutputCall(req, stream)
}
