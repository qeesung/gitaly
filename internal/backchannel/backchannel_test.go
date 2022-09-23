//go:build !gitaly_test_sha256

package backchannel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/listenmux"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

type mockTransactionServer struct {
	voteTransactionFunc func(context.Context, *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error)
	*gitalypb.UnimplementedRefTransactionServer
}

func (m mockTransactionServer) VoteTransaction(ctx context.Context, req *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
	return m.voteTransactionFunc(ctx, req)
}

func newLogger() *logrus.Entry {
	logger := logrus.New()
	logger.Out = io.Discard
	return logrus.NewEntry(logger)
}

func TestBackchannel_concurrentRequestsFromMultipleClients(t *testing.T) {
	var interceptorInvoked int32
	registry := NewRegistry()
	lm := listenmux.New(insecure.NewCredentials())
	lm.Register(NewServerHandshaker(
		newLogger(),
		registry,
		[]grpc.DialOption{
			grpc.WithUnaryInterceptor(func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
				atomic.AddInt32(&interceptorInvoked, 1)
				return invoker(ctx, method, req, reply, cc, opts...)
			}),
		},
	))

	ln, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	errNonMultiplexed := status.Error(codes.FailedPrecondition, ErrNonMultiplexedConnection.Error())
	srv := grpc.NewServer(grpc.Creds(lm))

	gitalypb.RegisterRefTransactionServer(srv, mockTransactionServer{
		voteTransactionFunc: func(ctx context.Context, req *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
			peerID, err := GetPeerID(ctx)
			if err == ErrNonMultiplexedConnection {
				return nil, errNonMultiplexed
			}
			assert.NoError(t, err)

			cc, err := registry.Backchannel(peerID)
			if !assert.NoError(t, err) {
				return nil, err
			}

			return gitalypb.NewRefTransactionClient(cc).VoteTransaction(ctx, req)
		},
	})

	defer srv.Stop()
	go testhelper.MustServe(t, srv, ln)
	ctx := testhelper.Context(t)

	start := make(chan struct{})

	// Create 25 multiplexed clients and non-multiplexed clients that launch requests
	// concurrently.
	var wg sync.WaitGroup
	for i := uint64(0); i < 25; i++ {
		i := i
		wg.Add(2)

		go func() {
			defer wg.Done()

			<-start
			client, err := grpc.Dial(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			if !assert.NoError(t, err) {
				return
			}

			resp, err := gitalypb.NewRefTransactionClient(client).VoteTransaction(ctx, &gitalypb.VoteTransactionRequest{})
			testhelper.RequireGrpcError(t, errNonMultiplexed, err)
			assert.Nil(t, resp)

			assert.NoError(t, client.Close())
		}()

		go func() {
			defer wg.Done()

			expectedErr := status.Error(codes.Internal, fmt.Sprintf("multiplexed %d", i))

			clientHandshaker := NewClientHandshaker(newLogger(), func() Server {
				srv := grpc.NewServer()
				gitalypb.RegisterRefTransactionServer(srv, mockTransactionServer{
					voteTransactionFunc: func(ctx context.Context, req *gitalypb.VoteTransactionRequest) (*gitalypb.VoteTransactionResponse, error) {
						testhelper.ProtoEqual(t, &gitalypb.VoteTransactionRequest{TransactionId: i}, req)
						return nil, expectedErr
					},
				})

				return srv
			})

			<-start
			client, err := grpc.Dial(ln.Addr().String(),
				grpc.WithTransportCredentials(clientHandshaker.ClientHandshake(insecure.NewCredentials())),
			)
			if !assert.NoError(t, err) {
				return
			}

			// Run two invocations concurrently on each multiplexed client to sanity check
			// the routing works with multiple requests from a connection.
			var invocations sync.WaitGroup
			for invocation := 0; invocation < 2; invocation++ {
				invocations.Add(1)
				go func() {
					defer invocations.Done()
					resp, err := gitalypb.NewRefTransactionClient(client).VoteTransaction(ctx, &gitalypb.VoteTransactionRequest{TransactionId: i})
					testhelper.RequireGrpcError(t, expectedErr, err)
					assert.Nil(t, resp)
				}()
			}

			invocations.Wait()
			assert.NoError(t, client.Close())
		}()
	}

	// Establish the connection and fire the requests.
	close(start)

	// Wait for the clients to finish their calls and close their connections.
	wg.Wait()
	require.Equal(t, interceptorInvoked, int32(50))
}

type mockServer struct {
	serveFunc func(net.Listener) error
	stopFunc  func()
}

func (mock mockServer) Serve(ln net.Listener) error { return mock.serveFunc(ln) }
func (mock mockServer) Stop()                       { mock.stopFunc() }

func TestHandshaker_idempotentClose(t *testing.T) {
	clientPipe, serverPipe := net.Pipe()

	stopCalled := 0
	stopServing := make(chan struct{})
	serverErr := errors.New("serve error")
	clientHandshaker := NewClientHandshaker(testhelper.NewDiscardingLogEntry(t), func() Server {
		return mockServer{
			serveFunc: func(ln net.Listener) error {
				<-stopServing
				return serverErr
			},
			stopFunc: func() {
				close(stopServing)
				stopCalled++
			},
		}
	})

	closeServer := make(chan struct{})
	serverClosed := make(chan struct{})
	go func() {
		defer close(serverClosed)

		// Discard the magic byte
		magic := make([]byte, len(magicBytes))
		_, err := serverPipe.Read(magic)
		assert.NoError(t, err)
		assert.Equal(t, magicBytes, magic)

		conn, _, err := NewServerHandshaker(
			testhelper.NewDiscardingLogEntry(t),
			NewRegistry(),
			nil,
		).Handshake(serverPipe, nil)
		assert.NoError(t, err)

		<-closeServer
		for i := 0; i < 2; i++ {
			assert.NoError(t, conn.Close())
		}
	}()

	ctx := testhelper.Context(t)
	conn, _, err := clientHandshaker.ClientHandshake(insecure.NewCredentials()).ClientHandshake(ctx, "server name", clientPipe)
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		require.Equal(t, serverErr, conn.Close())
		require.Equal(t, 1, stopCalled)
	}

	close(closeServer)
	<-serverClosed
}

type mockSSHService struct {
	sshUploadPackFunc func(gitalypb.SSHService_SSHUploadPackServer) error
	*gitalypb.UnimplementedSSHServiceServer
}

func (m mockSSHService) SSHUploadPack(stream gitalypb.SSHService_SSHUploadPackServer) error {
	return m.sshUploadPackFunc(stream)
}

func Benchmark(b *testing.B) {
	for _, tc := range []struct {
		desc        string
		multiplexed bool
	}{
		{desc: "multiplexed", multiplexed: true},
		{desc: "normal"},
	} {
		b.Run(tc.desc, func(b *testing.B) {
			for _, messageSize := range []int64{
				1024,
				1024 * 1024,
				3 * 1024 * 1024,
			} {
				b.Run(fmt.Sprintf("message size %dkb", messageSize/1024), func(b *testing.B) {
					var serverOpts []grpc.ServerOption
					if tc.multiplexed {
						lm := listenmux.New(insecure.NewCredentials())
						lm.Register(NewServerHandshaker(newLogger(), NewRegistry(), nil))
						serverOpts = []grpc.ServerOption{
							grpc.Creds(lm),
						}
					}

					srv := grpc.NewServer(serverOpts...)
					gitalypb.RegisterSSHServiceServer(srv, mockSSHService{
						sshUploadPackFunc: func(stream gitalypb.SSHService_SSHUploadPackServer) error {
							for {
								_, err := stream.Recv()
								if err != nil {
									assert.Equal(b, io.EOF, err)
									return nil
								}
							}
						},
					})

					ln, err := net.Listen("tcp", "localhost:0")
					require.NoError(b, err)

					defer srv.Stop()
					go testhelper.MustServe(b, srv, ln)
					ctx := testhelper.Context(b)

					opts := []grpc.DialOption{grpc.WithBlock(), grpc.WithTransportCredentials(insecure.NewCredentials())}
					if tc.multiplexed {
						clientHandshaker := NewClientHandshaker(newLogger(), func() Server { return grpc.NewServer() })
						opts = []grpc.DialOption{
							grpc.WithBlock(),
							grpc.WithTransportCredentials(clientHandshaker.ClientHandshake(insecure.NewCredentials())),
						}
					}

					cc, err := grpc.DialContext(ctx, ln.Addr().String(), opts...)
					require.NoError(b, err)

					defer cc.Close()

					client, err := gitalypb.NewSSHServiceClient(cc).SSHUploadPack(ctx)
					require.NoError(b, err)

					request := &gitalypb.SSHUploadPackRequest{Stdin: make([]byte, messageSize)}
					b.SetBytes(messageSize)

					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						require.NoError(b, client.Send(request))
					}

					require.NoError(b, client.CloseSend())
					_, err = client.Recv()
					require.Equal(b, io.EOF, err)
				})
			}
		})
	}
}
