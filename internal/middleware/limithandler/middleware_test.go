//go:build !gitaly_test_sha256

package limithandler_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	promtest "github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/limithandler"
	pb "gitlab.com/gitlab-org/gitaly/v15/internal/middleware/limithandler/testdata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func fixedLockKey(ctx context.Context) string {
	return "fixed-id"
}

func TestUnaryLimitHandler(t *testing.T) {
	t.Parallel()

	s := &server{blockCh: make(chan struct{})}

	cfg := config.Cfg{
		Concurrency: []config.Concurrency{
			{RPC: "/test.limithandler.Test/Unary", MaxPerRepo: 2},
		},
	}

	lh := limithandler.New(cfg, fixedLockKey, limithandler.WithConcurrencyLimiters)
	interceptor := lh.UnaryInterceptor()
	srv, serverSocketPath := runServer(t, s, grpc.UnaryInterceptor(interceptor))
	defer srv.Stop()

	client, conn := newClient(t, serverSocketPath)
	defer conn.Close()
	ctx := testhelper.Context(t)

	wg := &sync.WaitGroup{}
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()

			resp, err := client.Unary(ctx, &pb.UnaryRequest{})
			if !assert.NoError(t, err) {
				return
			}
			if !assert.NotNil(t, resp) {
				return
			}
			assert.True(t, resp.Ok)
		}()
	}

	time.Sleep(100 * time.Millisecond)

	require.Equal(t, 2, s.getRequestCount())

	close(s.blockCh)
	wg.Wait()
}

func TestStreamLimitHandler(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		desc                  string
		fullname              string
		f                     func(*testing.T, context.Context, pb.TestClient, chan interface{}, chan error)
		maxConcurrency        int
		expectedRequestCount  int
		expectedResponseCount int
	}{
		// The max queue size is set at 1, which means 1 request
		// will be queued while the later ones will return with
		// an error. That means that maxConcurrency number of
		// requests will be processing but blocked due to blockCh.
		// 1 request will be waiting to be picked up, and will be
		// processed once we close the blockCh.
		{
			desc:     "Single request, multiple responses",
			fullname: "/test.limithandler.Test/StreamOutput",
			f: func(t *testing.T, ctx context.Context, client pb.TestClient, respCh chan interface{}, errCh chan error) {
				stream, err := client.StreamOutput(ctx, &pb.StreamOutputRequest{})
				require.NoError(t, err)
				require.NotNil(t, stream)

				r, err := stream.Recv()
				if err != nil {
					errCh <- err
					return
				}
				require.NotNil(t, r)
				require.True(t, r.Ok)
				respCh <- r
			},
			maxConcurrency:        3,
			expectedRequestCount:  4,
			expectedResponseCount: 4,
		},
		{
			desc:     "Multiple requests, single response",
			fullname: "/test.limithandler.Test/StreamInput",
			f: func(t *testing.T, ctx context.Context, client pb.TestClient, respCh chan interface{}, errCh chan error) {
				stream, err := client.StreamInput(ctx)
				require.NoError(t, err)
				require.NotNil(t, stream)

				require.NoError(t, stream.Send(&pb.StreamInputRequest{}))
				r, err := stream.CloseAndRecv()
				if err != nil {
					errCh <- err
					return
				}
				require.NotNil(t, r)
				require.True(t, r.Ok)
				respCh <- r
			},
			maxConcurrency:        3,
			expectedRequestCount:  4,
			expectedResponseCount: 4,
		},
		{
			desc:     "Multiple requests, multiple responses",
			fullname: "/test.limithandler.Test/Bidirectional",
			f: func(t *testing.T, ctx context.Context, client pb.TestClient, respCh chan interface{}, errCh chan error) {
				stream, err := client.Bidirectional(ctx)
				require.NoError(t, err)
				require.NotNil(t, stream)

				require.NoError(t, stream.Send(&pb.BidirectionalRequest{}))
				require.NoError(t, stream.CloseSend())

				r, err := stream.Recv()
				if err != nil {
					errCh <- err
					return
				}
				require.NotNil(t, r)
				require.True(t, r.Ok)
				respCh <- r
			},
			maxConcurrency:        3,
			expectedRequestCount:  4,
			expectedResponseCount: 4,
		},
		{
			// Make sure that _streams_ are limited but that _requests_ on each
			// allowed stream are not limited.
			desc:     "Multiple requests with same id, multiple responses",
			fullname: "/test.limithandler.Test/Bidirectional",
			f: func(t *testing.T, ctx context.Context, client pb.TestClient, respCh chan interface{}, errCh chan error) {
				stream, err := client.Bidirectional(ctx)
				require.NoError(t, err)
				require.NotNil(t, stream)

				// Since the concurrency id is fixed all requests have the same
				// id, but subsequent requests in a stream, even with the same
				// id, should bypass the concurrency limiter
				for i := 0; i < 10; i++ {
					require.NoError(t, stream.Send(&pb.BidirectionalRequest{}))
				}
				require.NoError(t, stream.CloseSend())

				r, err := stream.Recv()
				if err != nil {
					errCh <- err
					return
				}
				require.NotNil(t, r)
				require.True(t, r.Ok)
				respCh <- r
			},
			maxConcurrency: 3,
			// 3 (concurrent streams allowed) * 10 (requests per stream)
			// + 1 (queued stream) * (10 requests per stream)
			expectedRequestCount:  40,
			expectedResponseCount: 4,
		},
		{
			desc:     "With a max concurrency of 0",
			fullname: "/test.limithandler.Test/StreamOutput",
			f: func(t *testing.T, ctx context.Context, client pb.TestClient, respCh chan interface{}, errCh chan error) {
				stream, err := client.StreamOutput(ctx, &pb.StreamOutputRequest{})
				require.NoError(t, err)
				require.NotNil(t, stream)

				r, err := stream.Recv()
				if err != nil {
					errCh <- err
					return
				}
				require.NotNil(t, r)
				require.True(t, r.Ok)
				respCh <- r
			},
			maxConcurrency:        0,
			expectedRequestCount:  10,
			expectedResponseCount: 10,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			s := &server{blockCh: make(chan struct{})}

			maxQueueSize := 1
			cfg := config.Cfg{
				Concurrency: []config.Concurrency{
					{
						RPC:          tc.fullname,
						MaxPerRepo:   tc.maxConcurrency,
						MaxQueueSize: maxQueueSize,
					},
				},
			}

			lh := limithandler.New(cfg, fixedLockKey, limithandler.WithConcurrencyLimiters)
			interceptor := lh.StreamInterceptor()
			srv, serverSocketPath := runServer(t, s, grpc.StreamInterceptor(interceptor))
			defer srv.Stop()

			client, conn := newClient(t, serverSocketPath)
			defer conn.Close()
			ctx := testhelper.Context(t)

			totalCalls := 10

			errChan := make(chan error)
			respChan := make(chan interface{})

			for i := 0; i < totalCalls; i++ {
				go func() {
					tc.f(t, ctx, client, respChan, errChan)
				}()
			}

			if tc.maxConcurrency > 0 {
				for i := 0; i < totalCalls-tc.maxConcurrency-maxQueueSize; i++ {
					err := <-errChan
					testhelper.RequireGrpcError(
						t,
						helper.ErrInternalf("rate limiting stream request: %w",
							limithandler.ErrMaxQueueSize), err)
				}
			}

			close(s.blockCh)

			for i := 0; i < tc.expectedResponseCount; i++ {
				<-respChan
			}

			require.Equal(t, tc.expectedRequestCount, s.getRequestCount())
		})
	}
}

func TestStreamLimitHandler_error(t *testing.T) {
	t.Parallel()

	s := &queueTestServer{reqArrivedCh: make(chan struct{})}
	s.blockCh = make(chan struct{})

	cfg := config.Cfg{
		Concurrency: []config.Concurrency{
			{RPC: "/test.limithandler.Test/Bidirectional", MaxPerRepo: 1, MaxQueueSize: 1},
		},
	}

	lh := limithandler.New(cfg, fixedLockKey, limithandler.WithConcurrencyLimiters)
	interceptor := lh.StreamInterceptor()
	srv, serverSocketPath := runServer(t, s, grpc.StreamInterceptor(interceptor))
	defer srv.Stop()

	client, conn := newClient(t, serverSocketPath)
	defer conn.Close()

	ctx := testhelper.Context(t)

	respChan := make(chan *pb.BidirectionalResponse)
	go func() {
		stream, err := client.Bidirectional(ctx)
		require.NoError(t, err)
		require.NoError(t, stream.Send(&pb.BidirectionalRequest{}))
		require.NoError(t, stream.CloseSend())
		resp, err := stream.Recv()
		require.NoError(t, err)
		respChan <- resp
	}()
	// The first request will be blocked by blockCh.
	<-s.reqArrivedCh

	// These are the second and third requests to be sent.
	// The second request will be waiting in the queue.
	// The third request should return with an error.
	errChan := make(chan error)
	for i := 0; i < 2; i++ {
		go func() {
			stream, err := client.Bidirectional(ctx)
			require.NoError(t, err)
			require.NotNil(t, stream)
			require.NoError(t, stream.Send(&pb.BidirectionalRequest{}))
			require.NoError(t, stream.CloseSend())
			resp, err := stream.Recv()

			if err != nil {
				errChan <- err
			} else {
				respChan <- resp
			}
		}()
	}

	err := <-errChan
	testhelper.RequireGrpcCode(t, err, codes.Unavailable)
	// ensure it is a structured error
	st, ok := status.FromError(err)
	require.True(t, ok)

	testhelper.ProtoEqual(t, []interface{}{&gitalypb.LimitError{
		ErrorMessage: "maximum queue size reached",
		RetryAfter:   &durationpb.Duration{},
	}}, st.Details())

	// allow the first request to finish
	close(s.blockCh)

	// This allows the second request to finish
	<-s.reqArrivedCh

	// we expect two responses. The first request, and the second
	// request. The third request returned immediately with an error
	// from the limit handler.
	<-respChan
	<-respChan
}

type queueTestServer struct {
	server
	reqArrivedCh chan struct{}
}

func (q *queueTestServer) Unary(ctx context.Context, in *pb.UnaryRequest) (*pb.UnaryResponse, error) {
	q.registerRequest()

	q.reqArrivedCh <- struct{}{} // We need a way to know when a request got to the middleware
	<-q.blockCh                  // Block to ensure concurrency

	return &pb.UnaryResponse{Ok: true}, nil
}

func (q *queueTestServer) Bidirectional(stream pb.Test_BidirectionalServer) error {
	// Read all the input
	for {
		if _, err := stream.Recv(); err != nil {
			if err != io.EOF {
				return err
			}

			break
		}
		q.reqArrivedCh <- struct{}{}

		q.registerRequest()
	}
	<-q.blockCh // Block to ensure concurrency

	return stream.Send(&pb.BidirectionalResponse{Ok: true})
}

func TestConcurrencyLimitHandlerMetrics(t *testing.T) {
	s := &queueTestServer{reqArrivedCh: make(chan struct{})}
	s.blockCh = make(chan struct{})

	methodName := "/test.limithandler.Test/Unary"
	cfg := config.Cfg{
		Concurrency: []config.Concurrency{
			{RPC: methodName, MaxPerRepo: 1, MaxQueueSize: 1},
		},
	}

	lh := limithandler.New(cfg, fixedLockKey, limithandler.WithConcurrencyLimiters)
	interceptor := lh.UnaryInterceptor()
	srv, serverSocketPath := runServer(t, s, grpc.UnaryInterceptor(interceptor))
	defer srv.Stop()

	client, conn := newClient(t, serverSocketPath)
	defer conn.Close()

	ctx := testhelper.Context(t)

	respCh := make(chan *pb.UnaryResponse)
	go func() {
		resp, err := client.Unary(ctx, &pb.UnaryRequest{})
		respCh <- resp
		require.NoError(t, err)
	}()
	// wait until the first request is being processed. After this, requests will be queued
	<-s.reqArrivedCh

	errChan := make(chan error)
	// out of ten requests, the first one will be queued and the other 9 will return with
	// an error
	for i := 0; i < 10; i++ {
		go func() {
			resp, err := client.Unary(ctx, &pb.UnaryRequest{})
			if err != nil {
				errChan <- err
			} else {
				respCh <- resp
			}
		}()
	}

	var errs int
	for err := range errChan {
		s, ok := status.FromError(err)
		require.True(t, ok)
		details := s.Details()
		require.Len(t, details, 1)

		limitErr, ok := details[0].(*gitalypb.LimitError)
		require.True(t, ok)

		assert.Equal(t, limithandler.ErrMaxQueueSize.Error(), limitErr.ErrorMessage)
		assert.Equal(t, durationpb.New(0), limitErr.RetryAfter)

		errs++
		if errs == 9 {
			break
		}
	}

	expectedMetrics := `# HELP gitaly_concurrency_limiting_in_progress Gauge of number of concurrent in-progress calls
# TYPE gitaly_concurrency_limiting_in_progress gauge
gitaly_concurrency_limiting_in_progress{grpc_method="ReplicateRepository",grpc_service="gitaly.RepositoryService",system="gitaly"} 0
gitaly_concurrency_limiting_in_progress{grpc_method="Unary",grpc_service="test.limithandler.Test",system="gitaly"} 1
# HELP gitaly_concurrency_limiting_queued Gauge of number of queued calls
# TYPE gitaly_concurrency_limiting_queued gauge
gitaly_concurrency_limiting_queued{grpc_method="ReplicateRepository",grpc_service="gitaly.RepositoryService",system="gitaly"} 0
gitaly_concurrency_limiting_queued{grpc_method="Unary",grpc_service="test.limithandler.Test",system="gitaly"} 1
# HELP gitaly_requests_dropped_total Number of requests dropped from the queue
# TYPE gitaly_requests_dropped_total counter
gitaly_requests_dropped_total{grpc_method="Unary",grpc_service="test.limithandler.Test",reason="max_size",system="gitaly"} 9
`
	assert.NoError(t, promtest.CollectAndCompare(lh, bytes.NewBufferString(expectedMetrics),
		"gitaly_concurrency_limiting_queued",
		"gitaly_requests_dropped_total",
		"gitaly_concurrency_limiting_in_progress"))

	close(s.blockCh)
	<-s.reqArrivedCh
	// we expect two requests to complete. The first one that started to process immediately,
	// and the second one that got queued
	<-respCh
	<-respCh
}

func TestRateLimitHandler(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.RateLimit).Run(t, testRateLimitHandler)
}

func testRateLimitHandler(t *testing.T, ctx context.Context) {
	methodName := "/test.limithandler.Test/Unary"
	cfg := config.Cfg{
		RateLimiting: []config.RateLimiting{
			{RPC: methodName, Interval: 1 * time.Hour, Burst: 1},
		},
	}

	t.Run("rate has hit max", func(t *testing.T) {
		s := &server{blockCh: make(chan struct{})}

		lh := limithandler.New(cfg, fixedLockKey, limithandler.WithRateLimiters(ctx))
		interceptor := lh.UnaryInterceptor()
		srv, serverSocketPath := runServer(t, s, grpc.UnaryInterceptor(interceptor))
		defer srv.Stop()

		client, conn := newClient(t, serverSocketPath)
		defer testhelper.MustClose(t, conn)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.Unary(ctx, &pb.UnaryRequest{})
			require.NoError(t, err)
		}()
		// wait until the first request is being processed so we know the rate
		// limiter already knows about it.
		s.blockCh <- struct{}{}
		close(s.blockCh)

		for i := 0; i < 10; i++ {
			_, err := client.Unary(ctx, &pb.UnaryRequest{})

			if featureflag.RateLimit.IsEnabled(ctx) {
				s, ok := status.FromError(err)
				require.True(t, ok)
				details := s.Details()
				require.Len(t, details, 1)

				limitErr, ok := details[0].(*gitalypb.LimitError)
				require.True(t, ok)

				assert.Equal(t, limithandler.ErrRateLimit.Error(), limitErr.ErrorMessage)
				assert.Equal(t, durationpb.New(0), limitErr.RetryAfter)

			} else {
				require.NoError(t, err)
			}
		}

		expectedMetrics := `# HELP gitaly_requests_dropped_total Number of requests dropped from the queue
# TYPE gitaly_requests_dropped_total counter
gitaly_requests_dropped_total{grpc_method="Unary",grpc_service="test.limithandler.Test",reason="rate",system="gitaly"} 10
`
		assert.NoError(t, promtest.CollectAndCompare(lh, bytes.NewBufferString(expectedMetrics),
			"gitaly_requests_dropped_total"))

		wg.Wait()
	})

	t.Run("rate has not hit max", func(t *testing.T) {
		s := &server{blockCh: make(chan struct{})}

		lh := limithandler.New(cfg, fixedLockKey, limithandler.WithRateLimiters(ctx))
		interceptor := lh.UnaryInterceptor()
		srv, serverSocketPath := runServer(t, s, grpc.UnaryInterceptor(interceptor))
		defer srv.Stop()

		client, conn := newClient(t, serverSocketPath)
		defer testhelper.MustClose(t, conn)

		close(s.blockCh)
		_, err := client.Unary(ctx, &pb.UnaryRequest{})
		require.NoError(t, err)

		expectedMetrics := `# HELP gitaly_requests_dropped_total Number of requests dropped from the queue
# TYPE gitaly_requests_dropped_total counter
gitaly_requests_dropped_total{grpc_method="Unary",grpc_service="test.limithandler.Test",reason="rate",system="gitaly"} 0
`
		assert.NoError(t, promtest.CollectAndCompare(lh, bytes.NewBufferString(expectedMetrics),
			"gitaly_requests_dropped_total"))
	})
}

func runServer(t *testing.T, s pb.TestServer, opt ...grpc.ServerOption) (*grpc.Server, string) {
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName(t)
	grpcServer := grpc.NewServer(opt...)
	pb.RegisterTestServer(grpcServer, s)

	lis, err := net.Listen("unix", serverSocketPath)
	require.NoError(t, err)

	go grpcServer.Serve(lis)

	return grpcServer, "unix://" + serverSocketPath
}

func newClient(t *testing.T, serverSocketPath string) (pb.TestClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return pb.NewTestClient(conn), conn
}
