//go:build !gitaly_test_sha256

// Copyright 2017 Michal Witkowski. All Rights Reserved.
// See LICENSE for licensing terms.

package proxy_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpc_metadata "google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/grpc_testing"
)

const (
	rejectingMdKey = "test-reject-rpc-if-in-context"
)

func TestHandler_carriesClientMetadata(t *testing.T) {
	t.Parallel()
	testHandlerCarriesClientMetadata(t)
}

func TestHandler_carriesClientMetadataStressTest(t *testing.T) {
	t.Parallel()

	for i := 0; i < 50; i++ {
		testHandlerCarriesClientMetadata(t)
	}
}

func testHandlerCarriesClientMetadata(t *testing.T) {
	ctx, client, backend := setupProxy(t)

	backend.unaryCall = func(ctx context.Context, request *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
		metadata, ok := grpc_metadata.FromIncomingContext(ctx)
		require.True(t, ok)

		metadataValue, ok := metadata["injected_metadata"]
		require.True(t, ok)
		require.Equal(t, []string{"injected_value"}, metadataValue)

		return &grpc_testing.SimpleResponse{Payload: request.GetPayload()}, nil
	}

	ctx = grpc_metadata.NewOutgoingContext(ctx, grpc_metadata.Pairs("injected_metadata", "injected_value"))

	response, err := client.UnaryCall(ctx, &grpc_testing.SimpleRequest{
		Payload: &grpc_testing.Payload{Body: []byte("data")},
	})
	require.NoError(t, err, "PingEmpty should succeed without errors")
	testhelper.ProtoEqual(t, &grpc_testing.SimpleResponse{
		Payload: &grpc_testing.Payload{Body: []byte("data")},
	}, response)
}

func TestHandler_carriesHeadersAndTrailers(t *testing.T) {
	t.Parallel()

	ctx, client, backend := setupProxy(t)

	backend.unaryCall = func(ctx context.Context, request *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
		require.NoError(t, grpc.SendHeader(ctx, grpc_metadata.Pairs("injected_header", "header_value")))
		require.NoError(t, grpc.SetTrailer(ctx, grpc_metadata.Pairs("injected_trailer", "trailer_value")))
		return &grpc_testing.SimpleResponse{Payload: request.GetPayload()}, nil
	}

	var headerMetadata, trailerMetadata grpc_metadata.MD

	response, err := client.UnaryCall(ctx, &grpc_testing.SimpleRequest{
		Payload: &grpc_testing.Payload{Body: []byte("data")},
	}, grpc.Header(&headerMetadata), grpc.Trailer(&trailerMetadata))
	require.NoError(t, err)
	testhelper.ProtoEqual(t, &grpc_testing.SimpleResponse{
		Payload: &grpc_testing.Payload{Body: []byte("data")},
	}, response)

	require.Equal(t, grpc_metadata.Pairs(
		"content-type", "application/grpc",
		"injected_header", "header_value",
	), headerMetadata)
	require.Equal(t, grpc_metadata.Pairs(
		"injected_trailer", "trailer_value",
	), trailerMetadata)
}

func TestHandler_propagatesServerError(t *testing.T) {
	ctx, client, backend := setupProxy(t)

	backend.unaryCall = func(context.Context, *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
		return nil, status.Errorf(codes.ResourceExhausted, "service error")
	}

	sentryTriggered := 0
	sentrySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sentryTriggered++
	}))
	defer sentrySrv.Close()

	// minimal required sentry client configuration
	sentryURL, err := url.Parse(sentrySrv.URL)
	require.NoError(t, err)
	sentryURL.User = url.UserPassword("stub", "stub")
	sentryURL.Path = "/stub/1"

	require.NoError(t, sentry.Init(sentry.ClientOptions{
		Dsn:       sentryURL.String(),
		Transport: sentry.NewHTTPSyncTransport(),
	}))

	// Verify that Sentry is configured correctyl to be triggered.
	sentry.CaptureEvent(sentry.NewEvent())
	require.Equal(t, 1, sentryTriggered)

	_, err = client.UnaryCall(ctx, &grpc_testing.SimpleRequest{})
	testhelper.RequireGrpcError(t, status.Errorf(codes.ResourceExhausted, "service error"), err)

	// Sentry must not be triggered because errors from remote must be just propagated.
	require.Equal(t, 1, sentryTriggered)
}

func TestHandler_directorErrorIsPropagated(t *testing.T) {
	t.Parallel()

	// There is no need to set up the backend given that we should reject the call before we
	// even hit the server.
	ctx, client, _ := setupProxy(t)

	// The proxy's director is set up so that it is rejecting requests when it sees the
	// following gRPC metadata.
	ctx = grpc_metadata.NewOutgoingContext(ctx, grpc_metadata.Pairs(rejectingMdKey, "true"))

	_, err := client.UnaryCall(ctx, &grpc_testing.SimpleRequest{})
	require.Error(t, err)
	testhelper.RequireGrpcError(t, helper.ErrPermissionDeniedf("testing rejection"), err)
}

func TestHandler_fullDuplex(t *testing.T) {
	t.Parallel()
	testHandlerFullDuplex(t)
}

func TestHandler_fullDuplexStressTest(t *testing.T) {
	t.Parallel()

	for i := 0; i < 50; i++ {
		testHandlerFullDuplex(t)
	}
}

func testHandlerFullDuplex(t *testing.T) {
	ctx, client, backend := setupProxy(t)

	backend.fullDuplexCall = func(stream grpc_testing.TestService_FullDuplexCallServer) error {
		require.NoError(t, stream.SendHeader(grpc_metadata.Pairs("custom_header", "header_value")))

		for i := 0; ; i++ {
			request, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)

			require.NoError(t, stream.Send(&grpc_testing.StreamingOutputCallResponse{
				Payload: &grpc_testing.Payload{
					Body: []byte(fmt.Sprintf("%s: %d", request.GetPayload().GetBody(), i)),
				},
			}))
		}

		stream.SetTrailer(grpc_metadata.Pairs("custom_trailer", "trailer_value"))
		return nil
	}

	stream, err := client.FullDuplexCall(ctx)
	require.NoError(t, err)

	for i := 0; i < 20; i++ {
		require.NoError(t, stream.Send(&grpc_testing.StreamingOutputCallRequest{
			Payload: &grpc_testing.Payload{
				Body: []byte(fmt.Sprintf("foo:%d", i)),
			},
		}))

		response, err := stream.Recv()
		require.NoError(t, err)
		testhelper.ProtoEqual(t, &grpc_testing.StreamingOutputCallResponse{
			Payload: &grpc_testing.Payload{
				Body: []byte(fmt.Sprintf("foo:%d: %d", i, i)),
			},
		}, response)

		if i == 0 {
			headerMetadata, err := stream.Header()
			require.NoError(t, err)
			require.Equal(t, grpc_metadata.Pairs(
				"content-type", "application/grpc",
				"custom_header", "header_value",
			), headerMetadata)
		}
	}

	require.NoError(t, stream.CloseSend())
	_, err = stream.Recv()
	require.Equal(t, io.EOF, err)

	require.Equal(t, grpc_metadata.Pairs(
		"custom_trailer", "trailer_value",
	), stream.Trailer())
}

func setupProxy(t *testing.T) (context.Context, grpc_testing.TestServiceClient, *interceptPinger) {
	t.Helper()

	ctx := testhelper.Context(t)

	proxy2Server, backend := newBackendPinger(t, ctx)

	director := func(ctx context.Context, _ string, peeker proxy.StreamPeeker) (*proxy.StreamParameters, error) {
		payload, err := peeker.Peek()
		if err != nil {
			return nil, err
		}

		md, ok := grpc_metadata.FromIncomingContext(ctx)
		if ok {
			if _, exists := md[rejectingMdKey]; exists {
				return proxy.NewStreamParameters(proxy.Destination{Ctx: metadata.IncomingToOutgoing(ctx), Msg: payload}, nil, nil, nil), status.Errorf(codes.PermissionDenied, "testing rejection")
			}
		}

		// Explicitly copy the metadata, otherwise the tests will fail.
		return proxy.NewStreamParameters(proxy.Destination{
			Ctx:  metadata.IncomingToOutgoing(ctx),
			Conn: proxy2Server,
			Msg:  payload,
		}, nil, nil, nil), nil
	}

	client2Proxy := newProxy(t, ctx, director, "mwitkow.testproto.TestService", "Ping")

	return ctx, grpc_testing.NewTestServiceClient(client2Proxy), backend
}

func TestProxyErrorPropagation(t *testing.T) {
	t.Parallel()

	errBackend := status.Error(codes.InvalidArgument, "backend error")
	errDirector := status.Error(codes.FailedPrecondition, "director error")
	errRequestFinalizer := status.Error(codes.Internal, "request finalizer error")

	for _, tc := range []struct {
		desc                  string
		backendError          error
		directorError         error
		requestFinalizerError error
		returnedError         error
		errHandler            func(error) error
	}{
		{
			desc:          "backend error is propagated",
			backendError:  errBackend,
			returnedError: errBackend,
		},
		{
			desc:          "director error is propagated",
			directorError: errDirector,
			returnedError: errDirector,
		},
		{
			desc:                  "request finalizer error is propagated",
			requestFinalizerError: errRequestFinalizer,
			returnedError:         errRequestFinalizer,
		},
		{
			desc:                  "director error cancels proxying",
			backendError:          errBackend,
			requestFinalizerError: errRequestFinalizer,
			directorError:         errDirector,
			returnedError:         errDirector,
		},
		{
			desc:                  "backend error prioritized over request finalizer error",
			backendError:          errBackend,
			requestFinalizerError: errRequestFinalizer,
			returnedError:         errBackend,
		},
		{
			desc:                  "err handler gets error",
			backendError:          errBackend,
			requestFinalizerError: errRequestFinalizer,
			returnedError:         errBackend,
			errHandler: func(err error) error {
				testhelper.RequireGrpcError(t, errBackend, err)
				return errBackend
			},
		},
		{
			desc:          "err handler can swallow error",
			backendError:  errBackend,
			returnedError: io.EOF,
			errHandler: func(err error) error {
				testhelper.RequireGrpcError(t, errBackend, err)
				return nil
			},
		},
		{
			desc:                  "swallowed error surfaces request finalizer error",
			backendError:          errBackend,
			requestFinalizerError: errRequestFinalizer,
			returnedError:         errRequestFinalizer,
			errHandler: func(err error) error {
				testhelper.RequireGrpcError(t, errBackend, err)
				return nil
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tmpDir := testhelper.TempDir(t)

			backendListener, err := net.Listen("unix", filepath.Join(tmpDir, "backend"))
			require.NoError(t, err)

			backendServer := grpc.NewServer(grpc.UnknownServiceHandler(func(interface{}, grpc.ServerStream) error {
				return tc.backendError
			}))
			go testhelper.MustServe(t, backendServer, backendListener)
			defer backendServer.Stop()
			ctx := testhelper.Context(t)

			backendClientConn, err := grpc.DialContext(ctx, "unix://"+backendListener.Addr().String(),
				grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.ForceCodec(proxy.NewCodec())))
			require.NoError(t, err)
			defer func() {
				require.NoError(t, backendClientConn.Close())
			}()

			proxyListener, err := net.Listen("unix", filepath.Join(tmpDir, "proxy"))
			require.NoError(t, err)

			proxyServer := grpc.NewServer(
				grpc.ForceServerCodec(proxy.NewCodec()),
				grpc.UnknownServiceHandler(proxy.TransparentHandler(func(ctx context.Context, fullMethodName string, peeker proxy.StreamPeeker) (*proxy.StreamParameters, error) {
					return proxy.NewStreamParameters(
						proxy.Destination{
							Ctx:        ctx,
							Conn:       backendClientConn,
							ErrHandler: tc.errHandler,
						},
						nil,
						func() error { return tc.requestFinalizerError },
						nil,
					), tc.directorError
				})),
			)

			go testhelper.MustServe(t, proxyServer, proxyListener)
			defer proxyServer.Stop()

			proxyClientConn, err := grpc.DialContext(ctx, "unix://"+proxyListener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			defer func() {
				require.NoError(t, proxyClientConn.Close())
			}()

			resp, err := grpc_testing.NewTestServiceClient(proxyClientConn).UnaryCall(ctx, &grpc_testing.SimpleRequest{})
			testhelper.RequireGrpcError(t, tc.returnedError, err)
			require.Nil(t, resp)
		})
	}
}

func TestRegisterStreamHandlers(t *testing.T) {
	t.Parallel()

	directorCalledError := errors.New("director was called")

	requestSent := &grpc_testing.SimpleRequest{
		Payload: &grpc_testing.Payload{
			Body: []byte("hello"),
		},
	}

	unaryCallStreamHandler := func(t *testing.T, srv interface{}, stream grpc.ServerStream) {
		var request grpc_testing.SimpleRequest
		require.NoError(t, stream.RecvMsg(&request))
		testhelper.ProtoEqual(t, requestSent, &request)
		require.NoError(t, stream.SendMsg(nil))
	}

	emptyCallStreamHandler := func(t *testing.T, srv interface{}, stream grpc.ServerStream) {
		var request grpc_testing.Empty
		require.NoError(t, stream.RecvMsg(&request))
		require.NoError(t, stream.SendMsg(nil))
	}

	for _, tc := range []struct {
		desc               string
		registeredHandlers map[string]func(*testing.T, interface{}, grpc.ServerStream)
		execute            func(context.Context, *testing.T, grpc_testing.TestServiceClient)
		expectedErr        error
		expectedCalls      map[string]int
	}{
		{
			desc: "single handler",
			registeredHandlers: map[string]func(*testing.T, interface{}, grpc.ServerStream){
				"UnaryCall": unaryCallStreamHandler,
			},
			execute: func(ctx context.Context, t *testing.T, client grpc_testing.TestServiceClient) {
				_, err := client.UnaryCall(ctx, requestSent)
				require.NoError(t, err)
			},
			expectedCalls: map[string]int{
				"UnaryCall": 1,
			},
		},
		{
			desc: "multiple handlers picks the right one",
			registeredHandlers: map[string]func(*testing.T, interface{}, grpc.ServerStream){
				"UnaryCall": unaryCallStreamHandler,
				"EmptyCall": emptyCallStreamHandler,
			},
			execute: func(ctx context.Context, t *testing.T, client grpc_testing.TestServiceClient) {
				_, err := client.EmptyCall(ctx, &grpc_testing.Empty{})
				require.NoError(t, err)
			},
			expectedCalls: map[string]int{
				"EmptyCall": 1,
			},
		},
		{
			desc: "call to unregistered handler",
			registeredHandlers: map[string]func(*testing.T, interface{}, grpc.ServerStream){
				"EmptyCall": emptyCallStreamHandler,
			},
			execute: func(ctx context.Context, t *testing.T, client grpc_testing.TestServiceClient) {
				_, err := client.UnaryCall(ctx, requestSent)
				testhelper.RequireGrpcError(t, directorCalledError, err)
			},
			expectedCalls: map[string]int{},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			server := grpc.NewServer(
				grpc.ForceServerCodec(proxy.NewCodec()),
				grpc.UnknownServiceHandler(proxy.TransparentHandler(
					func(ctx context.Context, fullMethodName string, peeker proxy.StreamPeeker) (*proxy.StreamParameters, error) {
						return nil, directorCalledError
					},
				)),
			)

			calls := map[string]int{}
			registeredHandlers := map[string]grpc.StreamHandler{}
			for name, handler := range tc.registeredHandlers {
				name, handler := name, handler

				// We wrap every handler so that we can easily count the number of
				// times each of them has been invoked.
				registeredHandlers[name] = func(srv interface{}, stream grpc.ServerStream) error {
					calls[name]++
					handler(t, srv, stream)
					return nil
				}
			}
			proxy.RegisterStreamHandlers(server, grpc_testing.TestService_ServiceDesc.ServiceName, registeredHandlers)

			listener := newListener(t)
			go testhelper.MustServe(t, server, listener)
			defer server.Stop()

			conn, err := client.Dial("tcp://"+listener.Addr().String(), []grpc.DialOption{grpc.WithBlock()})
			require.NoError(t, err)
			defer conn.Close()
			client := grpc_testing.NewTestServiceClient(conn)

			tc.execute(ctx, t, client)

			require.Equal(t, tc.expectedCalls, calls)
		})
	}
}
