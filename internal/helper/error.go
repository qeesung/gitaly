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

func GrpcCode(err error) codes.Code {
	if err == nil {
		return codes.OK
	}

	st := status.FromError(err)
	return st.Code()
}
