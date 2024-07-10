package loghandler

import (
	"context"

	grpcmwlogrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/sirupsen/logrus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
)

// FieldsProducer returns fields that need to be added into the logging context. error argument is
// the result of RPC handling.
type FieldsProducer func(context.Context, error) log.Fields

// MessageProducer returns a wrapper that extends passed mp to accept additional fields generated
// by each of the fieldsProducers.
func MessageProducer(mp grpcmwlogrus.MessageProducer, fieldsProducers ...FieldsProducer) grpcmwlogrus.MessageProducer {
	return func(ctx context.Context, format string, level logrus.Level, code codes.Code, err error, fields log.Fields) {
		for _, fieldsProducer := range fieldsProducers {
			for key, val := range fieldsProducer(ctx, err) {
				fields[key] = val
			}
		}
		mp(ctx, format, level, code, err, fields)
	}
}

type messageProducerHolder struct {
	logger log.LogrusLogger
	actual grpcmwlogrus.MessageProducer
	format string
	level  logrus.Level
	code   codes.Code
	err    error
	fields log.Fields
}

type messageProducerHolderKey struct{}

// messageProducerPropagationFrom extracts *messageProducerHolder from context
// and returns to the caller.
// It returns nil in case it is not found.
func messageProducerPropagationFrom(ctx context.Context) *messageProducerHolder {
	mpp, ok := ctx.Value(messageProducerHolderKey{}).(*messageProducerHolder)
	if !ok {
		return nil
	}
	return mpp
}

// PropagationMessageProducer catches logging information from the context and populates it
// to the special holder that should be present in the context.
// Should be used only in combination with PerRPCLogHandler.
func PropagationMessageProducer(actual grpcmwlogrus.MessageProducer) grpcmwlogrus.MessageProducer {
	return func(ctx context.Context, format string, level logrus.Level, code codes.Code, err error, fields log.Fields) {
		mpp := messageProducerPropagationFrom(ctx)
		if mpp == nil {
			return
		}
		*mpp = messageProducerHolder{
			logger: log.FromContext(ctx),
			actual: actual,
			format: format,
			level:  level,
			code:   code,
			err:    err,
			fields: fields,
		}
	}
}

// PerRPCLogHandler is designed to collect stats that are accessible
// from the google.golang.org/grpc/stats.Handler, because some information
// can't be extracted on the interceptors level.
type PerRPCLogHandler struct {
	Underlying     stats.Handler
	FieldProducers []FieldsProducer
}

// HandleConn only calls Underlying and exists to satisfy gRPC stats.Handler.
func (lh PerRPCLogHandler) HandleConn(ctx context.Context, cs stats.ConnStats) {
	lh.Underlying.HandleConn(ctx, cs)
}

// TagConn only calls Underlying and exists to satisfy gRPC stats.Handler.
func (lh PerRPCLogHandler) TagConn(ctx context.Context, cti *stats.ConnTagInfo) context.Context {
	return lh.Underlying.TagConn(ctx, cti)
}

// HandleRPC catches each RPC call and for the *stats.End stats invokes
// custom message producers to populate logging data. Once all data is collected
// the actual logging happens by using logger that is caught by PropagationMessageProducer.
func (lh PerRPCLogHandler) HandleRPC(ctx context.Context, rs stats.RPCStats) {
	lh.Underlying.HandleRPC(ctx, rs)
	switch rs.(type) {
	case *stats.End:
		// This code runs once all interceptors are finished their execution.
		// That is why any logging info collected after interceptors completion
		// is not at the logger's context. That is why we need to manually propagate
		// it to the logger.
		mpp := messageProducerPropagationFrom(ctx)
		if mpp == nil || (mpp != nil && mpp.actual == nil) {
			return
		}

		if mpp.fields == nil {
			mpp.fields = log.Fields{}
		}
		for _, fp := range lh.FieldProducers {
			for k, v := range fp(ctx, mpp.err) {
				mpp.fields[k] = v
			}
		}
		// Once again because all interceptors are finished and context doesn't contain
		// a logger we need to set logger manually into the context.
		// It's needed because github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus.DefaultMessageProducer
		// extracts logger from the context and use it to write the logs.
		ctx = mpp.logger.ToContext(ctx)
		mpp.actual(ctx, mpp.format, mpp.level, mpp.code, mpp.err, mpp.fields)
		return
	}
}

// TagRPC propagates a special data holder into the context that is responsible to
// hold logging information produced by the logging interceptor.
// The logging data should be caught by the UnaryLogDataCatcherServerInterceptor. It needs to
// be included into the interceptor chain below logging interceptor.
func (lh PerRPCLogHandler) TagRPC(ctx context.Context, rti *stats.RPCTagInfo) context.Context {
	ctx = context.WithValue(ctx, messageProducerHolderKey{}, new(messageProducerHolder))
	return lh.Underlying.TagRPC(ctx, rti)
}

// UnaryLogDataCatcherServerInterceptor catches logging data produced by the upper interceptors and
// propagates it into the holder to pop up it to the HandleRPC method of the PerRPCLogHandler.
func UnaryLogDataCatcherServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		mpp := messageProducerPropagationFrom(ctx)
		if mpp != nil {
			mpp.fields = log.FromContext(ctx).Data()
		}
		return handler(ctx, req)
	}
}

// StreamLogDataCatcherServerInterceptor catches logging data produced by the upper interceptors and
// propagates it into the holder to pop up it to the HandleRPC method of the PerRPCLogHandler.
func StreamLogDataCatcherServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		mpp := messageProducerPropagationFrom(ctx)
		if mpp != nil {
			mpp.fields = log.FromContext(ctx).Data()
		}
		return handler(srv, ss)
	}
}
