package helper

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Unimplemented is a Go error with gRPC error code 'Unimplemented'
var Unimplemented = status.Errorf(codes.Unimplemented, "this rpc is not implemented")

// DecorateError unless it's already a grpc error.
//  If given nil it will return nil.
func DecorateError(code codes.Code, err error) error {
	if err != nil && GrpcCode(err) == codes.Unknown {
		return status.Errorf(code, "%v", err)
	}
	return err
}

// ErrInternal wrappes err with codes.Internal, unless err is already a grpc error
func ErrInternal(err error) error { return DecorateError(codes.Internal, err) }

// ErrInvalidArgument wrappes err with codes.InvalidArgument, unless err is already a grpc error
func ErrInvalidArgument(err error) error { return DecorateError(codes.InvalidArgument, err) }

// ErrPreconditionFailed wraps error with codes.FailedPrecondition, unless err is already a grpc error
func ErrPreconditionFailed(err error) error { return DecorateError(codes.FailedPrecondition, err) }

// ErrNotFound wraps error with codes.NotFound, unless err is already a grpc error
func ErrNotFound(err error) error { return DecorateError(codes.NotFound, err) }

// GrpcCode emulates the old grpc.Code function: it translates errors into codes.Code values.
func GrpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}

	st, ok := status.FromError(err)
	if !ok {
		return codes.Unknown
	}

	return st.Code()
}
