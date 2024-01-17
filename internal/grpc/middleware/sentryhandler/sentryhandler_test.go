package sentryhandler

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	sentry "github.com/getsentry/sentry-go"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestGenerateSentryEvent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc          string
		ctx           context.Context
		method        string
		duration      time.Duration
		err           error
		expectedEvent *sentry.Event
	}{
		{
			desc:     "internal error",
			method:   "/gitaly.SSHService/SSHUploadPack",
			duration: 500 * time.Millisecond,
			err:      fmt.Errorf("Internal"),
			expectedEvent: &sentry.Event{
				Message:     "Internal",
				Transaction: "SSHService::SSHUploadPack",
				Tags: map[string]string{
					"grpc.code":    "Unknown",
					"grpc.method":  "/gitaly.SSHService/SSHUploadPack",
					"grpc.time_ms": "500",
					"system":       "grpc",
				},
				Fingerprint: []string{
					"grpc", "SSHService::SSHUploadPack", "Unknown",
				},
				Exception: []sentry.Exception{
					{
						Type:  "*errors.errorString",
						Value: "Internal",
					},
				},
				Extra:    map[string]any{},
				Contexts: map[string]map[string]any{},
				Modules:  map[string]string{},
			},
		},
		{
			desc:     "GRPC error",
			method:   "/gitaly.RepoService/RepoExists",
			duration: 500 * time.Millisecond,
			err:      status.Errorf(codes.NotFound, "Something failed"),
			expectedEvent: &sentry.Event{
				Message:     "rpc error: code = NotFound desc = Something failed",
				Transaction: "RepoService::RepoExists",
				Tags: map[string]string{
					"grpc.code":    "NotFound",
					"grpc.method":  "/gitaly.RepoService/RepoExists",
					"grpc.time_ms": "500",
					"system":       "grpc",
				},
				Fingerprint: []string{
					"grpc", "RepoService::RepoExists", "NotFound",
				},
				Exception: []sentry.Exception{
					{
						Type:  "*status.Error",
						Value: "rpc error: code = NotFound desc = Something failed",
					},
				},
				Extra:    map[string]any{},
				Contexts: map[string]map[string]any{},
				Modules:  map[string]string{},
			},
		},
		{
			desc:          "successful call is ignored",
			method:        "/gitaly.RepoService/RepoExists",
			err:           nil,
			expectedEvent: nil,
		},
		{
			desc:          "TreeEntry with NotFound error is ignored",
			method:        "/gitaly.CommitService/TreeEntry",
			err:           status.Errorf(codes.NotFound, "Path not found"),
			expectedEvent: nil,
		},
		{
			desc:          "Canceled error is ignored",
			method:        "/gitaly.RepoService/RepoExists",
			err:           status.Errorf(codes.Canceled, "Something failed"),
			expectedEvent: nil,
		},
		{
			desc:          "DeadlineExceeded error is ignored",
			method:        "/gitaly.RepoService/RepoExists",
			err:           status.Errorf(codes.DeadlineExceeded, "Something failed"),
			expectedEvent: nil,
		},
		{
			desc:          "FailedPrecondition error is ignored",
			method:        "/gitaly.RepoService/RepoExists",
			err:           status.Errorf(codes.FailedPrecondition, "Something failed"),
			expectedEvent: nil,
		},
		{
			desc: "error that is not marked to be skipped is not ignored",
			ctx: func() context.Context {
				skipSubmission := false
				return context.WithValue(testhelper.Context(t), skipSubmissionKey{}, &skipSubmission)
			}(),
			duration: 500 * time.Millisecond,
			method:   "/gitaly.RepoService/RepoExists",
			err:      status.Errorf(codes.NotFound, "Something failed"),
			expectedEvent: &sentry.Event{
				Message:     "rpc error: code = NotFound desc = Something failed",
				Transaction: "RepoService::RepoExists",
				Tags: map[string]string{
					"grpc.code":    "NotFound",
					"grpc.method":  "/gitaly.RepoService/RepoExists",
					"grpc.time_ms": "500",
					"system":       "grpc",
				},
				Fingerprint: []string{
					"grpc", "RepoService::RepoExists", "NotFound",
				},
				Exception: []sentry.Exception{
					{
						Type:  "*status.Error",
						Value: "rpc error: code = NotFound desc = Something failed",
					},
				},
				Extra:    map[string]any{},
				Contexts: map[string]map[string]any{},
				Modules:  map[string]string{},
			},
		},
		{
			desc: "error that is marked to be skipped is ignored",
			ctx: func() context.Context {
				skipSubmission := false
				ctx := context.WithValue(testhelper.Context(t), skipSubmissionKey{}, &skipSubmission)

				MarkToSkip(ctx)
				return ctx
			}(),
			method:        "/gitaly.RepoService/RepoExists",
			err:           status.Errorf(codes.NotFound, "Something failed"),
			expectedEvent: nil,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := tc.ctx
			if ctx == nil {
				ctx = testhelper.Context(t)
			}

			event := generateSentryEvent(ctx, tc.method, tc.duration, tc.err)
			require.Equal(t, tc.expectedEvent, event)
		})
	}
}

func TestUnaryLogHandler(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc           string
		handler        func(context.Context, *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error)
		expectedEvents []*sentry.Event
	}{
		{
			desc: "no error",
			handler: func(context.Context, *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
				return &grpc_testing.SimpleResponse{}, nil
			},
		},
		{
			desc: "with error",
			handler: func(context.Context, *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
				return nil, fmt.Errorf("oopsie")
			},
			expectedEvents: []*sentry.Event{
				{
					Message:     "oopsie",
					Transaction: "::grpc.testing.TestService/UnaryCall",
					Tags: map[string]string{
						"grpc.code":   "Unknown",
						"grpc.method": "/grpc.testing.TestService/UnaryCall",
						"system":      "grpc",
					},
					Fingerprint: []string{
						"grpc", "::grpc.testing.TestService/UnaryCall", "Unknown",
					},
					Exception: []sentry.Exception{
						{
							Type:  "*errors.errorString",
							Value: "oopsie",
						},
					},
					Extra:    map[string]any{},
					Contexts: map[string]map[string]any{},
					Modules:  map[string]string{},
				},
			},
		},
		{
			desc: "with structured error",
			handler: func(context.Context, *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
				return nil, structerr.NewNotFound("not found")
			},
			expectedEvents: []*sentry.Event{
				{
					Message:     "not found",
					Transaction: "::grpc.testing.TestService/UnaryCall",
					Tags: map[string]string{
						"grpc.code":   "NotFound",
						"grpc.method": "/grpc.testing.TestService/UnaryCall",
						"system":      "grpc",
					},
					Fingerprint: []string{
						"grpc", "::grpc.testing.TestService/UnaryCall", "NotFound",
					},
					Exception: []sentry.Exception{
						{
							Type:  "structerr.Error",
							Value: "not found",
						},
					},
					Extra:    map[string]any{},
					Contexts: map[string]map[string]any{},
					Modules:  map[string]string{},
				},
			},
		},
		{
			desc: "with error marked to skip",
			handler: func(ctx context.Context, _ *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
				MarkToSkip(ctx)
				return nil, fmt.Errorf("this error should be skipped")
			},
			expectedEvents: nil,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			server := mockServiceServer{
				unaryCallHandler: tc.handler,
			}
			client := server.setup(t, ctx)

			// We don't care about any errors returned by this call.
			_, _ = client.UnaryCall(ctx, &grpc_testing.SimpleRequest{})

			// The exact time spent in the RPC is indeterministic, so we only verify that the field exists
			// without asserting its exact value.
			for _, event := range server.events {
				require.Contains(t, event.Tags, "grpc.time_ms")
				delete(event.Tags, "grpc.time_ms")
			}

			require.Equal(t, tc.expectedEvents, server.events)
		})
	}
}

func TestStreamLogHandler(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc           string
		handler        func(server grpc_testing.TestService_FullDuplexCallServer) error
		expectedEvents []*sentry.Event
	}{
		{
			desc: "no error",
			handler: func(server grpc_testing.TestService_FullDuplexCallServer) error {
				return nil
			},
		},
		{
			desc: "with error",
			handler: func(server grpc_testing.TestService_FullDuplexCallServer) error {
				return fmt.Errorf("oopsie")
			},
			expectedEvents: []*sentry.Event{
				{
					Message:     "oopsie",
					Transaction: "::grpc.testing.TestService/FullDuplexCall",
					Tags: map[string]string{
						"grpc.code":   "Unknown",
						"grpc.method": "/grpc.testing.TestService/FullDuplexCall",
						"system":      "grpc",
					},
					Fingerprint: []string{
						"grpc", "::grpc.testing.TestService/FullDuplexCall", "Unknown",
					},
					Exception: []sentry.Exception{
						{
							Type:  "*errors.errorString",
							Value: "oopsie",
						},
					},
					Extra:    map[string]any{},
					Contexts: map[string]map[string]any{},
					Modules:  map[string]string{},
				},
			},
		},
		{
			desc: "with structured error",
			handler: func(server grpc_testing.TestService_FullDuplexCallServer) error {
				return structerr.NewNotFound("not found")
			},
			expectedEvents: []*sentry.Event{
				{
					Message:     "not found",
					Transaction: "::grpc.testing.TestService/FullDuplexCall",
					Tags: map[string]string{
						"grpc.code":   "NotFound",
						"grpc.method": "/grpc.testing.TestService/FullDuplexCall",
						"system":      "grpc",
					},
					Fingerprint: []string{
						"grpc", "::grpc.testing.TestService/FullDuplexCall", "NotFound",
					},
					Exception: []sentry.Exception{
						{
							Type:  "structerr.Error",
							Value: "not found",
						},
					},
					Extra:    map[string]any{},
					Contexts: map[string]map[string]any{},
					Modules:  map[string]string{},
				},
			},
		},
		{
			desc: "with error marked to skip",
			handler: func(server grpc_testing.TestService_FullDuplexCallServer) error {
				MarkToSkip(server.Context())
				return fmt.Errorf("this error should be skipped")
			},
			expectedEvents: nil,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			server := mockServiceServer{
				fullDuplexCallHandler: tc.handler,
			}
			client := server.setup(t, ctx)

			stream, err := client.FullDuplexCall(ctx)
			require.NoError(t, err)
			// We don't care about any errors returned by this call.
			_, _ = stream.Recv()

			// The exact time spent in the RPC is indeterministic, so we only verify that the field exists
			// without asserting its exact value.
			for _, event := range server.events {
				require.Contains(t, event.Tags, "grpc.time_ms")
				delete(event.Tags, "grpc.time_ms")
			}

			require.Equal(t, tc.expectedEvents, server.events)
		})
	}
}

type mockServiceServer struct {
	grpc_testing.TestServiceServer
	unaryCallHandler      func(context.Context, *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error)
	fullDuplexCallHandler func(server grpc_testing.TestService_FullDuplexCallServer) error
	events                []*sentry.Event
}

func (s *mockServiceServer) setup(tb testing.TB, ctx context.Context) grpc_testing.TestServiceClient {
	tb.Helper()

	eventReporterOption := WithEventReporter(func(event *sentry.Event) *sentry.EventID {
		s.events = append(s.events, event)
		return nil
	})

	server := grpc.NewServer(
		grpc.UnaryInterceptor(UnaryLogHandler(eventReporterOption)),
		grpc.StreamInterceptor(StreamLogHandler(eventReporterOption)),
	)
	tb.Cleanup(server.Stop)
	grpc_testing.RegisterTestServiceServer(server, s)

	listener := bufconn.Listen(1)
	go testhelper.MustServe(tb, server, listener)

	conn, err := grpc.DialContext(ctx, listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return listener.DialContext(ctx)
		}),
	)
	require.NoError(tb, err)
	tb.Cleanup(func() { testhelper.MustClose(tb, conn) })

	return grpc_testing.NewTestServiceClient(conn)
}

func (s mockServiceServer) UnaryCall(ctx context.Context, request *grpc_testing.SimpleRequest) (*grpc_testing.SimpleResponse, error) {
	return s.unaryCallHandler(ctx, request)
}

func (s mockServiceServer) FullDuplexCall(server grpc_testing.TestService_FullDuplexCallServer) error {
	return s.fullDuplexCallHandler(server)
}
