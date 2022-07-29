//go:build !gitaly_test_sha256

package helper

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestError(t *testing.T) {
	errorMessage := "sentinel error"
	input := errors.New(errorMessage)
	inputGRPCCode := codes.Unauthenticated
	inputGRPC := status.Error(inputGRPCCode, errorMessage)

	for _, tc := range []struct {
		desc   string
		errorf func(err error) error
		code   codes.Code
	}{
		{
			desc:   "Canceled",
			errorf: ErrCanceled,
			code:   codes.Canceled,
		},
		{
			desc:   "DeadlineExceeded",
			errorf: ErrDeadlineExceeded,
			code:   codes.DeadlineExceeded,
		},
		{
			desc:   "Internal",
			errorf: ErrInternal,
			code:   codes.Internal,
		},
		{
			desc:   "InvalidArgument",
			errorf: ErrInvalidArgument,
			code:   codes.InvalidArgument,
		},
		{
			desc:   "FailedPrecondition",
			errorf: ErrFailedPrecondition,
			code:   codes.FailedPrecondition,
		},
		{
			desc:   "NotFound",
			errorf: ErrNotFound,
			code:   codes.NotFound,
		},
		{
			desc:   "Unavailable",
			errorf: ErrUnavailable,
			code:   codes.Unavailable,
		},
		{
			desc:   "AlreadyExists",
			errorf: ErrAlreadyExists,
			code:   codes.AlreadyExists,
		},
		{
			desc:   "Aborted",
			errorf: ErrAborted,
			code:   codes.Aborted,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			// tc.code and our canary test code must not
			// clash!
			require.NotEqual(t, tc.code, inputGRPCCode)

			// When not re-throwing an error we get the
			// GRPC error code corresponding to the
			// function's name.
			err := tc.errorf(input)
			require.EqualError(t, err, errorMessage)
			require.False(t, errors.Is(err, inputGRPC))
			require.Equal(t, tc.code, status.Code(err))

			// When re-throwing an error an existing GRPC
			// error code will get preserved, instead of
			// the one corresponding to the function's
			// name.
			err = tc.errorf(inputGRPC)
			require.True(t, errors.Is(err, inputGRPC))
			require.False(t, errors.Is(err, input))
			require.Equal(t, inputGRPCCode, status.Code(err))
			require.NotEqual(t, tc.code, status.Code(inputGRPC))
		})
	}
}

func TestErrorf(t *testing.T) {
	for _, tc := range []struct {
		desc         string
		errorf       func(format string, a ...interface{}) error
		expectedCode codes.Code
	}{
		{
			desc:         "Canceledf",
			errorf:       ErrCanceledf,
			expectedCode: codes.Canceled,
		},
		{
			desc:         "DeadlineExceededf",
			errorf:       ErrDeadlineExceededf,
			expectedCode: codes.DeadlineExceeded,
		},
		{
			desc:         "Internalf",
			errorf:       ErrInternalf,
			expectedCode: codes.Internal,
		},
		{
			desc:         "InvalidArgumentf",
			errorf:       ErrInvalidArgumentf,
			expectedCode: codes.InvalidArgument,
		},
		{
			desc:         "FailedPreconditionf",
			errorf:       ErrFailedPreconditionf,
			expectedCode: codes.FailedPrecondition,
		},
		{
			desc:         "NotFoundf",
			errorf:       ErrNotFoundf,
			expectedCode: codes.NotFound,
		},
		{
			desc:         "ErrUnavailablef",
			errorf:       ErrUnavailablef,
			expectedCode: codes.Unavailable,
		},
		{
			desc:         "ErrAbortedf",
			errorf:       ErrAbortedf,
			expectedCode: codes.Aborted,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			t.Run("with non-gRPC error", func(t *testing.T) {
				err := tc.errorf("top-level: %w", errors.New("nested"))
				require.EqualError(t, err, "top-level: nested")
				require.Equal(t, tc.expectedCode, status.Code(err))

				s, ok := status.FromError(err)
				require.True(t, ok)
				require.Equal(t, status.New(tc.expectedCode, "top-level: nested"), s)
			})

			t.Run("with status.Errorf error and %v", func(t *testing.T) {
				require.NotEqual(t, tc.expectedCode, codes.Unauthenticated)

				err := tc.errorf("top-level: %v", status.Errorf(codes.Unauthenticated, "deeply: %s", "nested"))
				require.EqualError(t, err, "top-level: deeply: nested")

				// The error code of the nested error should be discarded.
				require.Equal(t, tc.expectedCode, status.Code(err))
				s, ok := status.FromError(err)
				require.True(t, ok)
				require.Equal(t, status.New(tc.expectedCode, "top-level: deeply: nested"), s)
			})

			t.Run("with status.Errorf error and %w", func(t *testing.T) {
				require.NotEqual(t, tc.expectedCode, codes.Unauthenticated)

				err := tc.errorf("top-level: %w", status.Errorf(codes.Unauthenticated, "deeply: %s", "nested"))
				require.EqualError(t, err, "top-level: deeply: nested")

				// We should be reporting the error code of the nested error.
				require.Equal(t, codes.Unauthenticated, status.Code(err))
				s, ok := status.FromError(err)
				require.True(t, ok)
				require.Equal(t, status.New(codes.Unauthenticated, "top-level: deeply: nested"), s)
			})

			t.Run("with status.Error error and %v", func(t *testing.T) {
				require.NotEqual(t, tc.expectedCode, codes.Unauthenticated)

				err := tc.errorf("top-level: %v", status.Error(codes.Unauthenticated, "nested"))
				require.EqualError(t, err, "top-level: nested")

				// The error code of the nested error should be discarded.
				require.Equal(t, tc.expectedCode, status.Code(err))
				s, ok := status.FromError(err)
				require.True(t, ok)
				require.Equal(t, status.New(tc.expectedCode, "top-level: nested"), s)
			})

			t.Run("with status.Error error and %w", func(t *testing.T) {
				require.NotEqual(t, tc.expectedCode, codes.Unauthenticated)

				err := tc.errorf("top-level: %w", status.Error(codes.Unauthenticated, "nested"))
				require.EqualError(t, err, "top-level: nested")

				// We should be reporting the error code of the nested error.
				require.Equal(t, codes.Unauthenticated, status.Code(err))
				s, ok := status.FromError(err)
				require.True(t, ok)
				require.Equal(t, status.New(codes.Unauthenticated, "top-level: nested"), s)
			})

			t.Run("multi-nesting gRPC errors", func(t *testing.T) {
				require.NotEqual(t, tc.expectedCode, codes.Unauthenticated)

				err := tc.errorf("first: %w",
					ErrInternalf("second: %w",
						status.Error(codes.Unauthenticated, "third"),
					),
				)
				require.EqualError(t, err, "first: second: third")

				// We should be reporting the error code of the nested error.
				require.Equal(t, codes.Unauthenticated, status.Code(err))
				s, ok := status.FromError(err)
				require.True(t, ok)
				require.Equal(t, status.New(codes.Unauthenticated, "first: second: third"), s)
			})
		})
	}
}

func TestErrWithDetails(t *testing.T) {
	for _, tc := range []struct {
		desc            string
		err             error
		details         []proto.Message
		expectedErr     error
		expectedMessage string
		expectedDetails []proto.Message
		expectedCode    codes.Code
	}{
		{
			desc:        "no error",
			expectedErr: errors.New("no error given"),
		},
		{
			desc:        "status with OK code",
			err:         status.Error(codes.OK, "message"),
			expectedErr: errors.New("no error given"),
		},
		{
			desc:        "normal error",
			err:         errors.New("message"),
			expectedErr: errors.New("error is not a gRPC status"),
		},
		{
			desc:            "status",
			err:             status.Error(codes.FailedPrecondition, "message"),
			expectedMessage: "rpc error: code = FailedPrecondition desc = message",
			expectedCode:    codes.FailedPrecondition,
		},
		{
			desc: "status with details",
			err:  status.Error(codes.FailedPrecondition, "message"),
			details: []proto.Message{
				&gitalypb.Repository{},
			},
			expectedMessage: "rpc error: code = FailedPrecondition desc = message",
			expectedCode:    codes.FailedPrecondition,
			expectedDetails: []proto.Message{
				&gitalypb.Repository{},
			},
		},
		{
			desc: "status with multiple details",
			err:  status.Error(codes.FailedPrecondition, "message"),
			details: []proto.Message{
				&gitalypb.Repository{RelativePath: "a"},
				&gitalypb.Repository{RelativePath: "b"},
			},
			expectedMessage: "rpc error: code = FailedPrecondition desc = message",
			expectedCode:    codes.FailedPrecondition,
			expectedDetails: []proto.Message{
				&gitalypb.Repository{RelativePath: "a"},
				&gitalypb.Repository{RelativePath: "b"},
			},
		},
		{
			desc: "status with mixed type details",
			err:  status.Error(codes.FailedPrecondition, "message"),
			details: []proto.Message{
				&gitalypb.Repository{},
				&gitalypb.GitCommit{},
			},
			expectedMessage: "rpc error: code = FailedPrecondition desc = message",
			expectedCode:    codes.FailedPrecondition,
			expectedDetails: []proto.Message{
				&gitalypb.Repository{},
				&gitalypb.GitCommit{},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			detailedErr, err := ErrWithDetails(tc.err, tc.details...)
			require.Equal(t, tc.expectedErr, err)
			if err != nil {
				return
			}

			require.Equal(t, tc.expectedMessage, detailedErr.Error())

			st, ok := status.FromError(detailedErr)
			require.True(t, ok, "error should be a status")
			require.Equal(t, tc.expectedCode, st.Code())

			statusProto := st.Proto()
			require.NotNil(t, statusProto)

			var details []proto.Message
			for _, detail := range statusProto.GetDetails() {
				detailProto, err := detail.UnmarshalNew()
				require.NoError(t, err)
				details = append(details, detailProto)
			}
			testhelper.ProtoEqual(t, tc.expectedDetails, details)
		})
	}
}
