package limithandler

import (
	"sync"

	"github.com/grpc-ecosystem/go-grpc-middleware/tags"
	"golang.org/x/net/context"
	"golang.org/x/sync/semaphore"
	"google.golang.org/grpc"
)

const perRepoConcurrencyLimit = 50

type limitedFunc func() (resp interface{}, err error)

// Limiter contains rate limiter state
type Limiter struct {
	v   map[string]*semaphore.Weighted
	mux sync.Mutex
}

func (c *Limiter) getSemaphoreForRepoPath(repoPath string, max int64) *semaphore.Weighted {
	c.mux.Lock()
	defer c.mux.Unlock()

	s := c.v[repoPath]
	if s != nil {
		return s
	}

	s = semaphore.NewWeighted(max)
	c.v[repoPath] = s
	return s
}

func (c *Limiter) attemptCollect(repoPath string, w *semaphore.Weighted, max int64) {
	if !w.TryAcquire(max) {
		return
	}

	c.mux.Lock()
	defer c.mux.Unlock()

	delete(c.v, repoPath)
}

func (c *Limiter) limit(ctx context.Context, repoPath string, f limitedFunc) (interface{}, error) {
	w := c.getSemaphoreForRepoPath(repoPath, perRepoConcurrencyLimit)
	err := w.Acquire(ctx, 1)

	if err != nil {
		return nil, err
	}

	defer w.Release(1)

	c.attemptCollect(repoPath, w, perRepoConcurrencyLimit)

	i, err := f()

	return i, err
}

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
func (c *Limiter) UnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		repoPath := getRepoPath(ctx)
		if repoPath == "" {
			return handler(ctx, req)
		}

		return c.limit(ctx, repoPath, func() (interface{}, error) {
			return handler(ctx, req)
		})
	}
}

// StreamInterceptor returns a Stream Interceptor
func (c *Limiter) StreamInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := stream.Context()

		repoPath := getRepoPath(ctx)
		if repoPath == "" {
			return handler(srv, stream)
		}

		_, err := c.limit(ctx, repoPath, func() (interface{}, error) {
			err := handler(srv, stream)
			return nil, err
		})

		return err
	}
}

// New creates a new rate limiter
func New() Limiter {
	return Limiter{v: make(map[string]*semaphore.Weighted)}
}
