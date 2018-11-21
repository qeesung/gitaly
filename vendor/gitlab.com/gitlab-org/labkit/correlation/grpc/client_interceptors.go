package grpccorrelation

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"gitlab.com/gitlab-org/labkit/correlation"
)

func injectFromContext(ctx context.Context) context.Context {
	correlationID := correlation.ExtractFromContext(ctx)
	if correlationID != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, metadataCorrelatorKey, correlationID)
	}

	return ctx
}

// UnaryClientCorrelationInterceptor propagates Correlation-IDs downstream
func UnaryClientCorrelationInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = injectFromContext(ctx)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// StreamClientCorrelationInterceptor propagates Correlation-IDs downstream
func StreamClientCorrelationInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = injectFromContext(ctx)
		return streamer(ctx, desc, cc, method, opts...)
	}
}
