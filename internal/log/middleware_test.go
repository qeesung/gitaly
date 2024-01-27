package log

import (
	"context"
	"testing"

	grpcmw "github.com/grpc-ecosystem/go-grpc-middleware"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	grpcmwloggingv2 "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/stats"
)

func TestPropagationMessageProducer(t *testing.T) {
	t.Run("empty context", func(t *testing.T) {
		ctx := createContext()
		mp := PropagationMessageProducer(func(context.Context, grpcmwloggingv2.Level, string, ...any) {})
		mp(ctx, grpcmwloggingv2.LevelDebug, "", nil, nil)
	})

	t.Run("context with holder", func(t *testing.T) {
		holder := new(messageProducerHolder)
		ctx := context.WithValue(createContext(), messageProducerHolderKey{}, holder)
		triggered := false
		mp := PropagationMessageProducer(func(context.Context, grpcmwloggingv2.Level, string, ...any) {
			triggered = true
		})
		mp(ctx, grpcmwloggingv2.LevelDebug, "format-stub", grpcmwloggingv2.Fields{"a", 1}...)
		require.Equal(t, "format-stub", holder.msg)
		require.Equal(t, grpcmwloggingv2.LevelDebug, holder.level)
		require.Equal(t, grpcmwloggingv2.Fields{"a", 1}, holder.fields)
		holder.actual(ctx, grpcmwloggingv2.LevelDebug, "", nil, nil)
		require.True(t, triggered)
	})
}

func TestPerRPCLogHandler(t *testing.T) {
	msh := &mockStatHandler{Calls: map[string][]interface{}{}}

	lh := PerRPCLogHandler{
		Underlying: msh,
		FieldProducers: []FieldsProducer{
			func(ctx context.Context, err error) Fields { return Fields{"a": 1} },
			func(ctx context.Context, err error) Fields { return Fields{"b": "2"} },
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
		mpp.msg = "message"
		mpp.level = grpcmwloggingv2.LevelInfo
		mpp.actual = func(ctx context.Context, level grpcmwloggingv2.Level, msg string, fields ...any) {
			assert.Equal(t, "message", msg)
			assert.Equal(t, grpcmwloggingv2.LevelInfo, level)
			assert.Equal(t, grpcmwloggingv2.Fields{"a", 1, "b", "2"}, mpp.fields)
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
		assert.Equal(t, grpcmwloggingv2.Fields{"a", 1}, mpp.fields)
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
		assert.Equal(t, grpcmwloggingv2.Fields{"a", 1}, mpp.fields)
	})
}

// createContext creates a new context for testing purposes. We cannot use `testhelper.Context()` because of a cyclic dependency between
// this package and the `testhelper` package.
func createContext() context.Context {
	return context.Background()
}

func TestLogDeciderOption_logByRegexpMatch(t *testing.T) {
	methodNames := []string{
		"/grpc.health.v1.Health/Check",
		"/gitaly.SmartHTTPService/InfoRefsUploadPack",
		"/gitaly.SmartHTTPService/PostUploadPackWithSidechannel",
	}
	for _, tc := range []struct {
		desc             string
		skip             string
		only             string
		shouldLogMethods []string
	}{
		{
			desc:             "default setting",
			skip:             "",
			only:             "",
			shouldLogMethods: []string{"InfoRefsUploadPack", "PostUploadPackWithSidechannel"},
		},
		{
			desc:             "allow all",
			skip:             "",
			only:             ".",
			shouldLogMethods: []string{"Check", "InfoRefsUploadPack", "PostUploadPackWithSidechannel"},
		},
		{
			desc:             "only log Check",
			skip:             "",
			only:             "^/grpc.health.v1.Health/Check$",
			shouldLogMethods: []string{"Check"},
		},
		{
			desc:             "skip log Check",
			skip:             "^/grpc.health.v1.Health/Check$",
			only:             "",
			shouldLogMethods: []string{"InfoRefsUploadPack", "PostUploadPackWithSidechannel"},
		},
		{
			// If condition 'only' exists, ignore condition 'skip'
			desc:             "only log Check and ignore skip setting",
			skip:             "^/grpc.health.v1.Health/Check$",
			only:             "^/grpc.health.v1.Health/Check$",
			shouldLogMethods: []string{"Check"},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			t.Setenv("GITALY_LOG_REQUEST_METHOD_DENY_PATTERN", tc.skip)
			t.Setenv("GITALY_LOG_REQUEST_METHOD_ALLOW_PATTERN", tc.only)

			logger, hook := test.NewNullLogger()
			gitalyLogger := FromLogrusEntry(logrus.NewEntry(logger))

			interceptor := grpcmwloggingv2.UnaryServerInterceptor(DefaultInterceptorLogger(gitalyLogger))
			interceptor = selector.UnaryServerInterceptor(interceptor, *DeciderMatcher())

			ctx := createContext()
			for _, methodName := range methodNames {
				_, err := interceptor(
					ctx,
					nil,
					&grpc.UnaryServerInfo{FullMethod: methodName},
					func(ctx context.Context, req interface{}) (interface{}, error) {
						return nil, nil
					},
				)
				require.NoError(t, err)
			}

			entries := hook.AllEntries()
			finishingCallEntries := make([]*logrus.Entry, 0)
			for _, entry := range entries {
				if entry.Message == "finished call" {
					finishingCallEntries = append(finishingCallEntries, entry)
				}
			}

			require.Len(t, finishingCallEntries, len(tc.shouldLogMethods))
			for idx, entry := range finishingCallEntries {
				require.Equal(t, entry.Data["grpc.method"], tc.shouldLogMethods[idx])
			}
		})
	}
}

func TestConvertLoggingFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		desc     string
		input    grpcmwloggingv2.Fields
		expected map[string]any
	}{
		{
			desc:     "Converting v2 logging fields to map[string]any, even number of fields",
			input:    grpcmwloggingv2.Fields{"k1", "v1", "k2", "v2"},
			expected: map[string]any{"k1": "v1", "k2": "v2"},
		},
		{
			desc:     "Converting v2 logging fields to map[string]any, odd number of fields",
			input:    grpcmwloggingv2.Fields{"k1", "v1", "k2"},
			expected: map[string]any{"k1": "v1", "k2": ""},
		},
		{
			desc:     "Converting v2 logging fields to map[string]any, duplicate keys",
			input:    grpcmwloggingv2.Fields{"k1", "v1", "k1", "v2"},
			expected: map[string]any{"k1": "v2"},
		},
		{
			desc:     "Converting v2 logging fields to map[string]any, empty input",
			input:    grpcmwloggingv2.Fields{},
			expected: map[string]any{},
		},
		{
			desc:     "Converting v2 logging fields to map[string]any, nil input",
			input:    nil,
			expected: map[string]any{},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			actual := ConvertLoggingFields(tc.input)
			require.Equal(t, tc.expected, actual)
		})
	}
}
