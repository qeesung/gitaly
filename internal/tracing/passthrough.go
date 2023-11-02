package tracing

import (
	"context"
	"strings"

	grpcmwmetadata "github.com/grpc-ecosystem/go-grpc-middleware/v2/metadata"
	"github.com/opentracing/opentracing-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// ExtractSpanContextFromEnv extracts a SpanContext from the environment variable list. The caller
// usually passes the result of os.Environ() into this method.
func ExtractSpanContextFromEnv(envs []string) (opentracing.SpanContext, error) {
	envMap := environAsMap(envs)
	return opentracing.GlobalTracer().Extract(
		opentracing.TextMap,
		opentracing.TextMapCarrier(envMap),
	)
}

// UnaryPassthroughInterceptor is a client gRPC unary interceptor that rewrites a span context into
// the outgoing metadata of the call. It is useful for intermediate systems who don't want to
// start new spans.
func UnaryPassthroughInterceptor(spanContext opentracing.SpanContext) grpc.UnaryClientInterceptor {
	return func(parentCtx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctxWithMetadata := injectSpanContext(parentCtx, spanContext)
		return invoker(ctxWithMetadata, method, req, reply, cc, opts...)
	}
}

// StreamPassthroughInterceptor is equivalent to UnaryPassthroughInterceptor, but for streaming
// gRPC calls.
func StreamPassthroughInterceptor(spanContext opentracing.SpanContext) grpc.StreamClientInterceptor {
	return func(parentCtx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctxWithMetadata := injectSpanContext(parentCtx, spanContext)
		return streamer(ctxWithMetadata, desc, cc, method, opts...)
	}
}

func injectSpanContext(parentCtx context.Context, spanContext opentracing.SpanContext) context.Context {
	tracer := opentracing.GlobalTracer()
	md := grpcmwmetadata.ExtractOutgoing(parentCtx).Clone()
	if err := tracer.Inject(spanContext, opentracing.HTTPHeaders, metadataTextMap(md)); err != nil {
		return parentCtx
	}
	ctxWithMetadata := md.ToOutgoing(parentCtx)
	return ctxWithMetadata
}

func environAsMap(env []string) map[string]string {
	envMap := make(map[string]string, len(env))
	for _, v := range env {
		s := strings.SplitN(v, "=", 2)
		envMap[s[0]] = s[1]
	}
	return envMap
}

// metadataTextMap is a wrapper for gRPC's metadata.MD. It implements opentracing.TextMapWriter,
// which is to set opentracing-related fields. In this use case, the passthrough interceptors touch
// span identify and maybe some luggage or tag fields. This implementation is good-enough for such
// fields. gRPC header name format is:
// > Header-Name → 1*( %x30-39 / %x61-7A / "_" / "-" / ".") ; 0-9 a-z _ - .
// > Source: https://github.com/grpc/grpc/blob/master/doc/PROTOCOL-HTTP2.md
type metadataTextMap metadata.MD

func (m metadataTextMap) Set(key, val string) {
	m[strings.ToLower(key)] = []string{val}
}
