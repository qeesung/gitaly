package statushandler

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/interop/stress/grpc_testing"
	"google.golang.org/grpc/status"
)

func TestStatushandler(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()
	timeoutCtx, timeout := context.WithTimeout(ctx, 0)
	timeout()

	for desc, tc := range map[string]struct {
		ctx         context.Context
		err         error
		expectedErr error
	}{
		"context cancelled": {
			ctx: cancelledCtx,
		},
		"context timeout": {
			ctx: timeoutCtx,
		},
		"context cancelled with an error returned": {
			ctx:         cancelledCtx,
			err:         assert.AnError,
			expectedErr: status.Error(codes.Canceled, assert.AnError.Error()),
		},
		"context cancelled with structured error": {
			ctx:         cancelledCtx,
			err:         structerr.NewInternal("%w", assert.AnError),
			expectedErr: status.Error(codes.Canceled, assert.AnError.Error()),
		},
		"context cancelled with structured error details": {
			ctx:         cancelledCtx,
			err:         structerr.NewInternal("error").WithDetail(&grpc_testing.EmptyMessage{}),
			expectedErr: structerr.NewCanceled("error").WithDetail(&grpc_testing.EmptyMessage{}),
		},
		"context cancelled with gRPC error": {
			ctx:         cancelledCtx,
			err:         status.Error(codes.Internal, assert.AnError.Error()),
			expectedErr: status.Error(codes.Canceled, assert.AnError.Error()),
		},
		"context canceled with wrapped error": {
			ctx:         cancelledCtx,
			err:         fmt.Errorf("cause: %w", structerr.NewInvalidArgument("%w", assert.AnError)),
			expectedErr: status.Error(codes.Canceled, "cause: "+assert.AnError.Error()),
		},
		"context timed out with an error returned": {
			ctx:         timeoutCtx,
			err:         assert.AnError,
			expectedErr: status.Error(codes.DeadlineExceeded, assert.AnError.Error()),
		},
		"context timed out with structured error": {
			ctx:         timeoutCtx,
			err:         structerr.NewInternal("%w", assert.AnError),
			expectedErr: status.Error(codes.DeadlineExceeded, assert.AnError.Error()),
		},
		"context timed out with structured error details": {
			ctx:         timeoutCtx,
			err:         structerr.NewInternal("error").WithDetail(&grpc_testing.EmptyMessage{}),
			expectedErr: structerr.NewDeadlineExceeded("error").WithDetail(&grpc_testing.EmptyMessage{}),
		},
		"context timed out with gRPC error": {
			ctx:         timeoutCtx,
			err:         status.Error(codes.Internal, assert.AnError.Error()),
			expectedErr: status.Error(codes.DeadlineExceeded, assert.AnError.Error()),
		},
		"context timed out with wrapped error": {
			ctx:         timeoutCtx,
			err:         fmt.Errorf("cause: %w", structerr.NewInvalidArgument("%w", assert.AnError)),
			expectedErr: status.Error(codes.DeadlineExceeded, "cause: "+assert.AnError.Error()),
		},
		"bare error": {
			ctx:         ctx,
			err:         assert.AnError,
			expectedErr: status.Error(codes.Internal, assert.AnError.Error()),
		},
		"wrapped error": {
			ctx:         ctx,
			err:         structerr.NewInvalidArgument("%w", assert.AnError),
			expectedErr: status.Error(codes.InvalidArgument, assert.AnError.Error()),
		},
		"formatted wrapped error": {
			ctx:         ctx,
			err:         fmt.Errorf("cause: %w", structerr.NewInvalidArgument("%w", assert.AnError)),
			expectedErr: status.Error(codes.InvalidArgument, "cause: "+assert.AnError.Error()),
		},
		"cancelled error": {
			ctx:         ctx,
			err:         context.Canceled,
			expectedErr: status.Error(codes.Internal, context.Canceled.Error()),
		},
		"timeout error": {
			ctx:         ctx,
			err:         context.DeadlineExceeded,
			expectedErr: status.Error(codes.Internal, context.DeadlineExceeded.Error()),
		},
		"no errors": {
			ctx:         ctx,
			expectedErr: status.New(codes.OK, "").Err(),
		},
	} {
		tc := tc

		t.Run(desc, func(t *testing.T) {
			t.Parallel()

			t.Run("unary", func(t *testing.T) {
				_, err := Unary(tc.ctx, nil, nil, func(context.Context, interface{}) (interface{}, error) {
					return nil, tc.err
				})
				testhelper.RequireGrpcError(t, tc.expectedErr, err)
			})

			t.Run("stream", func(t *testing.T) {
				err := Stream(nil, serverStream{ctx: tc.ctx}, nil, func(srv interface{}, stream grpc.ServerStream) error {
					return tc.err
				})
				testhelper.RequireGrpcError(t, tc.expectedErr, err)
			})
		})
	}
}

type serverStream struct {
	ctx context.Context
	grpc.ServerStream
}

func (ss serverStream) Context() context.Context {
	return ss.ctx
}
