package featureflag

import (
	"context"
	"net"
	"testing"

	grpcmwlogrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/loghandler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/interop/grpc_testing"
)

type mockService struct {
	grpc_testing.UnimplementedTestServiceServer
	err error
}

func (m *mockService) UnaryCall(
	context.Context, *grpc_testing.SimpleRequest,
) (*grpc_testing.SimpleResponse, error) {
	return &grpc_testing.SimpleResponse{}, m.err
}

// This test doesn't use testhelper.NewFeatureSets intentionally.
func TestFeatureFlagLogs(t *testing.T) {
	logger := testhelper.NewLogger(t)
	loggerHook := testhelper.AddLoggerHook(logger)

	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	service := &mockService{}
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			grpcmwlogrus.UnaryServerInterceptor(
				logger.LogrusEntry(), //nolint:staticcheck
				grpcmwlogrus.WithMessageProducer(
					loghandler.MessageProducer(
						grpcmwlogrus.DefaultMessageProducer,
						FieldsProducer,
					),
				),
			),
		),
	)
	grpc_testing.RegisterTestServiceServer(server, service)

	go testhelper.MustServe(t, server, listener)
	defer server.Stop()

	conn, err := grpc.Dial(listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	defer testhelper.MustClose(t, conn)

	client := grpc_testing.NewTestServiceClient(conn)

	featureA := featureflag.FeatureFlag{Name: "feature_a"}
	featureB := featureflag.FeatureFlag{Name: "feature_b"}
	featureC := featureflag.FeatureFlag{Name: "feature_c"}
	testCases := []struct {
		desc           string
		featureFlags   map[featureflag.FeatureFlag]bool
		returnedErr    error
		expectedFields string
	}{
		{
			desc: "some feature flags are enabled in successful RPC",
			featureFlags: map[featureflag.FeatureFlag]bool{
				featureC: true,
				featureA: true,
				featureB: false,
			},
			expectedFields: "feature_a feature_c",
		},
		{
			desc: "no feature flags are enabled in successful RPC",
			featureFlags: map[featureflag.FeatureFlag]bool{
				featureC: false,
				featureA: false,
				featureB: false,
			},
			expectedFields: "",
		},
		{
			desc: "some feature flags are enabled in failed RPC",
			featureFlags: map[featureflag.FeatureFlag]bool{
				featureC: true,
				featureA: true,
				featureB: false,
			},
			returnedErr:    structerr.NewInternal("something goes wrong"),
			expectedFields: "feature_a feature_c",
		},
		{
			desc: "no feature flags are enabled in failed RPC",
			featureFlags: map[featureflag.FeatureFlag]bool{
				featureC: false,
				featureA: false,
				featureB: false,
			},
			returnedErr:    structerr.NewInternal("something goes wrong"),
			expectedFields: "",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			loggerHook.Reset()
			service.err = tc.returnedErr

			// This test tests feature flags. We want context to be in a clean state and thus cannot use
			// `testhelper.Context()`.
			ctx := context.Background()
			for flag, value := range tc.featureFlags {
				ctx = featureflag.OutgoingCtxWithFeatureFlag(ctx, flag, value)
			}

			_, err := client.UnaryCall(ctx, &grpc_testing.SimpleRequest{})
			testhelper.RequireGrpcError(t, tc.returnedErr, err)

			for _, logEntry := range loggerHook.AllEntries() {
				if tc.expectedFields == "" {
					require.NotContains(t, logEntry.Data, "feature_flags")
				} else {
					require.Equal(t, tc.expectedFields, logEntry.Data["feature_flags"])
				}
			}
		})
	}
}
