package grpccorrelation

import (
	"context"

	"github.com/grpc-ecosystem/go-grpc-middleware"
	"gitlab.com/gitlab-org/labkit/correlation"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func extractFromContext(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	values := md.Get(metadataCorrelatorKey)
	if len(values) < 1 {
		return ctx
	}

	return correlation.ContextWithCorrelation(ctx, values[0])
}

// UnaryServerCorrelationInterceptor propagates Correlation-IDs from incoming upstream services
func UnaryServerCorrelationInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		ctx = extractFromContext(ctx)
		return handler(ctx, req)
	}
}

// StreamServerCorrelationInterceptor propagates Correlation-IDs from incoming upstream services
func StreamServerCorrelationInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		wrapped := grpc_middleware.WrapServerStream(ss)
		wrapped.WrappedContext = extractFromContext(ss.Context())

		return handler(srv, wrapped)
	}
}
