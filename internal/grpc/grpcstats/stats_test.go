package grpcstats

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/stats"
	"google.golang.org/protobuf/proto"
)

func newStubPayload() *grpc_testing.Payload {
	return &grpc_testing.Payload{Body: []byte("stub")}
}

type testService struct {
	grpc_testing.UnimplementedTestServiceServer
}

func (ts testService) UnaryCall(context.Context, *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
	return &grpc_testing.SimpleResponse{Payload: newStubPayload()}, nil
}

func (ts testService) HalfDuplexCall(stream grpc_testing.TestService_HalfDuplexCallServer) error {
	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
	}

	resp := &grpc_testing.StreamingOutputCallResponse{Payload: newStubPayload()}
	if err := stream.Send(proto.Clone(resp).(*grpc_testing.StreamingOutputCallResponse)); err != nil {
		return err
	}
	return stream.Send(proto.Clone(resp).(*grpc_testing.StreamingOutputCallResponse))
}

func TestPayloadBytes(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	logger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(logger)

	opts := []grpc.ServerOption{
		grpc.StatsHandler(log.PerRPCLogHandler{
			Underlying:     &PayloadBytes{},
			FieldProducers: []log.FieldsProducer{FieldsProducer},
		}),
		grpc.ChainUnaryInterceptor(
			logger.UnaryServerInterceptor(
				log.PropagationMessageProducer(log.DefaultInterceptorLogger(logger)),
				log.WithFiledProducers(FieldsProducer),
			),
			log.UnaryLogDataCatcherServerInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			logger.StreamServerInterceptor(
				log.PropagationMessageProducer(log.DefaultInterceptorLogger(logger)),
				log.WithFiledProducers(FieldsProducer),
			),
			log.StreamLogDataCatcherServerInterceptor(),
		),
	}

	srv := grpc.NewServer(opts...)
	grpc_testing.RegisterTestServiceServer(srv, testService{})
	sock, err := os.CreateTemp("", "")
	require.NoError(t, err)
	require.NoError(t, sock.Close())
	require.NoError(t, os.RemoveAll(sock.Name()))
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(sock.Name())) })

	lis, err := net.Listen("unix", sock.Name())
	require.NoError(t, err)

	t.Cleanup(srv.GracefulStop)
	go func() { assert.NoError(t, srv.Serve(lis)) }()

	cc, err := client.Dial(ctx, "unix://"+sock.Name())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cc.Close()) })

	testClient := grpc_testing.NewTestServiceClient(cc)
	const invocations = 2
	var wg sync.WaitGroup
	for i := 0; i < invocations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			resp, err := testClient.UnaryCall(ctx, &grpc_testing.SimpleRequest{Payload: newStubPayload()})
			if !assert.NoError(t, err) {
				return
			}
			require.Equal(t, newStubPayload(), resp.Payload)

			call, err := testClient.HalfDuplexCall(ctx)
			if !assert.NoError(t, err) {
				return
			}

			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					_, err := call.Recv()
					if errors.Is(err, io.EOF) {
						return
					}
					assert.NoError(t, err)
				}
			}()
			assert.NoError(t, call.Send(&grpc_testing.StreamingOutputCallRequest{Payload: newStubPayload()}))
			assert.NoError(t, call.Send(&grpc_testing.StreamingOutputCallRequest{Payload: newStubPayload()}))
			assert.NoError(t, call.CloseSend())
			<-done
		}()
	}
	wg.Wait()

	srv.GracefulStop()

	entries := hook.AllEntries()
	require.Len(t, entries, 4)
	var unary, stream int
	for _, e := range entries {
		if e.Message == "finished unary call with code OK" {
			unary++
			require.EqualValues(t, 8, e.Data["grpc.request.payload_bytes"])
			require.EqualValues(t, 8, e.Data["grpc.response.payload_bytes"])
		}
		if e.Message == "finished bidi_stream call with code OK" {
			stream++
			require.EqualValues(t, 16, e.Data["grpc.request.payload_bytes"])
			require.EqualValues(t, 16, e.Data["grpc.response.payload_bytes"])
		}
	}
	require.Equal(t, invocations, unary)
	require.Equal(t, invocations, stream)
}

func TestPayloadBytes_TagRPC(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx = (&PayloadBytes{}).TagRPC(ctx, nil)
	require.Equal(t,
		log.Fields{"grpc.request.payload_bytes": int64(0), "grpc.response.payload_bytes": int64(0)},
		FieldsProducer(ctx, nil),
	)
}

func TestPayloadBytes_HandleRPC(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	handler := &PayloadBytes{}
	ctx = handler.TagRPC(ctx, nil)

	handler.HandleRPC(ctx, nil)            // sanity check we don't fail anything
	handler.HandleRPC(ctx, &stats.Begin{}) // sanity check we don't fail anything
	handler.HandleRPC(ctx, &stats.InPayload{Length: 42})
	require.Equal(t,
		log.Fields{"grpc.request.payload_bytes": int64(42), "grpc.response.payload_bytes": int64(0)},
		FieldsProducer(ctx, nil),
	)
	handler.HandleRPC(ctx, &stats.OutPayload{Length: 24})
	require.Equal(t,
		log.Fields{"grpc.request.payload_bytes": int64(42), "grpc.response.payload_bytes": int64(24)},
		FieldsProducer(ctx, nil),
	)
	handler.HandleRPC(ctx, &stats.InPayload{Length: 38})
	require.Equal(t,
		log.Fields{"grpc.request.payload_bytes": int64(80), "grpc.response.payload_bytes": int64(24)},
		FieldsProducer(ctx, nil),
	)
	handler.HandleRPC(ctx, &stats.OutPayload{Length: 66})
	require.Equal(t,
		log.Fields{"grpc.request.payload_bytes": int64(80), "grpc.response.payload_bytes": int64(90)},
		FieldsProducer(ctx, nil),
	)
}

func TestPayloadBytesStats_Fields(t *testing.T) {
	t.Parallel()

	bytesStats := PayloadBytesStats{InPayloadBytes: 80, OutPayloadBytes: 90}
	require.Equal(t, log.Fields{
		"grpc.request.payload_bytes":  int64(80),
		"grpc.response.payload_bytes": int64(90),
	}, bytesStats.Fields())
}

func TestFieldsProducer(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	t.Run("ok", func(t *testing.T) {
		handler := &PayloadBytes{}
		ctx := handler.TagRPC(ctx, nil)
		handler.HandleRPC(ctx, &stats.InPayload{Length: 42})
		handler.HandleRPC(ctx, &stats.OutPayload{Length: 24})
		require.Equal(t, log.Fields{
			"grpc.request.payload_bytes":  int64(42),
			"grpc.response.payload_bytes": int64(24),
		}, FieldsProducer(ctx, nil))
	})

	t.Run("no data", func(t *testing.T) {
		require.Nil(t, FieldsProducer(ctx, nil))
	})
}
