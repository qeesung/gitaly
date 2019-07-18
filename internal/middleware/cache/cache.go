package cache

import (
	"context"
	"fmt"
	"sync"

	"github.com/golang/protobuf/proto"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	diskcache "gitlab.com/gitlab-org/gitaly/internal/cache"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
	"google.golang.org/grpc"
)

var (
	rpcTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "gitaly_cacheinvalidator_rpc_total",
			Help: "Total number of RPCs encountered by cache invalidator",
		},
	)
	rpcOpTypes = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "gitaly_cacheinvalidator_optype_total",
			Help: "Total number of operation types encountered by cache invalidator",
		},
		[]string{"type"},
	)
)

func init() {
	prometheus.MustRegister(rpcTotal)
	prometheus.MustRegister(rpcOpTypes)
}

func countRPCType(mInfo protoregistry.MethodInfo) {
	rpcTotal.Inc()

	switch mInfo.Operation {
	case protoregistry.OpAccessor:
		rpcOpTypes.WithLabelValues("accessor").Inc()
	case protoregistry.OpMutator:
		rpcOpTypes.WithLabelValues("mutator").Inc()
	default:
		rpcOpTypes.WithLabelValues("unknown").Inc()
	}
}

// Invalidator is able to invalidate parts of the cache pertinent to a
// specific repository. Before a repo mutating operation, StartLease should
// be called. Once the operation is complete, the returned LeaseEnder should
// be invoked to end the lease.
type Invalidator interface {
	StartLease(repo *gitalypb.Repository) (diskcache.LeaseEnder, error)
}

// StreamInvalidator will invalidate any mutating RPC that targets a
// repository in a gRPC stream based RPC
func StreamInvalidator(ci Invalidator, reg *protoregistry.Registry) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		mInfo, err := reg.LookupMethod(info.FullMethod)
		countRPCType(mInfo)
		if err != nil {
			logrus.WithField("FullMethodName", info.FullMethod).Errorf("unable to lookup method information for %+v", info)
		}

		if mInfo.Operation == protoregistry.OpAccessor {
			return handler(srv, ss)
		}

		handler, callback := invalidateCache(ci, mInfo, handler)
		peeker := newStreamPeeker(ss, callback)
		return handler(srv, peeker)
	}
}

// UnaryInvalidator will invalidate any mutating RPC that targets a
// repository in a gRPC unary RPC
func UnaryInvalidator(ci Invalidator, reg *protoregistry.Registry) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		mInfo, err := reg.LookupMethod(info.FullMethod)
		countRPCType(mInfo)
		if err != nil {
			logrus.WithField("full_method_name", info.FullMethod).Errorf("unable to lookup method information for %+v", info)
		}

		if mInfo.Operation == protoregistry.OpAccessor {
			return handler(ctx, req)
		}

		pbReq, ok := req.(proto.Message)
		if !ok {
			return nil, fmt.Errorf("cache invalidation expected protobuf request, but got %T", req)
		}

		target, err := mInfo.TargetRepo(pbReq)
		if err != nil {
			return nil, err
		}

		le, err := ci.StartLease(target)
		if err != nil {
			return nil, err
		}

		// wrap the handler to ensure the lease is always ended
		return func() (resp interface{}, err error) {
			defer func() {
				if err := le.EndLease(ctx); err != nil {
					logrus.Errorf("unable to end lease: %q", err)
				}
			}()
			return handler(ctx, req)
		}()
	}
}

type recvMsgCallback func(interface{}, error) error

func invalidateCache(ci Invalidator, mInfo protoregistry.MethodInfo, handler grpc.StreamHandler) (grpc.StreamHandler, recvMsgCallback) {
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
				logrus.Errorf("unable to end lease: %q", err)
			}
		}()
		return handler(srv, stream)
	}

	// starts the cache lease and sets the lease ender iff the request's target
	// repository can be determined from the first request message
	peekerCallback := func(firstReq interface{}, err error) error {
		if err != nil {
			return err
		}

		pbFirstReq, ok := firstReq.(proto.Message)
		if !ok {
			return fmt.Errorf("cache invalidation expected protobuf request, but got %T", firstReq)
		}

		target, err := mInfo.TargetRepo(pbFirstReq)
		if err != nil {
			return err
		}

		le.Lock()
		defer le.Unlock()

		le.LeaseEnder, err = ci.StartLease(target)
		if err != nil {
			return err
		}

		return nil
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
	sp.onFirstRecvOnce.Do(func() {
		err := sp.onFirstRecvCallback(m, err)
		if err != nil {
			logrus.Errorf("unable to invalidate cache: %q", err)
		}
	})
	return err
}
