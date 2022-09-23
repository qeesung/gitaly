//go:build !gitaly_test_sha256

package client

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentracing/opentracing-go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber/jaeger-client-go"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v15/auth"
	internalclient "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	gitalyx509 "gitlab.com/gitlab-org/gitaly/v15/internal/x509"
	"gitlab.com/gitlab-org/labkit/correlation"
	grpccorrelation "gitlab.com/gitlab-org/labkit/correlation/grpc"
	grpctracing "gitlab.com/gitlab-org/labkit/tracing/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/grpc_testing"
)

var proxyEnvironmentKeys = []string{"http_proxy", "https_proxy", "no_proxy"}

func TestDial(t *testing.T) {
	if emitProxyWarning() {
		t.Log("WARNING. Proxy configuration detected from environment settings. This test failure may be related to proxy configuration. Please process with caution")
	}

	stop, connectionMap := startListeners(t, func(creds credentials.TransportCredentials) *grpc.Server {
		srv := grpc.NewServer(grpc.Creds(creds))
		healthpb.RegisterHealthServer(srv, &healthServer{})
		return srv
	})
	defer stop()

	unixSocketAbsPath := connectionMap["unix"]

	tempDir := testhelper.TempDir(t)

	unixSocketPath := filepath.Join(tempDir, "gitaly.socket")
	require.NoError(t, os.Symlink(unixSocketAbsPath, unixSocketPath))

	tests := []struct {
		name                string
		rawAddress          string
		envSSLCertFile      string
		dialOpts            []grpc.DialOption
		expectDialFailure   bool
		expectHealthFailure bool
	}{
		{
			name:                "tcp localhost with prefix",
			rawAddress:          "tcp://localhost:" + connectionMap["tcp"], // "tcp://localhost:1234"
			expectDialFailure:   false,
			expectHealthFailure: false,
		},
		{
			name:                "tls localhost",
			rawAddress:          "tls://localhost:" + connectionMap["tls"], // "tls://localhost:1234"
			envSSLCertFile:      "./testdata/gitalycert.pem",
			expectDialFailure:   false,
			expectHealthFailure: false,
		},
		{
			name:                "unix absolute",
			rawAddress:          "unix:" + unixSocketAbsPath, // "unix:/tmp/temp-socket"
			expectDialFailure:   false,
			expectHealthFailure: false,
		},
		{
			name:                "unix relative",
			rawAddress:          "unix:" + unixSocketPath, // "unix:../../tmp/temp-socket"
			expectDialFailure:   false,
			expectHealthFailure: false,
		},
		{
			name:                "unix absolute does not exist",
			rawAddress:          "unix:" + unixSocketAbsPath + ".does_not_exist", // "unix:/tmp/temp-socket.does_not_exist"
			expectDialFailure:   false,
			expectHealthFailure: true,
		},
		{
			name:                "unix relative does not exist",
			rawAddress:          "unix:" + unixSocketPath + ".does_not_exist", // "unix:../../tmp/temp-socket.does_not_exist"
			expectDialFailure:   false,
			expectHealthFailure: true,
		},
		{
			// Gitaly does not support connections that do not have a scheme.
			name:              "tcp localhost no prefix",
			rawAddress:        "localhost:" + connectionMap["tcp"], // "localhost:1234"
			expectDialFailure: true,
		},
		{
			name:              "invalid",
			rawAddress:        ".",
			expectDialFailure: true,
		},
		{
			name:              "empty",
			rawAddress:        "",
			expectDialFailure: true,
		},
		{
			name:              "dial fail if there is no listener on address",
			rawAddress:        "tcp://invalid.address",
			dialOpts:          FailOnNonTempDialError(),
			expectDialFailure: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if emitProxyWarning() {
				t.Log("WARNING. Proxy configuration detected from environment settings. This test failure may be related to proxy configuration. Please process with caution")
			}

			if tt.envSSLCertFile != "" {
				t.Setenv(gitalyx509.SSLCertFile, tt.envSSLCertFile)
			}

			ctx := testhelper.Context(t)

			conn, err := Dial(tt.rawAddress, tt.dialOpts)
			if tt.expectDialFailure {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			defer conn.Close()

			_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
			if tt.expectHealthFailure {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestDialSidechannel(t *testing.T) {
	if emitProxyWarning() {
		t.Log("WARNING. Proxy configuration detected from environment settings. This test failure may be related to proxy configuration. Please process with caution")
	}

	stop, connectionMap := startListeners(t, func(creds credentials.TransportCredentials) *grpc.Server {
		return grpc.NewServer(TestSidechannelServer(newLogger(t), creds, func(
			_ interface{},
			stream grpc.ServerStream,
			sidechannelConn io.ReadWriteCloser,
		) error {
			if method, ok := grpc.Method(stream.Context()); !ok || method != "/grpc.health.v1.Health/Check" {
				return fmt.Errorf("unexpected method: %s", method)
			}

			var req healthpb.HealthCheckRequest
			if err := stream.RecvMsg(&req); err != nil {
				return fmt.Errorf("recv msg: %w", err)
			}

			if _, err := io.Copy(sidechannelConn, sidechannelConn); err != nil {
				return fmt.Errorf("copy: %w", err)
			}

			if err := stream.SendMsg(&healthpb.HealthCheckResponse{}); err != nil {
				return fmt.Errorf("send msg: %w", err)
			}

			return nil
		})...)
	})
	defer stop()

	unixSocketAbsPath := connectionMap["unix"]

	tempDir := testhelper.TempDir(t)

	unixSocketPath := filepath.Join(tempDir, "gitaly.socket")
	require.NoError(t, os.Symlink(unixSocketAbsPath, unixSocketPath))

	registry := NewSidechannelRegistry(newLogger(t))

	tests := []struct {
		name           string
		rawAddress     string
		envSSLCertFile string
		dialOpts       []grpc.DialOption
	}{
		{
			name:       "tcp sidechannel",
			rawAddress: "tcp://localhost:" + connectionMap["tcp"], // "tcp://localhost:1234"
		},
		{
			name:           "tls sidechannel",
			rawAddress:     "tls://localhost:" + connectionMap["tls"], // "tls://localhost:1234"
			envSSLCertFile: "./testdata/gitalycert.pem",
		},
		{
			name:       "unix sidechannel",
			rawAddress: "unix:" + unixSocketAbsPath, // "unix:/tmp/temp-socket"
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envSSLCertFile != "" {
				t.Setenv(gitalyx509.SSLCertFile, tt.envSSLCertFile)
			}

			ctx := testhelper.Context(t)

			conn, err := DialSidechannel(ctx, tt.rawAddress, registry, tt.dialOpts)
			require.NoError(t, err)
			defer conn.Close()

			ctx, scw := registry.Register(ctx, func(conn SidechannelConn) error {
				const message = "hello world"
				if _, err := io.WriteString(conn, message); err != nil {
					return err
				}
				if err := conn.CloseWrite(); err != nil {
					return err
				}
				buf, err := io.ReadAll(conn)
				if err != nil {
					return err
				}
				if string(buf) != message {
					return fmt.Errorf("expected %q, got %q", message, buf)
				}

				return nil
			})
			defer testhelper.MustClose(t, scw)

			req := &healthpb.HealthCheckRequest{Service: "test sidechannel"}
			_, err = healthpb.NewHealthClient(conn).Check(ctx, req)
			require.NoError(t, err)
			require.NoError(t, scw.Close())
		})
	}
}

type testSvc struct {
	grpc_testing.UnimplementedTestServiceServer
	unaryCall      func(context.Context, *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error)
	fullDuplexCall func(stream grpc_testing.TestService_FullDuplexCallServer) error
}

func (ts *testSvc) UnaryCall(ctx context.Context, r *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
	if ts.unaryCall != nil {
		return ts.unaryCall(ctx, r)
	}

	return &grpc_testing.SimpleResponse{}, nil
}

func (ts *testSvc) FullDuplexCall(stream grpc_testing.TestService_FullDuplexCallServer) error {
	if ts.fullDuplexCall != nil {
		return ts.fullDuplexCall(stream)
	}

	return nil
}

func TestDial_Correlation(t *testing.T) {
	t.Run("unary", func(t *testing.T) {
		serverSocketPath := testhelper.GetTemporaryGitalySocketFileName(t)

		listener, err := net.Listen("unix", serverSocketPath)
		require.NoError(t, err)

		grpcServer := grpc.NewServer(grpc.UnaryInterceptor(grpccorrelation.UnaryServerCorrelationInterceptor()))
		svc := &testSvc{
			unaryCall: func(ctx context.Context, r *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
				cid := correlation.ExtractFromContext(ctx)
				assert.Equal(t, "correlation-id-1", cid)
				return &grpc_testing.SimpleResponse{}, nil
			},
		}
		grpc_testing.RegisterTestServiceServer(grpcServer, svc)

		go testhelper.MustServe(t, grpcServer, listener)

		defer grpcServer.Stop()
		ctx := testhelper.Context(t)

		cc, err := DialContext(ctx, "unix://"+serverSocketPath, []grpc.DialOption{
			internalclient.UnaryInterceptor(), internalclient.StreamInterceptor(),
		})
		require.NoError(t, err)
		defer cc.Close()

		client := grpc_testing.NewTestServiceClient(cc)

		ctx = correlation.ContextWithCorrelation(ctx, "correlation-id-1")
		_, err = client.UnaryCall(ctx, &grpc_testing.SimpleRequest{})
		require.NoError(t, err)
	})

	t.Run("stream", func(t *testing.T) {
		serverSocketPath := testhelper.GetTemporaryGitalySocketFileName(t)

		listener, err := net.Listen("unix", serverSocketPath)
		require.NoError(t, err)

		grpcServer := grpc.NewServer(grpc.StreamInterceptor(grpccorrelation.StreamServerCorrelationInterceptor()))
		svc := &testSvc{
			fullDuplexCall: func(stream grpc_testing.TestService_FullDuplexCallServer) error {
				cid := correlation.ExtractFromContext(stream.Context())
				assert.Equal(t, "correlation-id-1", cid)
				_, err := stream.Recv()
				assert.NoError(t, err)
				return stream.Send(&grpc_testing.StreamingOutputCallResponse{})
			},
		}
		grpc_testing.RegisterTestServiceServer(grpcServer, svc)

		go testhelper.MustServe(t, grpcServer, listener)
		defer grpcServer.Stop()
		ctx := testhelper.Context(t)

		cc, err := DialContext(ctx, "unix://"+serverSocketPath, []grpc.DialOption{
			internalclient.UnaryInterceptor(), internalclient.StreamInterceptor(),
		})
		require.NoError(t, err)
		defer cc.Close()

		client := grpc_testing.NewTestServiceClient(cc)

		ctx = correlation.ContextWithCorrelation(ctx, "correlation-id-1")
		stream, err := client.FullDuplexCall(ctx)
		require.NoError(t, err)

		require.NoError(t, stream.Send(&grpc_testing.StreamingOutputCallRequest{}))
		require.NoError(t, stream.CloseSend())

		_, err = stream.Recv()
		require.NoError(t, err)
	})
}

func TestDial_Tracing(t *testing.T) {
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName(t)

	listener, err := net.Listen("unix", serverSocketPath)
	require.NoError(t, err)

	clientSendClosed := make(chan struct{})

	// This is our test service. All it does is to create additional spans
	// which should in the end be visible when collecting all registered
	// spans.
	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(grpctracing.UnaryServerTracingInterceptor()),
		grpc.StreamInterceptor(grpctracing.StreamServerTracingInterceptor()),
	)
	svc := &testSvc{
		unaryCall: func(ctx context.Context, r *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
			span, _ := opentracing.StartSpanFromContext(ctx, "nested-span")
			defer span.Finish()
			span.LogKV("was", "called")
			return &grpc_testing.SimpleResponse{}, nil
		},
		fullDuplexCall: func(stream grpc_testing.TestService_FullDuplexCallServer) error {
			// synchronize the client has returned from CloseSend as the client span finishing
			// races with sending the stream close to the server
			select {
			case <-clientSendClosed:
			case <-stream.Context().Done():
				return stream.Context().Err()
			}

			span, _ := opentracing.StartSpanFromContext(stream.Context(), "nested-span")
			defer span.Finish()
			span.LogKV("was", "called")
			return nil
		},
	}
	grpc_testing.RegisterTestServiceServer(grpcServer, svc)

	go testhelper.MustServe(t, grpcServer, listener)
	defer grpcServer.Stop()
	ctx := testhelper.Context(t)

	t.Run("unary", func(t *testing.T) {
		reporter := jaeger.NewInMemoryReporter()
		tracer, tracerCloser := jaeger.NewTracer("", jaeger.NewConstSampler(true), reporter)
		defer tracerCloser.Close()

		defer func(old opentracing.Tracer) { opentracing.SetGlobalTracer(old) }(opentracing.GlobalTracer())
		opentracing.SetGlobalTracer(tracer)

		// This needs to be run after setting up the global tracer as it will cause us to
		// create the span when executing the RPC call further down below.
		cc, err := DialContext(ctx, "unix://"+serverSocketPath, []grpc.DialOption{
			internalclient.UnaryInterceptor(), internalclient.StreamInterceptor(),
		})
		require.NoError(t, err)
		defer cc.Close()

		// We set up a "main" span here, which is going to be what the
		// other spans inherit from. In order to check whether baggage
		// works correctly, we also set up a "stub" baggage item which
		// should be inherited to child contexts.
		span := tracer.StartSpan("unary-check")
		span = span.SetBaggageItem("service", "stub")
		ctx := opentracing.ContextWithSpan(ctx, span)

		// We're now invoking the unary RPC with the span injected into
		// the context. This should create a span that's nested into
		// the "stream-check" span.
		_, err = grpc_testing.NewTestServiceClient(cc).UnaryCall(ctx, &grpc_testing.SimpleRequest{})
		require.NoError(t, err)

		span.Finish()

		spans := reporter.GetSpans()
		require.Len(t, spans, 3)

		for i, expectedSpan := range []struct {
			baggage   string
			operation string
		}{
			// This is the first span we expect, which is the
			// "health" span which we've manually created inside of
			// PingMethod.
			{baggage: "", operation: "nested-span"},
			// This span is the RPC call to TestService/Ping. It
			// inherits the "unary-check" we set up and thus has
			// baggage.
			{baggage: "stub", operation: "/grpc.testing.TestService/UnaryCall"},
			// And this finally is the outermost span which we
			// manually set up before the RPC call.
			{baggage: "stub", operation: "unary-check"},
		} {
			assert.IsType(t, spans[i], &jaeger.Span{})
			span := spans[i].(*jaeger.Span)

			assert.Equal(t, expectedSpan.baggage, span.BaggageItem("service"), "wrong baggage item for span %d", i)
			assert.Equal(t, expectedSpan.operation, span.OperationName(), "wrong operation name for span %d", i)
		}
	})

	t.Run("stream", func(t *testing.T) {
		reporter := jaeger.NewInMemoryReporter()
		tracer, tracerCloser := jaeger.NewTracer("", jaeger.NewConstSampler(true), reporter)
		defer tracerCloser.Close()

		defer func(old opentracing.Tracer) { opentracing.SetGlobalTracer(old) }(opentracing.GlobalTracer())
		opentracing.SetGlobalTracer(tracer)

		// This needs to be run after setting up the global tracer as it will cause us to
		// create the span when executing the RPC call further down below.
		cc, err := DialContext(ctx, "unix://"+serverSocketPath, []grpc.DialOption{
			internalclient.UnaryInterceptor(), internalclient.StreamInterceptor(),
		})
		require.NoError(t, err)
		defer cc.Close()

		// We set up a "main" span here, which is going to be what the other spans inherit
		// from. In order to check whether baggage works correctly, we also set up a "stub"
		// baggage item which should be inherited to child contexts.
		span := tracer.StartSpan("stream-check")
		span = span.SetBaggageItem("service", "stub")
		ctx := opentracing.ContextWithSpan(ctx, span)

		// We're now invoking the streaming RPC with the span injected into the context.
		// This should create a span that's nested into the "stream-check" span.
		stream, err := grpc_testing.NewTestServiceClient(cc).FullDuplexCall(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.CloseSend())
		close(clientSendClosed)

		// wait for the server to finish its spans and close the stream
		resp, err := stream.Recv()
		require.Equal(t, err, io.EOF)
		require.Nil(t, resp)

		span.Finish()

		spans := reporter.GetSpans()
		require.Len(t, spans, 3)

		for i, expectedSpan := range []struct {
			baggage   string
			operation string
		}{
			// This span is the RPC call to TestService/Ping.
			{baggage: "stub", operation: "/grpc.testing.TestService/FullDuplexCall"},
			// This is the second span we expect, which is the "nested-span" span which
			// we've manually created inside of PingMethod. This is different than for
			// unary RPCs: given that one can send multiple messages to the RPC, we may
			// see multiple such "nested-span"s being created. And the PingStream span
			// will only be finalized last.
			{baggage: "", operation: "nested-span"},
			// And this finally is the outermost span which we
			// manually set up before the RPC call.
			{baggage: "stub", operation: "stream-check"},
		} {
			if !assert.IsType(t, spans[i], &jaeger.Span{}) {
				continue
			}

			span := spans[i].(*jaeger.Span)
			assert.Equal(t, expectedSpan.baggage, span.BaggageItem("service"), "wrong baggage item for span %d", i)
			assert.Equal(t, expectedSpan.operation, span.OperationName(), "wrong operation name for span %d", i)
		}
	})
}

// healthServer provide a basic GRPC health service endpoint for testing purposes
type healthServer struct {
	healthpb.UnimplementedHealthServer
}

func (*healthServer) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	return &healthpb.HealthCheckResponse{}, nil
}

// startTCPListener will start a insecure TCP listener on a random unused port
func startTCPListener(tb testing.TB, factory func(credentials.TransportCredentials) *grpc.Server) (func(), string) {
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(tb, err)

	tcpPort := listener.Addr().(*net.TCPAddr).Port
	address := fmt.Sprintf("%d", tcpPort)

	grpcServer := factory(insecure.NewCredentials())
	go testhelper.MustServe(tb, grpcServer, listener)

	return func() {
		grpcServer.Stop()
	}, address
}

// startUnixListener will start a unix socket listener using a temporary file
func startUnixListener(tb testing.TB, factory func(credentials.TransportCredentials) *grpc.Server) (func(), string) {
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName(tb)

	listener, err := net.Listen("unix", serverSocketPath)
	require.NoError(tb, err)

	grpcServer := factory(insecure.NewCredentials())
	go testhelper.MustServe(tb, grpcServer, listener)

	return func() {
		grpcServer.Stop()
	}, serverSocketPath
}

// startTLSListener will start a secure TLS listener on a random unused port
//go:generate openssl req -newkey rsa:4096 -new -nodes -x509 -days 3650 -out testdata/gitalycert.pem -keyout testdata/gitalykey.pem -subj "/C=US/ST=California/L=San Francisco/O=GitLab/OU=GitLab-Shell/CN=localhost" -addext "subjectAltName = IP:127.0.0.1, DNS:localhost"
func startTLSListener(tb testing.TB, factory func(credentials.TransportCredentials) *grpc.Server) (func(), string) {
	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(tb, err)

	tcpPort := listener.Addr().(*net.TCPAddr).Port
	address := fmt.Sprintf("%d", tcpPort)

	cert, err := tls.LoadX509KeyPair("testdata/gitalycert.pem", "testdata/gitalykey.pem")
	require.NoError(tb, err)

	grpcServer := factory(
		credentials.NewTLS(&tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}),
	)
	go testhelper.MustServe(tb, grpcServer, listener)

	return func() {
		grpcServer.Stop()
	}, address
}

var listeners = map[string]func(testing.TB, func(credentials.TransportCredentials) *grpc.Server) (func(), string){
	"tcp":  startTCPListener,
	"unix": startUnixListener,
	"tls":  startTLSListener,
}

// startListeners will start all the different listeners used in this test
func startListeners(tb testing.TB, factory func(credentials.TransportCredentials) *grpc.Server) (func(), map[string]string) {
	var closers []func()
	connectionMap := map[string]string{}
	for k, v := range listeners {
		closer, address := v(tb, factory)
		closers = append(closers, closer)
		connectionMap[k] = address
	}

	return func() {
		for _, v := range closers {
			v()
		}
	}, connectionMap
}

func emitProxyWarning() bool {
	for _, key := range proxyEnvironmentKeys {
		value := os.Getenv(key)
		if value != "" {
			return true
		}
		value = os.Getenv(strings.ToUpper(key))
		if value != "" {
			return true
		}
	}
	return false
}

func TestHealthCheckDialer(t *testing.T) {
	_, addr, cleanup := runServer(t, "token")
	defer cleanup()
	ctx := testhelper.Context(t)

	_, err := HealthCheckDialer(DialContext)(ctx, addr, nil)
	testhelper.RequireGrpcError(t, status.Error(codes.Unauthenticated, "authentication required"), err)

	cc, err := HealthCheckDialer(DialContext)(ctx, addr, []grpc.DialOption{
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2("token")),
		internalclient.UnaryInterceptor(),
		internalclient.StreamInterceptor(),
	})
	require.NoError(t, err)
	require.NoError(t, cc.Close())
}

func newLogger(tb testing.TB) *logrus.Entry {
	return logrus.NewEntry(testhelper.NewDiscardingLogger(tb))
}
