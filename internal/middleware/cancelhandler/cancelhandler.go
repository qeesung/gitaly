package cancelhandler

import (
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Unary is a unary server interceptor that puts cancel codes on errors
// from canceled contexts.
func Unary(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	resp, err := handler(ctx, req)
	return resp, wrapErr(ctx, err)
}

// Stream is a stream server interceptor that puts cancel codes on errors
// from canceled contexts.
func Stream(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	return wrapErr(stream.Context(), handler(srv, stream))
}

func wrapErr(ctx context.Context, err error) error {
	if err == nil || ctx.Err() == nil {
		return err
	}

	code := codes.Canceled
	if ctx.Err() == context.DeadlineExceeded {
		code = codes.DeadlineExceeded
	}
	return status.Errorf(code, "%v", err)
}
