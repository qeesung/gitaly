package sidechannel

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v14/internal/listenmux"
	"gitlab.com/gitlab-org/gitaly/v14/internal/middleware/cancelhandler"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestSidechannel(t *testing.T) {
	const blobSize = 1024 * 1024

	in := make([]byte, blobSize)
	_, err := rand.Read(in)
	require.NoError(t, err)

	var out []byte
	require.NotEqual(t, in, out)

	addr := startServer(
		t,
		func(context context.Context, request *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
			conn, err := OpenSidechannel(context)
			if err != nil {
				return nil, err
			}
			defer conn.Close()

			if _, err = io.CopyN(conn, conn, blobSize); err != nil {
				return nil, err
			}
			return &healthpb.HealthCheckResponse{}, conn.Close()
		},
		nil,
	)

	conn, registry := dial(t, addr)
	err = call(
		context.Background(), conn, registry,
		func(conn *ClientConn) error {
			errC := make(chan error, 1)
			go func() {
				var err error
				out, err = io.ReadAll(conn)
				errC <- err
			}()

			_, err = io.Copy(conn, bytes.NewReader(in))
			require.NoError(t, err)
			require.NoError(t, <-errC)

			return nil
		},
	)
	require.NoError(t, err)
	require.Equal(t, in, out, "byte stream works")
}

// Conduct multiple requests with sidechannel included on the same grpc
// connection.
func TestSidechannelConcurrency(t *testing.T) {
	const concurrency = 10
	const blobSize = 1024 * 1024

	ins := make([][]byte, concurrency)
	for i := 0; i < concurrency; i++ {
		ins[i] = make([]byte, blobSize)
		_, err := rand.Read(ins[i])
		require.NoError(t, err)
	}

	outs := make([][]byte, concurrency)
	for i := 0; i < concurrency; i++ {
		require.NotEqual(t, ins[i], outs[i])
	}

	addr := startServer(
		t,
		func(context context.Context, request *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
			conn, err := OpenSidechannel(context)
			if err != nil {
				return nil, err
			}
			defer conn.Close()

			if _, err = io.CopyN(conn, conn, blobSize); err != nil {
				return nil, err
			}

			return &healthpb.HealthCheckResponse{}, conn.Close()
		},
		nil,
	)

	conn, registry := dial(t, addr)

	errors := make(chan error, concurrency)

	wg := sync.WaitGroup{}
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			err := call(
				context.Background(), conn, registry,
				func(conn *ClientConn) error {
					errC := make(chan error, 1)
					go func() {
						var err error
						outs[i], err = io.ReadAll(conn)
						errC <- err
					}()

					if _, err := io.Copy(conn, bytes.NewReader(ins[i])); err != nil {
						return err
					}
					if err := <-errC; err != nil {
						return err
					}

					return nil
				},
			)
			errors <- err
		}(i)
	}
	wg.Wait()

	for i := 0; i < concurrency; i++ {
		require.Equal(t, ins[i], outs[i], "byte stream works")
		require.NoError(t, <-errors)
	}
}

func TestSidechannelCancelled(t *testing.T) {
	addr := startServer(
		t,
		func(context context.Context, request *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
			conn, err := OpenSidechannel(context)
			if err != nil {
				return nil, err
			}
			defer conn.Close()

			if _, err := io.Copy(io.Discard, conn); err != nil {
				return nil, err
			}

			responseData := make([]byte, 64*1024)
			for {
				// Write into yamux connection until reaching error.
				if _, err = conn.Write(responseData); err != nil {
					return nil, err
				}
			}
		},
		[]grpc.ServerOption{
			grpc.UnaryInterceptor(cancelhandler.Unary),
			grpc.StreamInterceptor(cancelhandler.Stream),
		},
		withYamuxCfgFastTimeout(),
	)

	conn, registry := dial(t, addr, withYamuxCfgFastTimeout())
	client := healthpb.NewHealthClient(conn)

	ctxOut, waiter := RegisterSidechannel(context.Background(), registry, func(conn *ClientConn) error {
		// Send data to the server but not wait for the response
		return conn.CloseWrite()
	})
	defer waiter.Close()

	_, err := client.Check(ctxOut, &healthpb.HealthCheckRequest{})
	testhelper.RequireGrpcError(t, err, codes.Canceled)
}

func startServer(t *testing.T, th testHandler, grpcOpts []grpc.ServerOption, sidechannelOpts ...Option) string {
	t.Helper()

	options := defaultSidechannelOptions(logrus.StandardLogger().Writer())
	for _, opt := range sidechannelOpts {
		opt(options)
	}

	lm := listenmux.New(insecure.NewCredentials())
	lm.Register(backchannel.NewServerHandshaker(newLogger(), backchannel.NewRegistry(), nil, backchannel.WithYamuxConfig(options.yamuxConfig)))

	grpcOpts = append(grpcOpts, grpc.Creds(lm))

	s := grpc.NewServer(grpcOpts...)
	t.Cleanup(func() { s.Stop() })

	handler := &server{testHandler: th}
	healthpb.RegisterHealthServer(s, handler)

	lis, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)
	t.Cleanup(func() { lis.Close() })

	go func() { s.Serve(lis) }()

	return lis.Addr().String()
}

func dial(t *testing.T, addr string, opts ...Option) (*grpc.ClientConn, *Registry) {
	registry := NewRegistry()
	clientHandshaker := NewClientHandshaker(newLogger(), registry, opts...)
	dialOpt := grpc.WithTransportCredentials(clientHandshaker.ClientHandshake(insecure.NewCredentials()))

	conn, err := grpc.Dial(addr, dialOpt)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return conn, registry
}

func call(ctx context.Context, conn *grpc.ClientConn, registry *Registry, handler func(*ClientConn) error) error {
	client := healthpb.NewHealthClient(conn)

	ctxOut, waiter := RegisterSidechannel(ctx, registry, handler)
	defer waiter.Close()

	if _, err := client.Check(ctxOut, &healthpb.HealthCheckRequest{}); err != nil {
		return err
	}

	if err := waiter.Close(); err != nil {
		return err
	}

	return nil
}

type testHandler func(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error)

type server struct {
	healthpb.UnimplementedHealthServer
	testHandler
}

func (s *server) Check(context context.Context, request *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	return s.testHandler(context, request)
}

func withYamuxCfgFastTimeout() Option {
	return func(options *options) {
		options.yamuxConfig.StreamCloseTimeout = 10 * time.Millisecond
	}
}
