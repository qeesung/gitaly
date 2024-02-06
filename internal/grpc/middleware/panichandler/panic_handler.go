package panichandler

import (
	"context"
	"runtime/debug"

	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// PanicHandler is a handler that will be called on a grpc panic
type PanicHandler func(methodName string, error interface{})

func toPanicError(grpcMethodName string, r interface{}) error {
	return status.Errorf(codes.Internal, "panic: %v", r)
}

// UnaryPanicHandler creates a new unary server interceptor that handles panics.
func UnaryPanicHandler(logger log.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		defer handleCrash(logger, info.FullMethod, func(grpcMethodName string, r interface{}) {
			err = toPanicError(grpcMethodName, r)
		})

		return handler(ctx, req)
	}
}

// StreamPanicHandler creates a new stream server interceptor that handles panics.
func StreamPanicHandler(logger log.Logger) grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		defer handleCrash(logger, info.FullMethod, func(grpcMethodName string, r interface{}) {
			err = toPanicError(grpcMethodName, r)
		})

		return handler(srv, stream)
	}
}

var additionalHandlers []PanicHandler

// InstallPanicHandler installs additional crash handles for dealing with a panic
func InstallPanicHandler(handler PanicHandler) {
	additionalHandlers = append(additionalHandlers, handler)
}

func handleCrash(logger log.Logger, grpcMethodName string, handler PanicHandler) {
	if r := recover(); r != nil {
		logger.WithFields(log.Fields{
			"error":     r,
			"method":    grpcMethodName,
			"backtrace": string(debug.Stack()),
		}).Error("grpc panic")

		handler(grpcMethodName, r)

		for _, fn := range additionalHandlers {
			fn(grpcMethodName, r)
		}
	}
}
