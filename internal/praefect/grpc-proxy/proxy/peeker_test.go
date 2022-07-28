//go:build !gitaly_test_sha256

package proxy_test

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"google.golang.org/grpc/test/grpc_testing"
	"google.golang.org/protobuf/proto"
)

// TestStreamPeeking demonstrates that a director function is able to peek into a stream. Further
// more, it demonstrates that peeking into a stream will not disturb the stream sent from the proxy
// client to the backend.
func TestStreamPeeking(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	backendCC, backendSrvr := newBackendPinger(t, ctx)

	requestSent := &grpc_testing.StreamingOutputCallRequest{
		Payload: &grpc_testing.Payload{
			Body: []byte("hi"),
		},
	}
	responseSent := &grpc_testing.StreamingOutputCallResponse{
		Payload: &grpc_testing.Payload{
			Body: []byte("bye"),
		},
	}

	// We create a director that's peeking into the message in order to assert that the peeked
	// message will still be seen by the client.
	director := func(ctx context.Context, _ string, peeker proxy.StreamPeeker) (*proxy.StreamParameters, error) {
		peekedMessage, err := peeker.Peek()
		require.NoError(t, err)

		var peekedRequest grpc_testing.StreamingOutputCallRequest
		require.NoError(t, proto.Unmarshal(peekedMessage, &peekedRequest))
		testhelper.ProtoEqual(t, requestSent, &peekedRequest)

		return proxy.NewStreamParameters(proxy.Destination{
			Ctx:  metadata.IncomingToOutgoing(ctx),
			Conn: backendCC,
			Msg:  peekedMessage,
		}, nil, nil, nil), nil
	}

	// The backend is supposed to still receive the message as expected without any modification
	// to it.
	backendSrvr.fullDuplexCall = func(stream grpc_testing.TestService_FullDuplexCallServer) error {
		requestReceived, err := stream.Recv()
		require.NoError(t, err)
		testhelper.ProtoEqual(t, requestSent, requestReceived)

		return stream.Send(responseSent)
	}

	proxyConn := newProxy(t, ctx, director, "grpc_testing.TestService", "FullDuplexCall")
	proxyClient := grpc_testing.NewTestServiceClient(proxyConn)

	// Send the request on the stream and close the writing side.
	proxyStream, err := proxyClient.FullDuplexCall(ctx)
	require.NoError(t, err)
	require.NoError(t, proxyStream.Send(requestSent))
	require.NoError(t, proxyStream.CloseSend())

	// And now verify that the response we've got in fact matches our expected response.
	responseReceived, err := proxyStream.Recv()
	require.NoError(t, err)
	testhelper.ProtoEqual(t, responseReceived, responseSent)

	_, err = proxyStream.Recv()
	require.Equal(t, io.EOF, err)
}

func TestStreamInjecting(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	backendCC, backendSrvr := newBackendPinger(t, ctx)

	requestSent := &grpc_testing.StreamingOutputCallRequest{
		Payload: &grpc_testing.Payload{
			Body: []byte("hi"),
		},
	}
	requestReplaced := &grpc_testing.StreamingOutputCallRequest{
		Payload: &grpc_testing.Payload{
			Body: []byte("replaced"),
		},
	}
	responseSent := &grpc_testing.StreamingOutputCallResponse{
		Payload: &grpc_testing.Payload{
			Body: []byte("bye"),
		},
	}

	// We create a director that peeks the incoming request and in fact changes its values. This
	// is to assert that the client receives the changed requests.
	director := func(ctx context.Context, fullMethodName string, peeker proxy.StreamPeeker) (*proxy.StreamParameters, error) {
		peekedMessage, err := peeker.Peek()
		require.NoError(t, err)

		// Assert that we get the expected original ping request.
		var peekedRequest grpc_testing.StreamingOutputCallRequest
		require.NoError(t, proto.Unmarshal(peekedMessage, &peekedRequest))
		testhelper.ProtoEqual(t, requestSent, &peekedRequest)

		// Replace the value of the peeked request and send along the changed request.
		replacedMessage, err := proto.Marshal(requestReplaced)
		require.NoError(t, err)

		return proxy.NewStreamParameters(proxy.Destination{
			Ctx:  metadata.IncomingToOutgoing(ctx),
			Conn: backendCC,
			Msg:  replacedMessage,
		}, nil, nil, nil), nil
	}

	// Upon receiving the request the backend server should only ever see the changed request.
	backendSrvr.fullDuplexCall = func(stream grpc_testing.TestService_FullDuplexCallServer) error {
		requestReceived, err := stream.Recv()
		require.NoError(t, err)
		testhelper.ProtoEqual(t, requestReplaced, requestReceived)

		return stream.Send(responseSent)
	}

	proxyConn := newProxy(t, ctx, director, "grpc_testing.TestService", "FullDuplexCall")
	proxyClient := grpc_testing.NewTestServiceClient(proxyConn)

	proxyStream, err := proxyClient.FullDuplexCall(ctx)
	require.NoError(t, err)
	defer func() {
		require.NoError(t, proxyStream.CloseSend())
	}()
	require.NoError(t, proxyStream.Send(requestSent))

	responseReceived, err := proxyStream.Recv()
	require.NoError(t, err)
	testhelper.ProtoEqual(t, responseSent, responseReceived)

	_, err = proxyStream.Recv()
	require.Equal(t, io.EOF, err)
}
