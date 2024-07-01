package cache

import (
	"context"
	"fmt"
	"strings"
	"sync"

	diskcache "gitlab.com/gitlab-org/gitaly/v16/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

func methodErrLogger(logger log.Logger, method string) func(error) {
	return func(err error) {
		countMethodErr(method)
		logger.WithField("full_method_name", method).Error(err.Error())
	}
}

func shouldIgnore(reg *protoregistry.Registry, fullMethod string) bool {
	return strings.HasPrefix(fullMethod, "/grpc.health") || reg.IsInterceptedMethod(fullMethod)
}

func shouldInvalidate(mi protoregistry.MethodInfo) bool {
	if mi.Scope != protoregistry.ScopeRepository {
		return false
	}

	if mi.Operation == protoregistry.OpAccessor {
		return false
	}

	if mi.Operation == protoregistry.OpMaintenance {
		return false
	}

	return true
}

// StreamInvalidator will invalidate any mutating RPC that targets a
// repository in a gRPC stream based RPC
func StreamInvalidator(ci diskcache.Invalidator, reg *protoregistry.Registry, logger log.Logger) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		if shouldIgnore(reg, info.FullMethod) {
			return handler(srv, ss)
		}

		errLogger := methodErrLogger(logger, info.FullMethod)

		mInfo, err := reg.LookupMethod(info.FullMethod)
		countRPCType(mInfo)
		if err != nil {
			errLogger(err)
			return handler(srv, ss)
		}

		if !shouldInvalidate(mInfo) {
			return handler(srv, ss)
		}

		handler, callback := invalidateCache(ci, mInfo, handler, errLogger)
		peeker := newStreamPeeker(ss, callback)
		return handler(srv, peeker)
	}
}

// UnaryInvalidator will invalidate any mutating RPC that targets a
// repository in a gRPC unary RPC
func UnaryInvalidator(ci diskcache.Invalidator, reg *protoregistry.Registry, logger log.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		if shouldIgnore(reg, info.FullMethod) {
			return handler(ctx, req)
		}

		errLogger := methodErrLogger(logger, info.FullMethod)

		mInfo, err := reg.LookupMethod(info.FullMethod)
		countRPCType(mInfo)
		if err != nil {
			errLogger(err)
			return handler(ctx, req)
		}

		if !shouldInvalidate(mInfo) {
			return handler(ctx, req)
		}

		pbReq, ok := req.(proto.Message)
		if !ok {
			errLogger(fmt.Errorf("expected protobuf message but got %T", req))
			return handler(ctx, req)
		}

		target, err := mInfo.TargetRepo(pbReq)
		if err != nil {
			errLogger(err)
			return handler(ctx, req)
		}

		le, err := ci.StartLease(ctx, target)
		if err != nil {
			errLogger(err)
			return handler(ctx, req)
		}

		// wrap the handler to ensure the lease is always ended
		return func() (resp interface{}, err error) {
			defer func() {
				if err := le.EndLease(ctx); err != nil {
					errLogger(err)
				}
			}()
			return handler(ctx, req)
		}()
	}
}

type recvMsgCallback func(context.Context, interface{}, error)

func invalidateCache(ci diskcache.Invalidator, mInfo protoregistry.MethodInfo, handler grpc.StreamHandler, errLogger func(error)) (grpc.StreamHandler, recvMsgCallback) {
	var le struct {
		sync.RWMutex
		diskcache.LeaseEnder
	}

	// ensures that the lease ender is invoked after the original handler
	wrappedHandler := func(srv interface{}, stream grpc.ServerStream) error {
		defer func() {
			le.RLock()
			defer le.RUnlock()

			if le.LeaseEnder == nil {
				return
			}
			if err := le.EndLease(stream.Context()); err != nil {
				errLogger(err)
			}
		}()
		return handler(srv, stream)
	}

	// starts the cache lease and sets the lease ender iff the request's target
	// repository can be determined from the first request message
	peekerCallback := func(ctx context.Context, firstReq interface{}, err error) {
		if err != nil {
			errLogger(err)
			return
		}

		pbFirstReq, ok := firstReq.(proto.Message)
		if !ok {
			errLogger(fmt.Errorf("cache invalidation expected protobuf request, but got %T", firstReq))
			return
		}

		target, err := mInfo.TargetRepo(pbFirstReq)
		if err != nil {
			errLogger(err)
			return
		}

		le.Lock()
		defer le.Unlock()

		l, err := ci.StartLease(ctx, target)
		if err != nil {
			errLogger(err)
			return
		}
		le.LeaseEnder = l
	}

	return wrappedHandler, peekerCallback
}

// streamPeeker allows a stream interceptor to insert peeking logic to perform
// an action when the first RecvMsg
type streamPeeker struct {
	grpc.ServerStream

	// onFirstRecvCallback is called the first time the server stream's RecvMsg
	// is invoked. It passes the results of the stream's RecvMsg as the
	// callback's parameters.
	onFirstRecvOnce     sync.Once
	onFirstRecvCallback recvMsgCallback
}

// newStreamPeeker returns a wrapped stream that allows a callback to be called
// on the first invocation of RecvMsg.
func newStreamPeeker(stream grpc.ServerStream, callback recvMsgCallback) grpc.ServerStream {
	return &streamPeeker{
		ServerStream:        stream,
		onFirstRecvCallback: callback,
	}
}

// RecvMsg overrides the embedded grpc.ServerStream's method of the same name so
// that the callback is called on the first call.
func (sp *streamPeeker) RecvMsg(m interface{}) error {
	err := sp.ServerStream.RecvMsg(m)
	sp.onFirstRecvOnce.Do(func() { sp.onFirstRecvCallback(sp.ServerStream.Context(), m, err) })
	return err
}
