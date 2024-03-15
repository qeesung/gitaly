package requestinfohandler

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	grpcmwtags "github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/stretchr/testify/require"
	gitalylog "gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/labkit/correlation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

const (
	correlationID = "CORRELATION_ID"
	clientName    = "CLIENT_NAME"
)

func TestNewRequestInfo(t *testing.T) {
	t.Parallel()

	baseContext := testhelper.Context(t)

	for _, tc := range []struct {
		desc         string
		fullMethod   string
		metadata     metadata.MD
		deadline     bool
		expectedInfo *RequestInfo
	}{
		{
			desc:     "empty metadata",
			metadata: metadata.Pairs(),
			deadline: false,
			expectedInfo: &RequestInfo{
				methodType:      "unary",
				clientName:      unknownValue,
				callSite:        unknownValue,
				authVersion:     unknownValue,
				deadlineType:    "none",
				methodOperation: unknownValue,
				methodScope:     unknownValue,
			},
		},
		{
			desc:     "context containing metadata",
			metadata: metadata.Pairs("call_site", "testsite"),
			deadline: false,
			expectedInfo: &RequestInfo{
				methodType:      "unary",
				clientName:      unknownValue,
				callSite:        "testsite",
				authVersion:     unknownValue,
				deadlineType:    "none",
				methodOperation: unknownValue,
				methodScope:     unknownValue,
			},
		},
		{
			desc:     "context containing metadata and a deadline",
			metadata: metadata.Pairs("call_site", "testsite"),
			deadline: true,
			expectedInfo: &RequestInfo{
				methodType:      "unary",
				clientName:      unknownValue,
				callSite:        "testsite",
				authVersion:     unknownValue,
				deadlineType:    unknownValue,
				methodOperation: unknownValue,
				methodScope:     unknownValue,
			},
		},
		{
			desc:     "context containing metadata and a deadline type",
			metadata: metadata.Pairs("deadline_type", "regular"),
			deadline: true,
			expectedInfo: &RequestInfo{
				methodType:      "unary",
				clientName:      unknownValue,
				callSite:        unknownValue,
				authVersion:     unknownValue,
				deadlineType:    "regular",
				methodOperation: unknownValue,
				methodScope:     unknownValue,
			},
		},
		{
			desc:     "a context without deadline but with deadline type",
			metadata: metadata.Pairs("deadline_type", "regular"),
			deadline: false,
			expectedInfo: &RequestInfo{
				methodType:      "unary",
				clientName:      unknownValue,
				callSite:        unknownValue,
				authVersion:     unknownValue,
				deadlineType:    "none",
				methodOperation: unknownValue,
				methodScope:     unknownValue,
			},
		},
		{
			desc:     "with a context containing metadata",
			metadata: metadata.Pairs("deadline_type", "regular", "client_name", "rails"),
			deadline: true,
			expectedInfo: &RequestInfo{
				methodType:      "unary",
				clientName:      "rails",
				callSite:        unknownValue,
				authVersion:     unknownValue,
				deadlineType:    "regular",
				methodOperation: unknownValue,
				methodScope:     unknownValue,
			},
		},
		{
			desc:       "with unknown method",
			fullMethod: "/gitaly.RepositoryService/UnknownMethod",
			metadata:   metadata.Pairs(),
			deadline:   false,
			expectedInfo: &RequestInfo{
				FullMethod:      "/gitaly.RepositoryService/UnknownMethod",
				methodType:      "unary",
				clientName:      unknownValue,
				callSite:        unknownValue,
				authVersion:     unknownValue,
				deadlineType:    "none",
				methodOperation: unknownValue,
				methodScope:     unknownValue,
			},
		},
		{
			desc:       "with repository-scoped accessor",
			fullMethod: "/gitaly.RepositoryService/ObjectFormat",
			metadata:   metadata.Pairs(),
			deadline:   false,
			expectedInfo: &RequestInfo{
				FullMethod:      "/gitaly.RepositoryService/ObjectFormat",
				methodType:      "unary",
				clientName:      unknownValue,
				callSite:        unknownValue,
				authVersion:     unknownValue,
				deadlineType:    "none",
				methodOperation: "accessor",
				methodScope:     "repository",
			},
		},
		{
			desc:       "with repository-scoped mutator",
			fullMethod: "/gitaly.RepositoryService/CreateRepository",
			metadata:   metadata.Pairs(),
			deadline:   false,
			expectedInfo: &RequestInfo{
				FullMethod:      "/gitaly.RepositoryService/CreateRepository",
				methodType:      "unary",
				clientName:      unknownValue,
				callSite:        unknownValue,
				authVersion:     unknownValue,
				deadlineType:    "none",
				methodOperation: "mutator",
				methodScope:     "repository",
			},
		},
		{
			desc:       "with repository-scoped maintenance",
			fullMethod: "/gitaly.RepositoryService/OptimizeRepository",
			metadata:   metadata.Pairs(),
			deadline:   false,
			expectedInfo: &RequestInfo{
				FullMethod:      "/gitaly.RepositoryService/OptimizeRepository",
				methodType:      "unary",
				clientName:      unknownValue,
				callSite:        unknownValue,
				authVersion:     unknownValue,
				deadlineType:    "none",
				methodOperation: "maintenance",
				methodScope:     "repository",
			},
		},
		{
			desc:       "with repository-scoped maintenance",
			fullMethod: "/gitaly.RepositoryService/OptimizeRepository",
			metadata:   metadata.Pairs(),
			deadline:   false,
			expectedInfo: &RequestInfo{
				FullMethod:      "/gitaly.RepositoryService/OptimizeRepository",
				methodType:      "unary",
				clientName:      unknownValue,
				callSite:        unknownValue,
				authVersion:     unknownValue,
				deadlineType:    "none",
				methodOperation: "maintenance",
				methodScope:     "repository",
			},
		},
		{
			desc:       "with storage-scoped accessor",
			fullMethod: "/gitaly.RemoteService/FindRemoteRepository",
			metadata:   metadata.Pairs(),
			deadline:   false,
			expectedInfo: &RequestInfo{
				FullMethod:      "/gitaly.RemoteService/FindRemoteRepository",
				methodType:      "unary",
				clientName:      unknownValue,
				callSite:        unknownValue,
				authVersion:     unknownValue,
				deadlineType:    "none",
				methodOperation: "accessor",
				methodScope:     "storage",
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := metadata.NewIncomingContext(baseContext, tc.metadata)
			if tc.deadline {
				var cancel func()

				ctx, cancel = context.WithDeadline(ctx, time.Now().Add(50*time.Millisecond))
				defer cancel()
			}

			require.Equal(t, tc.expectedInfo, newRequestInfo(ctx, tc.fullMethod, "unary"))
		})
	}
}

func TestGRPCTags(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx = metadata.NewIncomingContext(
		correlation.ContextWithCorrelation(
			correlation.ContextWithClientName(
				ctx,
				clientName,
			),
			correlationID,
		),
		metadata.Pairs(),
	)

	interceptor := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		return handler(ctx, req)
	}

	_, err := interceptor(ctx, nil, nil, func(ctx context.Context, _ interface{}) (interface{}, error) {
		info := newRequestInfo(ctx, "/gitaly.RepositoryService/OptimizeRepository", "unary")

		tags := grpcmwtags.NewTags()

		repoInfo := &gitalypb.Repository{
			StorageName:   "storage",
			RelativePath:  "path",
			GlProjectPath: "glProject",
			GlRepository:  "glRepository",
		}
		info.Repository = repoInfo

		ctx = info.injectTags(ctx, tags)

		require.Equal(t, &RequestInfo{
			correlationID:   correlationID,
			FullMethod:      "/gitaly.RepositoryService/OptimizeRepository",
			methodType:      "unary",
			clientName:      clientName,
			callSite:        "unknown",
			authVersion:     "unknown",
			deadlineType:    "none",
			methodOperation: "maintenance",
			methodScope:     "repository",
			Repository:      repoInfo,
		}, info)

		fields := logging.ExtractFields(ctx)

		require.Equal(t, map[string]any{
			"correlation_id":             correlationID,
			"grpc.meta.client_name":      clientName,
			"grpc.meta.deadline_type":    "none",
			"grpc.meta.method_type":      "unary",
			"grpc.meta.method_operation": "maintenance",
			"grpc.meta.method_scope":     "repository",
			"grpc.request.fullMethod":    "/gitaly.RepositoryService/OptimizeRepository",
			"grpc.request.glProjectPath": "glProject",
			"grpc.request.glRepository":  "glRepository",
			"grpc.request.repoPath":      "path",
			"grpc.request.repoStorage":   "storage",
		}, gitalylog.ConvertLoggingFields(fields))

		legacyFields := grpcmwtags.Extract(ctx).Values()

		require.Equal(t, map[string]any{
			"correlation_id":             correlationID,
			"grpc.meta.client_name":      clientName,
			"grpc.meta.deadline_type":    "none",
			"grpc.meta.method_type":      "unary",
			"grpc.meta.method_operation": "maintenance",
			"grpc.meta.method_scope":     "repository",
			"grpc.request.fullMethod":    "/gitaly.RepositoryService/OptimizeRepository",
			"grpc.request.glProjectPath": "glProject",
			"grpc.request.glRepository":  "glRepository",
			"grpc.request.repoPath":      "path",
			"grpc.request.repoStorage":   "storage",
		}, legacyFields)

		// reject tags again to test no fields is missing
		// this is to regression test https://gitlab.com/gitlab-org/gitaly/-/issues/5794
		info.Repository = nil
		ctx = info.injectTags(ctx, tags)
		fields = logging.ExtractFields(ctx)
		require.Equal(t, map[string]any{
			"correlation_id":             correlationID,
			"grpc.meta.client_name":      clientName,
			"grpc.meta.deadline_type":    "none",
			"grpc.meta.method_type":      "unary",
			"grpc.meta.method_operation": "maintenance",
			"grpc.meta.method_scope":     "repository",
			"grpc.request.fullMethod":    "/gitaly.RepositoryService/OptimizeRepository",
			"grpc.request.glProjectPath": "glProject",
			"grpc.request.glRepository":  "glRepository",
			"grpc.request.repoPath":      "path",
			"grpc.request.repoStorage":   "storage",
		}, gitalylog.ConvertLoggingFields(fields))

		legacyFields = grpcmwtags.Extract(ctx).Values()

		require.Equal(t, map[string]any{
			"correlation_id":             correlationID,
			"grpc.meta.client_name":      clientName,
			"grpc.meta.deadline_type":    "none",
			"grpc.meta.method_type":      "unary",
			"grpc.meta.method_operation": "maintenance",
			"grpc.meta.method_scope":     "repository",
			"grpc.request.fullMethod":    "/gitaly.RepositoryService/OptimizeRepository",
			"grpc.request.glProjectPath": "glProject",
			"grpc.request.glRepository":  "glRepository",
			"grpc.request.repoPath":      "path",
			"grpc.request.repoStorage":   "storage",
		}, legacyFields)

		return nil, nil
	})
	require.NoError(t, err)
}

func TestExtractServiceAndMethodName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc            string
		fullMethodName  string
		expectedService string
		expectedMethod  string
	}{
		{
			desc:            "blank",
			fullMethodName:  "",
			expectedService: unknownValue,
			expectedMethod:  unknownValue,
		},
		{
			desc:            "normal",
			fullMethodName:  "/gitaly.OperationService/method",
			expectedService: "gitaly.OperationService",
			expectedMethod:  "method",
		},
		{
			desc:            "malformed",
			fullMethodName:  "//method",
			expectedService: "",
			expectedMethod:  "method",
		},
		{
			desc:            "malformed",
			fullMethodName:  "/gitaly.OperationService/",
			expectedService: "gitaly.OperationService",
			expectedMethod:  "",
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			info := RequestInfo{
				FullMethod: tc.fullMethodName,
			}

			service, method := info.ExtractServiceAndMethodName()
			require.Equal(t, tc.expectedService, service)
			require.Equal(t, tc.expectedMethod, method)
		})
	}
}

func TestInterceptors(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc         string
		call         func(*testing.T, mockClient)
		expectedInfo *RequestInfo
		expectedTags map[string]any
	}{
		{
			desc: "unary repository-scoped call",
			call: func(t *testing.T, client mockClient) {
				_, err := client.RepositoryInfo(ctx, &gitalypb.RepositoryInfoRequest{
					Repository: &gitalypb.Repository{
						StorageName:   "storage",
						RelativePath:  "path",
						GlProjectPath: "glProject",
						GlRepository:  "glRepository",
					},
				})

				require.NoError(t, err)
			},
			expectedInfo: &RequestInfo{
				clientName:      "unknown",
				callSite:        "unknown",
				authVersion:     "unknown",
				deadlineType:    "none",
				methodOperation: "accessor",
				methodScope:     "repository",
				methodType:      "unary",
				FullMethod:      "/gitaly.RepositoryService/RepositoryInfo",
				Repository: &gitalypb.Repository{
					StorageName:   "storage",
					RelativePath:  "path",
					GlProjectPath: "glProject",
					GlRepository:  "glRepository",
				},
			},
			expectedTags: map[string]any{
				"grpc.meta.deadline_type":    "none",
				"grpc.meta.method_operation": "accessor",
				"grpc.meta.method_scope":     "repository",
				"grpc.meta.method_type":      "unary",
				"grpc.request.fullMethod":    "/gitaly.RepositoryService/RepositoryInfo",
				"grpc.request.repoStorage":   "storage",
				"grpc.request.repoPath":      "path",
				"grpc.request.glProjectPath": "glProject",
				"grpc.request.glRepository":  "glRepository",
			},
		},
		{
			desc: "unary repository-scoped call with unset repository",
			call: func(t *testing.T, client mockClient) {
				_, err := client.RepositoryInfo(ctx, &gitalypb.RepositoryInfoRequest{
					Repository: nil,
				})

				require.NoError(t, err)
			},
			expectedInfo: &RequestInfo{
				clientName:      "unknown",
				callSite:        "unknown",
				authVersion:     "unknown",
				deadlineType:    "none",
				methodOperation: "accessor",
				methodScope:     "repository",
				methodType:      "unary",
				FullMethod:      "/gitaly.RepositoryService/RepositoryInfo",
			},
			expectedTags: map[string]any{
				"grpc.meta.deadline_type":    "none",
				"grpc.meta.method_operation": "accessor",
				"grpc.meta.method_scope":     "repository",
				"grpc.meta.method_type":      "unary",
				"grpc.request.fullMethod":    "/gitaly.RepositoryService/RepositoryInfo",
			},
		},
		{
			desc: "unary object-pool-scoped call",
			call: func(t *testing.T, client mockClient) {
				_, err := client.FetchIntoObjectPool(ctx, &gitalypb.FetchIntoObjectPoolRequest{
					ObjectPool: &gitalypb.ObjectPool{
						Repository: &gitalypb.Repository{
							StorageName:   "storage",
							RelativePath:  "path",
							GlProjectPath: "glProject",
						},
					},
				})

				require.NoError(t, err)
			},
			expectedInfo: &RequestInfo{
				clientName:      "unknown",
				callSite:        "unknown",
				authVersion:     "unknown",
				deadlineType:    "none",
				methodOperation: "mutator",
				methodScope:     "repository",
				methodType:      "unary",
				FullMethod:      "/gitaly.ObjectPoolService/FetchIntoObjectPool",
				objectPool: &gitalypb.ObjectPool{
					Repository: &gitalypb.Repository{
						StorageName:   "storage",
						RelativePath:  "path",
						GlProjectPath: "glProject",
					},
				},
			},
			expectedTags: map[string]any{
				"grpc.meta.deadline_type":             "none",
				"grpc.meta.method_operation":          "mutator",
				"grpc.meta.method_scope":              "repository",
				"grpc.meta.method_type":               "unary",
				"grpc.request.fullMethod":             "/gitaly.ObjectPoolService/FetchIntoObjectPool",
				"grpc.request.pool.relativePath":      "path",
				"grpc.request.pool.storage":           "storage",
				"grpc.request.pool.sourceProjectPath": "glProject",
			},
		},
		{
			desc: "unary repository-scoped call with deadline",
			call: func(t *testing.T, client mockClient) {
				ctx, cancel := context.WithDeadline(ctx, time.Date(2100, time.January, 1, 12, 0, 0, 0, time.UTC))
				defer cancel()

				_, err := client.RepositoryInfo(ctx, &gitalypb.RepositoryInfoRequest{
					Repository: &gitalypb.Repository{
						StorageName:   "storage",
						RelativePath:  "path",
						GlProjectPath: "glProject",
						GlRepository:  "glRepository",
					},
				})

				require.NoError(t, err)
			},
			expectedInfo: &RequestInfo{
				clientName:      "unknown",
				callSite:        "unknown",
				authVersion:     "unknown",
				deadlineType:    "unknown",
				methodOperation: "accessor",
				methodScope:     "repository",
				methodType:      "unary",
				FullMethod:      "/gitaly.RepositoryService/RepositoryInfo",
				Repository: &gitalypb.Repository{
					StorageName:   "storage",
					RelativePath:  "path",
					GlProjectPath: "glProject",
					GlRepository:  "glRepository",
				},
			},
			expectedTags: map[string]any{
				// Note that there is no "deadline: none" field anymore. If we were
				// to inject the deadline type then it would appear here.
				"grpc.meta.method_operation": "accessor",
				"grpc.meta.method_scope":     "repository",
				"grpc.meta.method_type":      "unary",
				"grpc.request.fullMethod":    "/gitaly.RepositoryService/RepositoryInfo",
				"grpc.request.repoStorage":   "storage",
				"grpc.request.repoPath":      "path",
				"grpc.request.glProjectPath": "glProject",
				"grpc.request.glRepository":  "glRepository",
			},
		},
		{
			desc: "unary repository-scoped call with additional metadata",
			call: func(t *testing.T, client mockClient) {
				ctx, cancel := context.WithDeadline(ctx, time.Date(2100, time.January, 1, 12, 0, 0, 0, time.UTC))
				defer cancel()

				ctx = metadata.NewOutgoingContext(ctx, metadata.MD{
					"call_site":           []string{"callSite"},
					"deadline_type":       []string{"deadlineType"},
					"client_name":         []string{"clientName"},
					"remote_ip":           []string{"remoteIP"},
					"user_id":             []string{"userID"},
					"username":            []string{"userName"},
					correlation.FieldName: []string{"correlationID"},
				})

				_, err := client.RepositoryInfo(ctx, &gitalypb.RepositoryInfoRequest{
					Repository: &gitalypb.Repository{
						StorageName:   "storage",
						RelativePath:  "path",
						GlProjectPath: "glProject",
						GlRepository:  "glRepository",
					},
				})

				require.NoError(t, err)
			},
			expectedInfo: &RequestInfo{
				clientName:      "clientName",
				callSite:        "callSite",
				authVersion:     "unknown",
				remoteIP:        "remoteIP",
				userID:          "userID",
				userName:        "userName",
				deadlineType:    "deadlineType",
				methodOperation: "accessor",
				methodScope:     "repository",
				methodType:      "unary",
				FullMethod:      "/gitaly.RepositoryService/RepositoryInfo",
				Repository: &gitalypb.Repository{
					StorageName:   "storage",
					RelativePath:  "path",
					GlProjectPath: "glProject",
					GlRepository:  "glRepository",
				},
			},
			expectedTags: map[string]any{
				"grpc.meta.call_site":        "callSite",
				"grpc.meta.deadline_type":    "deadlineType",
				"grpc.meta.client_name":      "clientName",
				"grpc.meta.method_operation": "accessor",
				"grpc.meta.method_scope":     "repository",
				"grpc.meta.method_type":      "unary",
				"grpc.request.fullMethod":    "/gitaly.RepositoryService/RepositoryInfo",
				"grpc.request.repoStorage":   "storage",
				"grpc.request.repoPath":      "path",
				"grpc.request.glProjectPath": "glProject",
				"grpc.request.glRepository":  "glRepository",
				"remote_ip":                  "remoteIP",
				"user_id":                    "userID",
				"username":                   "userName",
			},
		},
		{
			desc: "streaming repository-scoped call",
			call: func(t *testing.T, client mockClient) {
				stream, err := client.CreateBundleFromRefList(ctx)
				require.NoError(t, err)

				require.NoError(t, stream.Send(&gitalypb.CreateBundleFromRefListRequest{
					Repository: &gitalypb.Repository{
						StorageName:   "storage",
						RelativePath:  "path",
						GlProjectPath: "glProject",
						GlRepository:  "glRepository",
					},
				}))

				_, err = stream.Recv()
				require.Equal(t, err, io.EOF)
			},
			expectedInfo: &RequestInfo{
				clientName:      "unknown",
				callSite:        "unknown",
				authVersion:     "unknown",
				deadlineType:    "none",
				methodOperation: "accessor",
				methodScope:     "repository",
				methodType:      "bidi_stream",
				FullMethod:      "/gitaly.RepositoryService/CreateBundleFromRefList",
				Repository: &gitalypb.Repository{
					StorageName:   "storage",
					RelativePath:  "path",
					GlProjectPath: "glProject",
					GlRepository:  "glRepository",
				},
			},
			expectedTags: map[string]any{
				"grpc.meta.deadline_type":    "none",
				"grpc.meta.method_operation": "accessor",
				"grpc.meta.method_scope":     "repository",
				"grpc.meta.method_type":      "bidi_stream",
				"grpc.request.fullMethod":    "/gitaly.RepositoryService/CreateBundleFromRefList",
				"grpc.request.repoStorage":   "storage",
				"grpc.request.repoPath":      "path",
				"grpc.request.glProjectPath": "glProject",
				"grpc.request.glRepository":  "glRepository",
			},
		},
		{
			desc: "streaming repository-scoped call with missing initial request",
			call: func(t *testing.T, client mockClient) {
				stream, err := client.CreateBundleFromRefList(ctx)
				require.NoError(t, err)
				require.NoError(t, stream.CloseSend())

				_, err = stream.Recv()
				testhelper.RequireGrpcError(t, structerr.New("%w", io.EOF), err)
			},
			expectedInfo: &RequestInfo{
				clientName:      "unknown",
				callSite:        "unknown",
				authVersion:     "unknown",
				deadlineType:    "none",
				methodOperation: "accessor",
				methodScope:     "repository",
				methodType:      "bidi_stream",
				FullMethod:      "/gitaly.RepositoryService/CreateBundleFromRefList",
			},
			expectedTags: map[string]any{
				"grpc.meta.deadline_type":    "none",
				"grpc.meta.method_operation": "accessor",
				"grpc.meta.method_scope":     "repository",
				"grpc.meta.method_type":      "bidi_stream",
				"grpc.request.fullMethod":    "/gitaly.RepositoryService/CreateBundleFromRefList",
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			server, client := setupServer(t, ctx)

			tc.call(t, client)

			require.Equal(t, tc.expectedInfo, server.info)
			if tc.expectedTags == nil {
				require.Equal(t, nil, tc.expectedTags)
			} else {
				require.Equal(t, tc.expectedTags, server.tags)
			}
		})
	}
}

type mockServer struct {
	gitalypb.RepositoryServiceServer
	gitalypb.ObjectPoolServiceServer
	gitalypb.OperationServiceServer
	tags map[string]any
	info *RequestInfo
}

type mockClient struct {
	gitalypb.RepositoryServiceClient
	gitalypb.ObjectPoolServiceClient
	gitalypb.OperationServiceClient
}

func setupServer(tb testing.TB, ctx context.Context) (*mockServer, mockClient) {
	tb.Helper()

	var mockServer mockServer

	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			UnaryInterceptor,
			// This interceptor and the equivalent interceptor for the streaming gRPC calls is responsible
			// for recording the tags that the preceding interceptor has injected.
			func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
				mockServer.info = Extract(ctx)
				tags := logging.ExtractFields(ctx)
				mockServer.tags = gitalylog.ConvertLoggingFields(tags)
				return handler(ctx, req)
			},
		),
		grpc.ChainStreamInterceptor(
			StreamInterceptor,
			func(server any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
				mockServer.info = Extract(stream.Context())
				err := handler(server, stream)
				tags := logging.ExtractFields(stream.Context())
				mockServer.tags = gitalylog.ConvertLoggingFields(tags)
				return err
			},
		),
	)
	tb.Cleanup(server.Stop)
	gitalypb.RegisterRepositoryServiceServer(server, &mockServer)
	gitalypb.RegisterObjectPoolServiceServer(server, &mockServer)
	gitalypb.RegisterOperationServiceServer(server, &mockServer)

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

	return &mockServer, mockClient{
		RepositoryServiceClient: gitalypb.NewRepositoryServiceClient(conn),
		ObjectPoolServiceClient: gitalypb.NewObjectPoolServiceClient(conn),
		OperationServiceClient:  gitalypb.NewOperationServiceClient(conn),
	}
}

func (s *mockServer) RepositoryInfo(ctx context.Context, _ *gitalypb.RepositoryInfoRequest) (*gitalypb.RepositoryInfoResponse, error) {
	return &gitalypb.RepositoryInfoResponse{}, nil
}

func (s *mockServer) FetchIntoObjectPool(ctx context.Context, _ *gitalypb.FetchIntoObjectPoolRequest) (*gitalypb.FetchIntoObjectPoolResponse, error) {
	return &gitalypb.FetchIntoObjectPoolResponse{}, nil
}

func (s *mockServer) UserCommitFiles(stream gitalypb.OperationService_UserCommitFilesServer) error {
	_, err := stream.Recv()
	return err
}

func (s *mockServer) CreateBundleFromRefList(stream gitalypb.RepositoryService_CreateBundleFromRefListServer) error {
	_, err := stream.Recv()
	return err
}
