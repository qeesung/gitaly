package testserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	grpcmwlogrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/loghandler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb/testproto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/interop/grpc_testing"
	"google.golang.org/grpc/status"
)

func TestInterceptedError(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc        string
		err         error
		expectedErr error
	}{
		{
			desc:        "normal error",
			err:         fmt.Errorf("test"),
			expectedErr: fmt.Errorf("test"),
		},
		{
			desc:        "structured error",
			err:         structerr.NewNotFound("not found"),
			expectedErr: structerr.NewNotFound("not found"),
		},
		{
			desc:        "wrapped structured error",
			err:         fmt.Errorf("wrapped: %w", structerr.NewNotFound("not found")),
			expectedErr: fmt.Errorf("wrapped: %w", structerr.NewNotFound("not found")),
		},
		{
			desc: "metadata",
			err:  structerr.NewNotFound("not found").WithMetadata("key", "value"),
			expectedErr: structerr.NewNotFound("not found").WithDetail(
				&testproto.ErrorMetadata{
					Key:   []byte("key"),
					Value: []byte("value"),
				},
			),
		},
		{
			desc: "wrapped error with metadata",
			err:  fmt.Errorf("wrapped: %w", structerr.NewNotFound("not found").WithMetadata("key", "value")),
			expectedErr: structerr.NewNotFound("wrapped: not found").WithDetail(
				&testproto.ErrorMetadata{
					Key:   []byte("key"),
					Value: []byte("value"),
				},
			),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			err := interceptedError(tc.err)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

type mockService struct {
	grpc_testing.UnimplementedTestServiceServer
	err error
}

func (m *mockService) UnaryCall(
	context.Context, *grpc_testing.SimpleRequest,
) (*grpc_testing.SimpleResponse, error) {
	return &grpc_testing.SimpleResponse{}, m.err
}

func TestFieldsProducer(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	logger := testhelper.NewLogger(t)
	loggerHook := testhelper.AddLoggerHook(logger)

	listener, err := net.Listen("tcp", "localhost:0")
	require.NoError(t, err)

	service := &mockService{}
	server := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			logger.UnaryServerInterceptor(
				grpcmwlogrus.WithMessageProducer(
					loghandler.MessageProducer(
						grpcmwlogrus.DefaultMessageProducer,
						structerr.FieldsProducer,
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

	for _, tc := range []struct {
		desc             string
		returnedErr      error
		expectedErr      error
		expectedMetadata []map[string]any
	}{
		{
			desc:        "no error",
			returnedErr: nil,
		},
		{
			desc:        "plain error",
			returnedErr: errors.New("message"),
			expectedErr: status.Error(codes.Unknown, "message"),
		},
		{
			desc:        "structured error",
			returnedErr: structerr.New("message"),
			expectedErr: status.Error(codes.Unknown, "message"),
		},
		{
			desc:        "structured error with metadata",
			returnedErr: structerr.New("message").WithMetadata("key", "value"),
			expectedErr: status.Error(codes.Unknown, "message"),
			expectedMetadata: []map[string]any{
				{
					"key": "value",
				},
			},
		},
		{
			desc:        "structured error with nested metadata",
			returnedErr: structerr.New("message: %w", structerr.New("nested").WithMetadata("nested", "value")).WithMetadata("key", "value"),
			expectedErr: status.Error(codes.Unknown, "message: nested"),
			expectedMetadata: []map[string]any{
				{
					"key":    "value",
					"nested": "value",
				},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			loggerHook.Reset()

			service.err = tc.returnedErr

			_, err := client.UnaryCall(ctx, &grpc_testing.SimpleRequest{})
			testhelper.ProtoEqual(t, tc.expectedErr, err)

			var metadata []map[string]any
			for _, entry := range loggerHook.AllEntries() {
				if untypedMetadata := entry.Data["error_metadata"]; untypedMetadata != nil {
					require.IsType(t, untypedMetadata, map[string]any{})
					metadata = append(metadata, untypedMetadata.(map[string]any))
				}
			}
			require.Equal(t, tc.expectedMetadata, metadata)
		})
	}
}
