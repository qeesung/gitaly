package loghandler

import (
	"context"
	"io"
	"testing"

	grpcmw "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/stats"
)

func TestMessageProducer(t *testing.T) {
	triggered := false

	attachedFields := log.Fields{"e": "stub"}
	msgProducer := MessageProducer(func(c context.Context, format string, level logrus.Level, code codes.Code, err error, fields log.Fields) {
		require.Equal(t, createContext(), c)
		require.Equal(t, "format-stub", format)
		require.Equal(t, logrus.DebugLevel, level)
		require.Equal(t, codes.OutOfRange, code)
		require.Equal(t, assert.AnError, err)
		require.Equal(t, attachedFields, fields)
		triggered = true
	})
	msgProducer(createContext(), "format-stub", logrus.DebugLevel, codes.OutOfRange, assert.AnError, attachedFields)

	require.True(t, triggered)
}

func TestMessageProducerWithFieldsProducers(t *testing.T) {
	triggered := false

	var infoFromCtx struct{}
	ctx := createContext()
	ctx = context.WithValue(ctx, infoFromCtx, "world")

	fieldsProducer1 := func(context.Context, error) log.Fields {
		return log.Fields{"a": 1}
	}
	fieldsProducer2 := func(context.Context, error) log.Fields {
		return log.Fields{"b": "test"}
	}
	fieldsProducer3 := func(ctx context.Context, err error) log.Fields {
		return log.Fields{"c": err.Error()}
	}
	fieldsProducer4 := func(ctx context.Context, err error) log.Fields {
		return log.Fields{"d": ctx.Value(infoFromCtx)}
	}
	attachedFields := log.Fields{"e": "stub"}

	msgProducer := MessageProducer(func(c context.Context, format string, level logrus.Level, code codes.Code, err error, fields log.Fields) {
		require.Equal(t, log.Fields{"a": 1, "b": "test", "c": err.Error(), "d": "world", "e": "stub"}, fields)
		triggered = true
	}, fieldsProducer1, fieldsProducer2, fieldsProducer3, fieldsProducer4)
	msgProducer(ctx, "format-stub", logrus.InfoLevel, codes.OK, assert.AnError, attachedFields)

	require.True(t, triggered)
}

func TestPropagationMessageProducer(t *testing.T) {
	t.Run("empty context", func(t *testing.T) {
		ctx := createContext()
		mp := PropagationMessageProducer(func(context.Context, string, logrus.Level, codes.Code, error, log.Fields) {})
		mp(ctx, "", logrus.DebugLevel, codes.OK, nil, nil)
	})

	t.Run("context with holder", func(t *testing.T) {
		holder := new(messageProducerHolder)
		ctx := context.WithValue(createContext(), messageProducerHolderKey{}, holder)
		triggered := false
		mp := PropagationMessageProducer(func(ctx context.Context, format string, level logrus.Level, code codes.Code, err error, fields log.Fields) {
			triggered = true
		})
		mp(ctx, "format-stub", logrus.DebugLevel, codes.OutOfRange, assert.AnError, log.Fields{"a": 1})
		require.Equal(t, "format-stub", holder.format)
		require.Equal(t, logrus.DebugLevel, holder.level)
		require.Equal(t, codes.OutOfRange, holder.code)
		require.Equal(t, assert.AnError, holder.err)
		require.Equal(t, log.Fields{"a": 1}, holder.fields)
		holder.actual(ctx, "", logrus.DebugLevel, codes.OK, nil, nil)
		require.True(t, triggered)
	})
}

func TestPerRPCLogHandler(t *testing.T) {
	msh := &mockStatHandler{Calls: map[string][]interface{}{}}

	lh := PerRPCLogHandler{
		Underlying: msh,
		FieldProducers: []FieldsProducer{
			func(ctx context.Context, err error) log.Fields { return log.Fields{"a": 1} },
			func(ctx context.Context, err error) log.Fields { return log.Fields{"b": "2"} },
			func(ctx context.Context, err error) log.Fields { return log.Fields{"c": err.Error()} },
		},
	}

	t.Run("check propagation", func(t *testing.T) {
		ctx := createContext()
		ctx = lh.TagConn(ctx, &stats.ConnTagInfo{})
		lh.HandleConn(ctx, &stats.ConnBegin{})
		ctx = lh.TagRPC(ctx, &stats.RPCTagInfo{})
		lh.HandleRPC(ctx, &stats.Begin{})
		lh.HandleRPC(ctx, &stats.InHeader{})
		lh.HandleRPC(ctx, &stats.InPayload{})
		lh.HandleRPC(ctx, &stats.OutHeader{})
		lh.HandleRPC(ctx, &stats.OutPayload{})
		lh.HandleRPC(ctx, &stats.End{})
		lh.HandleConn(ctx, &stats.ConnEnd{})

		assert.Equal(t, map[string][]interface{}{
			"TagConn":    {&stats.ConnTagInfo{}},
			"HandleConn": {&stats.ConnBegin{}, &stats.ConnEnd{}},
			"TagRPC":     {&stats.RPCTagInfo{}},
			"HandleRPC":  {&stats.Begin{}, &stats.InHeader{}, &stats.InPayload{}, &stats.OutHeader{}, &stats.OutPayload{}, &stats.End{}},
		}, msh.Calls)
	})

	t.Run("log handling", func(t *testing.T) {
		ctx := ctxlogrus.ToContext(createContext(), logrus.NewEntry(newLogger()))
		ctx = lh.TagRPC(ctx, &stats.RPCTagInfo{})
		mpp := ctx.Value(messageProducerHolderKey{}).(*messageProducerHolder)
		mpp.format = "message"
		mpp.level = logrus.InfoLevel
		mpp.code = codes.InvalidArgument
		mpp.err = assert.AnError
		mpp.actual = func(ctx context.Context, format string, level logrus.Level, code codes.Code, err error, fields log.Fields) {
			assert.Equal(t, "message", format)
			assert.Equal(t, logrus.InfoLevel, level)
			assert.Equal(t, codes.InvalidArgument, code)
			assert.Equal(t, assert.AnError, err)
			assert.Equal(t, log.Fields{"a": 1, "b": "2", "c": mpp.err.Error()}, mpp.fields)
		}
		lh.HandleRPC(ctx, &stats.End{})
	})
}

type mockStatHandler struct {
	Calls map[string][]interface{}
}

func (m *mockStatHandler) TagRPC(ctx context.Context, s *stats.RPCTagInfo) context.Context {
	m.Calls["TagRPC"] = append(m.Calls["TagRPC"], s)
	return ctx
}

func (m *mockStatHandler) HandleRPC(ctx context.Context, s stats.RPCStats) {
	m.Calls["HandleRPC"] = append(m.Calls["HandleRPC"], s)
}

func (m *mockStatHandler) TagConn(ctx context.Context, s *stats.ConnTagInfo) context.Context {
	m.Calls["TagConn"] = append(m.Calls["TagConn"], s)
	return ctx
}

func (m *mockStatHandler) HandleConn(ctx context.Context, s stats.ConnStats) {
	m.Calls["HandleConn"] = append(m.Calls["HandleConn"], s)
}

func TestUnaryLogDataCatcherServerInterceptor(t *testing.T) {
	handlerStub := func(context.Context, interface{}) (interface{}, error) {
		return nil, nil
	}

	t.Run("propagates call", func(t *testing.T) {
		interceptor := UnaryLogDataCatcherServerInterceptor()
		resp, err := interceptor(createContext(), nil, nil, func(ctx context.Context, req interface{}) (interface{}, error) {
			return 42, assert.AnError
		})

		assert.Equal(t, 42, resp)
		assert.Equal(t, assert.AnError, err)
	})

	t.Run("no logger", func(t *testing.T) {
		mpp := &messageProducerHolder{}
		ctx := context.WithValue(createContext(), messageProducerHolderKey{}, mpp)

		interceptor := UnaryLogDataCatcherServerInterceptor()
		_, _ = interceptor(ctx, nil, nil, handlerStub)
		assert.Empty(t, mpp.fields)
	})

	t.Run("caught", func(t *testing.T) {
		mpp := &messageProducerHolder{}
		ctx := context.WithValue(createContext(), messageProducerHolderKey{}, mpp)
		ctx = ctxlogrus.ToContext(ctx, newLogger().WithField("a", 1))
		interceptor := UnaryLogDataCatcherServerInterceptor()
		_, _ = interceptor(ctx, nil, nil, handlerStub)
		assert.Equal(t, log.Fields{"a": 1}, mpp.fields)
	})
}

func TestStreamLogDataCatcherServerInterceptor(t *testing.T) {
	t.Run("propagates call", func(t *testing.T) {
		interceptor := StreamLogDataCatcherServerInterceptor()
		ss := &grpcmw.WrappedServerStream{WrappedContext: createContext()}
		err := interceptor(nil, ss, nil, func(interface{}, grpc.ServerStream) error {
			return assert.AnError
		})

		assert.Equal(t, assert.AnError, err)
	})

	t.Run("no logger", func(t *testing.T) {
		mpp := &messageProducerHolder{}
		ctx := context.WithValue(createContext(), messageProducerHolderKey{}, mpp)

		interceptor := StreamLogDataCatcherServerInterceptor()
		ss := &grpcmw.WrappedServerStream{WrappedContext: ctx}
		_ = interceptor(nil, ss, nil, func(interface{}, grpc.ServerStream) error { return nil })
	})

	t.Run("caught", func(t *testing.T) {
		mpp := &messageProducerHolder{}
		ctx := context.WithValue(createContext(), messageProducerHolderKey{}, mpp)
		ctx = ctxlogrus.ToContext(ctx, newLogger().WithField("a", 1))

		interceptor := StreamLogDataCatcherServerInterceptor()
		ss := &grpcmw.WrappedServerStream{WrappedContext: ctx}
		_ = interceptor(nil, ss, nil, func(interface{}, grpc.ServerStream) error { return nil })
		assert.Equal(t, log.Fields{"a": 1}, mpp.fields)
	})
}

// createContext creates a new context for testing purposes. We cannot use `testhelper.Context()` because of a cyclic dependency between
// this package and the `testhelper` package.
func createContext() context.Context {
	return context.Background()
}

//nolint:forbidigo
func newLogger() *logrus.Logger {
	logger := logrus.New()
	logger.Out = io.Discard
	return logger
}
