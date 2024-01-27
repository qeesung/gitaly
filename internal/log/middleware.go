package log

import (
	"context"
	"regexp"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors"
	grpcmwloggingv2 "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/env"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

const (
	defaultLogRequestMethodAllowPattern = ""
	defaultLogRequestMethodDenyPattern  = "^/grpc.health.v1.Health/Check$"
)

// DeciderMatcher is used as a selector to support log filtering.
// If "GITALY_LOG_REQUEST_METHOD_DENY_PATTERN" ENV variable is set, logger will filter out the log whose "fullMethodName" matches it;
// If "GITALY_LOG_REQUEST_METHOD_ALLOW_PATTERN" ENV variable is set, logger will only keep the log whose "fullMethodName" matches it;
// Under any conditions, the error log will not be filtered out;
// If the ENV variables are not set, there will be no additional effects.
// Replacing old DeciderOption
func DeciderMatcher() *selector.Matcher {
	matcher := methodNameMatcherFromEnv()
	matcherFunc := selector.MatchFunc(func(_ context.Context, callMeta interceptors.CallMeta) bool {
		if matcher == nil {
			return true
		}
		return matcher(callMeta.FullMethod())
	})
	return &matcherFunc
}

func methodNameMatcherFromEnv() func(string) bool {
	if pattern := env.GetString("GITALY_LOG_REQUEST_METHOD_ALLOW_PATTERN",
		defaultLogRequestMethodAllowPattern); pattern != "" {
		methodRegex := regexp.MustCompile(pattern)

		return func(fullMethodName string) bool {
			return methodRegex.MatchString(fullMethodName)
		}
	}

	if pattern := env.GetString("GITALY_LOG_REQUEST_METHOD_DENY_PATTERN",
		defaultLogRequestMethodDenyPattern); pattern != "" {
		methodRegex := regexp.MustCompile(pattern)

		return func(fullMethodName string) bool {
			return !methodRegex.MatchString(fullMethodName)
		}
	}

	return nil
}

// FieldsProducer returns fields that need to be added into the logging context. error argument is
// the result of RPC handling.
type FieldsProducer func(context.Context, error) Fields

type messageProducerHolder struct {
	logger LogrusLogger
	actual grpcmwloggingv2.LoggerFunc
	msg    string
	level  grpcmwloggingv2.Level
	fields grpcmwloggingv2.Fields
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
func PropagationMessageProducer(actual grpcmwloggingv2.LoggerFunc) grpcmwloggingv2.LoggerFunc {
	return func(ctx context.Context, level grpcmwloggingv2.Level, msg string, fields ...any) {
		mpp := messageProducerPropagationFrom(ctx)
		if mpp == nil {
			return
		}
		*mpp = messageProducerHolder{
			logger: fromContext(ctx),
			actual: actual,
			msg:    msg,
			level:  level,
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
			mpp.fields = grpcmwloggingv2.Fields{}
		}
		for _, fp := range lh.FieldProducers {
			for k, v := range fp(ctx, nil) {
				// The message producers can have fields with updated values, for example
				// grpc.response.payload_bytes increased from 0 to 100. In this case we need
				// update the value of the field instead of appending it. The grpc middleware v2 logging
				// fields don't support update, so we need to delete the field and append it again.
				mpp.fields.Delete(k)
				mpp.fields = mpp.fields.AppendUnique(grpcmwloggingv2.Fields{k, v})
			}
		}
		// Once again because all interceptors are finished and context doesn't contain
		// a logger we need to set logger manually into the context.
		// It's needed because github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus.DefaultMessageProducer
		// extracts logger from the context and use it to write the logs.
		ctx = mpp.logger.toContext(ctx)
		mpp.actual(ctx, mpp.level, mpp.msg, mpp.fields...)

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
			for k, v := range fromContext(ctx).entryWrapper.entry.Data {
				mpp.fields = mpp.fields.AppendUnique(grpcmwloggingv2.Fields{k, v})
			}
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
			for k, v := range fromContext(ctx).entryWrapper.entry.Data {
				mpp.fields = mpp.fields.AppendUnique(grpcmwloggingv2.Fields{k, v})
			}
		}
		return handler(srv, ss)
	}
}

// ConvertLoggingFields converts Fields in go-grpc-middleware/v2/interceptors/logging package
// into a general map[string]interface{}. So that other logging packages (such as Logrus) can use them.
func ConvertLoggingFields(fields grpcmwloggingv2.Fields) map[string]any {
	// We need len(fields)/2 because fields's type is a slice in the form of [key1, value1, key2, value2, ...]
	// so in order to parse it into a map, we need a map with len(fields)/2 size.
	fieldsMap := make(map[string]any, len(fields)/2)
	i := fields.Iterator()
	for i.Next() {
		k, v := i.At()
		fieldsMap[k] = v
	}
	return fieldsMap
}

// DefaultInterceptorLogger adapts gitaly's logger interface to grpc middleware logger function.
func DefaultInterceptorLogger(l Logger) grpcmwloggingv2.LoggerFunc {
	return func(c context.Context, level grpcmwloggingv2.Level, msg string, fields ...any) {
		f := ConvertLoggingFields(fields)
		switch level {
		case grpcmwloggingv2.LevelDebug:
			l.ReplaceFields(f).Debug(msg)
		case grpcmwloggingv2.LevelInfo:
			l.ReplaceFields(f).Info(msg)
		case grpcmwloggingv2.LevelWarn:
			l.ReplaceFields(f).Warn(msg)
		case grpcmwloggingv2.LevelError:
			l.ReplaceFields(f).Error(msg)
		}
	}
}
