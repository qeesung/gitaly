package limithandler

import (
	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// LimiterMiddleware contains rate limiter state
type LimiterMiddleware struct {
	methodLimiters map[string]*ConcurrencyLimiter
}

var maxConcurrencyPerRepoPerRPC map[string]int

func getRepoPath(ctx context.Context) string {
	tags := grpc_ctxtags.Extract(ctx)
	ctxValue := tags.Values()["grpc.request.repoPath"]
	if ctxValue == nil {
		return ""
	}

	s, ok := ctxValue.(string)
	if ok {
		return s
	}

	return ""
}

// UnaryInterceptor returns a Unary Interceptor
func (c *LimiterMiddleware) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		repoPath := getRepoPath(ctx)
		if repoPath == "" {
			return handler(ctx, req)
		}

		limiter := c.methodLimiters[info.FullMethod]
		if limiter == nil {
			// No concurrency limiting
			return handler(ctx, req)
		}

		return limiter.Limit(ctx, repoPath, func() (interface{}, error) {
			return handler(ctx, req)
		})
	}
}

// StreamInterceptor returns a Stream Interceptor
func (c *LimiterMiddleware) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := stream.Context()

		repoPath := getRepoPath(ctx)
		if repoPath == "" {
			return handler(srv, stream)
		}

		limiter := c.methodLimiters[info.FullMethod]
		if limiter == nil {
			// No concurrency limiting
			return handler(srv, stream)
		}

		_, err := limiter.Limit(ctx, repoPath, func() (interface{}, error) {
			return nil, handler(srv, stream)
		})

		return err
	}
}

// New creates a new rate limiter
func New() LimiterMiddleware {
	return LimiterMiddleware{
		methodLimiters: createLimiterConfig(),
	}
}

func createLimiterConfig() map[string]*ConcurrencyLimiter {
	result := make(map[string]*ConcurrencyLimiter)

	for fullMethodName, max := range maxConcurrencyPerRepoPerRPC {
		result[fullMethodName] = NewLimiter(max, newPromMonitor(fullMethodName))
	}

	return result
}

// SetMaxRepoConcurrency Configures the max concurrency per repo per RPC
func SetMaxRepoConcurrency(config map[string]int) {
	maxConcurrencyPerRepoPerRPC = config
}
