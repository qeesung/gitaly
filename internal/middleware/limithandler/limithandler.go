package limithandler

import (
	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
)

// LimiterMiddleware contains rate limiter state
type LimiterMiddleware struct {
	rpcLimiterConfigs map[string]*rpcLimiterConfig
}

type rpcLimiterConfig struct {
	limiter ConcurrencyLimiter
	max     int
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

		config := c.rpcLimiterConfigs[info.FullMethod]
		if config == nil {
			// No concurrency limiting
			return handler(ctx, req)
		}

		return config.limiter.Limit(ctx, repoPath, config.max, func() (interface{}, error) {
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

		config := c.rpcLimiterConfigs[info.FullMethod]
		if config == nil {
			// No concurrency limiting
			return handler(srv, stream)
		}

		_, err := config.limiter.Limit(ctx, repoPath, config.max, func() (interface{}, error) {
			err := handler(srv, stream)
			return nil, err
		})

		return err
	}
}

// New creates a new rate limiter
func New() LimiterMiddleware {
	return LimiterMiddleware{
		rpcLimiterConfigs: createLimiterConfig(),
	}
}

func createLimiterConfig() map[string]*rpcLimiterConfig {
	m := make(map[string]*rpcLimiterConfig)
	for k, v := range maxConcurrencyPerRepoPerRPC {
		m[k] = &rpcLimiterConfig{limiter: NewLimiter(newPromMonitor(k)), max: v}
	}

	return m
}

// SetMaxRepoConcurrency Configures the max concurrency per repo per RPC
func SetMaxRepoConcurrency(config map[string]int) {
	maxConcurrencyPerRepoPerRPC = config
}
