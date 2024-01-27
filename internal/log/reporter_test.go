package log

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors"
	grpcmwloggingv2 "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testprotomessage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestLoggerFunc(t *testing.T) {
	triggered := false

	attachedFields := grpcmwloggingv2.Fields{"field_key1", "v1", "field_key2", "v2"}
	expectedFieldMap := ConvertLoggingFields(attachedFields)

	loggerFunc := grpcmwloggingv2.LoggerFunc(
		func(c context.Context, level grpcmwloggingv2.Level, msg string, fields ...any) {
			actual := ConvertLoggingFields(fields)

			require.Equal(t, createContext(), c)
			require.Equal(t, "msg-stub", msg)
			require.Equal(t, grpcmwloggingv2.LevelDebug, level)

			require.Equal(t, expectedFieldMap, actual)
			triggered = true
		})
	loggerFunc(createContext(), grpcmwloggingv2.LevelDebug, "msg-stub", attachedFields...)

	require.True(t, triggered)
}

func TestReporter_PostCall(t *testing.T) {
	abortedError := status.Error(codes.Aborted, "testing call aborted")
	fieldProducers := []FieldsProducer{
		func(ctx context.Context, err error) Fields { return Fields{"a": 1} },
		func(ctx context.Context, err error) Fields { return Fields{"b": "2"} },
		func(ctx context.Context, err error) Fields {
			if err == nil {
				return Fields{"c": nil}
			}
			return Fields{"c": err.Error()}
		},
	}

	for _, tc := range []struct {
		desc               string
		err                error
		loggableEvents     []grpcmwloggingv2.LoggableEvent
		loggerFuncCalled   bool // true if the logger function should be called
		fieldsInCtx        grpcmwloggingv2.Fields
		expectedLevel      grpcmwloggingv2.Level
		expectedStatusCode codes.Code
		expectedFields     map[string]any
	}{
		{
			desc:               "no error",
			err:                nil,
			loggableEvents:     []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.FinishCall},
			loggerFuncCalled:   true,
			fieldsInCtx:        grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel:      grpcmwloggingv2.LevelInfo,
			expectedStatusCode: codes.OK,
			expectedFields: map[string]any{
				"a":            1,                 // from the field producers
				"b":            "2",               // from the field producers
				"c":            nil,               // from the field producers
				"d":            3,                 // reporter pre-existing fields
				"e":            "4",               // reporter pre-existing fields
				"grpc.code":    codes.OK.String(), // added by PostCall
				"grpc.time_ms": "31.425",          // added by PostCall
				"ctx.key1":     "v1",              // from the context
				"ctx.key2":     "v2",              // from the context
			},
		},
		{
			desc:               "EOF error",
			err:                io.EOF,
			loggableEvents:     []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.FinishCall},
			loggerFuncCalled:   true,
			expectedLevel:      grpcmwloggingv2.LevelInfo,
			expectedStatusCode: codes.OK,
			expectedFields: map[string]any{
				"a":            1,                 // from the field producers
				"b":            "2",               // from the field producers
				"c":            nil,               // from the field producers
				"d":            3,                 // reporter pre-existing fields
				"e":            "4",               // reporter pre-existing fields
				"grpc.code":    codes.OK.String(), // added by PostCall
				"grpc.time_ms": "31.425",          // added by PostCall
			},
		},
		{
			desc:               "aborted error",
			err:                abortedError,
			loggableEvents:     []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.FinishCall},
			loggerFuncCalled:   true,
			fieldsInCtx:        grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel:      grpcmwloggingv2.LevelWarn,
			expectedStatusCode: codes.Aborted,
			expectedFields: map[string]any{
				"a":            1,                      // from the field producers
				"b":            "2",                    // from the field producers
				"c":            abortedError.Error(),   // from the field producers
				"d":            3,                      // reporter pre-existing fields
				"e":            "4",                    // reporter pre-existing fields
				"grpc.code":    codes.Aborted.String(), // added by PostCall
				"grpc.error":   abortedError.Error(),   // added by PostCall
				"grpc.time_ms": "31.425",               // added by PostCall
				"ctx.key1":     "v1",                   // from the context
				"ctx.key2":     "v2",                   // from the context
			},
		},
		{
			desc:             "empty loggable events",
			err:              nil,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{},
			loggerFuncCalled: false, // loggable events are empty, so the logger function should not be called
			// the rest of the fields are not important here, because the logger function is not called
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			ctx := createContext()
			if len(tc.fieldsInCtx) != 0 {
				ctx = grpcmwloggingv2.InjectFields(ctx, tc.fieldsInCtx)
			}
			opts := evaluateServerOpt([]Option{WithFiledProducers(fieldProducers...), WithLogOnEvents(tc.loggableEvents...)})
			// opts.rpcType = rpcTypeUnary
			var actualLoggerFuncCalled bool
			mockReporter := &reporter{
				CallMeta: interceptors.CallMeta{
					Typ: interceptors.Unary,
				},
				ctx:    ctx,
				kind:   "kind-stub",
				opts:   opts,
				fields: grpcmwloggingv2.Fields{"d", 3, "e", "4"},
				logger: grpcmwloggingv2.LoggerFunc(
					// Customized logger function to verify the expected log message and fields
					func(c context.Context, level grpcmwloggingv2.Level, msg string, fields ...any) {
						actualLoggerFuncCalled = true
						require.Equal(t,
							fmt.Sprintf("finished unary call with code %s", tc.expectedStatusCode.String()),
							msg)
						require.Equal(t, tc.expectedLevel, level)

						actualFields := ConvertLoggingFields(fields)
						require.Equal(t, tc.expectedFields, actualFields)
					}),
			}
			mockReporter.PostCall(tc.err, time.Duration(31425926))
			require.Equal(t, tc.loggerFuncCalled, actualLoggerFuncCalled)
		})
	}
}

func TestReporter_PostMsgSend(t *testing.T) {
	abortedError := status.Error(codes.Aborted, "testing call aborted")
	testProtoMsg := &testprotomessage.MockProtoMessage{Key: "test-message-key", Value: "test-message-value"}

	for _, tc := range []struct {
		desc             string
		err              error
		loggableEvents   []grpcmwloggingv2.LoggableEvent
		loggerFuncCalled bool // true if the logger function should be called
		payload          any
		callMeta         interceptors.CallMeta
		fieldsInCtx      grpcmwloggingv2.Fields
		expectedLevel    grpcmwloggingv2.Level
		expectedFields   map[string]any
		expectedMsg      string
	}{
		{
			desc:             "no error with only StartCall event",
			err:              nil,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.StartCall},
			loggerFuncCalled: true,
			fieldsInCtx:      grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel:    grpcmwloggingv2.LevelInfo,
			expectedFields: map[string]any{
				"d":            3,        // reporter pre-existing fields
				"e":            "4",      // reporter pre-existing fields
				"grpc.time_ms": "31.425", // added by PostMsgSend
				"ctx.key1":     "v1",     // from the context
				"ctx.key2":     "v2",     // from the context
			},
			expectedMsg: "started call",
		},
		{
			desc:             "client side, with only PayloadSent event",
			err:              nil,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.PayloadSent},
			loggerFuncCalled: true,
			callMeta: interceptors.CallMeta{
				IsClient: true,
			},
			payload:       proto.Message(testProtoMsg),
			fieldsInCtx:   grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel: grpcmwloggingv2.LevelInfo,
			expectedFields: map[string]any{
				"d":                    3,             // reporter pre-existing fields
				"e":                    "4",           // reporter pre-existing fields
				"grpc.request.content": testProtoMsg,  // added by PostMsgSend
				"grpc.send.duration":   "31.425926ms", // added by PostMsgSend
				"ctx.key1":             "v1",          // from the context
				"ctx.key2":             "v2",          // from the context
			},
			expectedMsg: "request sent",
		},
		{
			desc:             "client side, invalid payload, with only PayloadSent event",
			err:              nil,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.PayloadSent},
			loggerFuncCalled: true,
			callMeta: interceptors.CallMeta{
				IsClient: true,
			},
			payload:       "invalid payload",
			fieldsInCtx:   grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel: grpcmwloggingv2.LevelError,
			expectedFields: map[string]any{
				"d":                 3,        // reporter pre-existing fields
				"e":                 "4",      // reporter pre-existing fields
				"grpc.request.type": "string", // added by PostMsgSend
				"ctx.key1":          "v1",     // from the context
				"ctx.key2":          "v2",     // from the context
			},
			expectedMsg: "payload is not a google.golang.org/protobuf/proto.Message; programmatic error?",
		},
		{
			desc:             "server side, with only PayloadSent event",
			err:              nil,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.PayloadSent},
			loggerFuncCalled: true,
			callMeta: interceptors.CallMeta{
				IsClient: false,
			},
			payload:       proto.Message(testProtoMsg),
			fieldsInCtx:   grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel: grpcmwloggingv2.LevelInfo,
			expectedFields: map[string]any{
				"d":                     3,             // reporter pre-existing fields
				"e":                     "4",           // reporter pre-existing fields
				"grpc.response.content": testProtoMsg,  // added by PostMsgSend
				"grpc.send.duration":    "31.425926ms", // added by PostMsgSend
				"ctx.key1":              "v1",          // from the context
				"ctx.key2":              "v2",          // from the context
			},
			expectedMsg: "response sent",
		},
		{
			desc:             "server side, invalid payload, with only PayloadSent event",
			err:              nil,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.PayloadSent},
			loggerFuncCalled: true,
			callMeta: interceptors.CallMeta{
				IsClient: false,
			},
			payload:       "invalid payload",
			fieldsInCtx:   grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel: grpcmwloggingv2.LevelError,
			expectedFields: map[string]any{
				"d":                  3,        // reporter pre-existing fields
				"e":                  "4",      // reporter pre-existing fields
				"grpc.response.type": "string", // added by PostMsgSend
				"ctx.key1":           "v1",     // from the context
				"ctx.key2":           "v2",     // from the context
			},
			expectedMsg: "payload is not a google.golang.org/protobuf/proto.Message; programmatic error?",
		},
		{
			desc:             "aborted error with only StartCall event",
			err:              abortedError,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.StartCall},
			loggerFuncCalled: true,
			fieldsInCtx:      grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel:    grpcmwloggingv2.LevelWarn,
			expectedFields: map[string]any{
				"d":            3,                    // reporter pre-existing fields
				"e":            "4",                  // reporter pre-existing fields
				"grpc.time_ms": "31.425",             // added by PostMsgSend
				"grpc.error":   abortedError.Error(), // added by PostMsgSend
				"ctx.key1":     "v1",                 // from the context
				"ctx.key2":     "v2",                 // from the context
			},
			expectedMsg: "started call",
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			ctx := createContext()
			if len(tc.fieldsInCtx) != 0 {
				ctx = grpcmwloggingv2.InjectFields(ctx, tc.fieldsInCtx)
			}
			opts := evaluateServerOpt([]Option{WithLogOnEvents(tc.loggableEvents...)})
			// opts.rpcType = rpcTypeUnary
			var actualLoggerFuncCalled bool
			mockReporter := &reporter{
				CallMeta: tc.callMeta,
				ctx:      ctx,
				kind:     "kind-stub",
				opts:     opts,
				fields:   grpcmwloggingv2.Fields{"d", 3, "e", "4"},
				logger: grpcmwloggingv2.LoggerFunc(
					// Customized logger function to verify the expected log message and fields
					func(c context.Context, level grpcmwloggingv2.Level, msg string, fields ...any) {
						actualLoggerFuncCalled = true
						require.Equal(t, tc.expectedMsg, msg)
						require.Equal(t, tc.expectedLevel, level)
						actualFields := ConvertLoggingFields(fields)
						require.Equal(t, tc.expectedFields, actualFields)
					}),
			}
			mockReporter.PostMsgSend(tc.payload, tc.err, time.Duration(31425926))
			require.Equal(t, tc.loggerFuncCalled, actualLoggerFuncCalled)
		})
	}
}

func TestReporter_PostMsgReceive(t *testing.T) {
	abortedError := status.Error(codes.Aborted, "testing call aborted")
	testProtoMsg := &testprotomessage.MockProtoMessage{Key: "test-message-key", Value: "test-message-value"}

	for _, tc := range []struct {
		desc             string
		err              error
		loggableEvents   []grpcmwloggingv2.LoggableEvent
		loggerFuncCalled bool // true if the logger function should be called
		payload          any
		callMeta         interceptors.CallMeta
		fieldsInCtx      grpcmwloggingv2.Fields
		expectedLevel    grpcmwloggingv2.Level
		expectedFields   map[string]any
		expectedMsg      string
	}{
		{
			desc:             "no error with only StartCall event",
			err:              nil,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.StartCall},
			loggerFuncCalled: true,
			fieldsInCtx:      grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel:    grpcmwloggingv2.LevelInfo,
			expectedFields: map[string]any{
				"d":            3,        // reporter pre-existing fields
				"e":            "4",      // reporter pre-existing fields
				"grpc.time_ms": "31.425", // added by PostMsgSend
				"ctx.key1":     "v1",     // from the context
				"ctx.key2":     "v2",     // from the context
			},
			expectedMsg: "started call",
		},
		{
			desc:             "client side, with only PayloadReceived event",
			err:              nil,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.PayloadReceived},
			loggerFuncCalled: true,
			callMeta: interceptors.CallMeta{
				IsClient: true,
			},
			payload:       proto.Message(testProtoMsg),
			fieldsInCtx:   grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel: grpcmwloggingv2.LevelInfo,
			expectedFields: map[string]any{
				"d":                     3,             // reporter pre-existing fields
				"e":                     "4",           // reporter pre-existing fields
				"grpc.response.content": testProtoMsg,  // added by PostMsgSend
				"grpc.recv.duration":    "31.425926ms", // added by PostMsgSend
				"ctx.key1":              "v1",          // from the context
				"ctx.key2":              "v2",          // from the context
			},
			expectedMsg: "response received",
		},
		{
			desc:             "client side, invalid payload, with only PayloadReceived event",
			err:              nil,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.PayloadReceived},
			loggerFuncCalled: true,
			callMeta: interceptors.CallMeta{
				IsClient: true,
			},
			payload:       "invalid payload",
			fieldsInCtx:   grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel: grpcmwloggingv2.LevelError,
			expectedFields: map[string]any{
				"d":                  3,        // reporter pre-existing fields
				"e":                  "4",      // reporter pre-existing fields
				"grpc.response.type": "string", // added by PostMsgSend
				"ctx.key1":           "v1",     // from the context
				"ctx.key2":           "v2",     // from the context
			},
			expectedMsg: "payload is not a google.golang.org/protobuf/proto.Message; programmatic error?",
		},
		{
			desc:             "server side, with only PayloadReceived event",
			err:              nil,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.PayloadReceived},
			loggerFuncCalled: true,
			callMeta: interceptors.CallMeta{
				IsClient: false,
			},
			payload:       proto.Message(testProtoMsg),
			fieldsInCtx:   grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel: grpcmwloggingv2.LevelInfo,
			expectedFields: map[string]any{
				"d":                    3,             // reporter pre-existing fields
				"e":                    "4",           // reporter pre-existing fields
				"grpc.request.content": testProtoMsg,  // added by PostMsgSend
				"grpc.recv.duration":   "31.425926ms", // added by PostMsgSend
				"ctx.key1":             "v1",          // from the context
				"ctx.key2":             "v2",          // from the context
			},
			expectedMsg: "request received",
		},
		{
			desc:             "server side, invalid payload, with only PayloadSent event",
			err:              nil,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.PayloadReceived},
			loggerFuncCalled: true,
			callMeta: interceptors.CallMeta{
				IsClient: false,
			},
			payload:       "invalid payload",
			fieldsInCtx:   grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel: grpcmwloggingv2.LevelError,
			expectedFields: map[string]any{
				"d":                 3,        // reporter pre-existing fields
				"e":                 "4",      // reporter pre-existing fields
				"grpc.request.type": "string", // added by PostMsgSend
				"ctx.key1":          "v1",     // from the context
				"ctx.key2":          "v2",     // from the context
			},
			expectedMsg: "payload is not a google.golang.org/protobuf/proto.Message; programmatic error?",
		},
		{
			desc:             "aborted error with only StartCall event",
			err:              abortedError,
			loggableEvents:   []grpcmwloggingv2.LoggableEvent{grpcmwloggingv2.StartCall},
			loggerFuncCalled: true,
			fieldsInCtx:      grpcmwloggingv2.Fields{"ctx.key1", "v1", "ctx.key2", "v2"},
			expectedLevel:    grpcmwloggingv2.LevelWarn,
			expectedFields: map[string]any{
				"d":            3,                    // reporter pre-existing fields
				"e":            "4",                  // reporter pre-existing fields
				"grpc.time_ms": "31.425",             // added by PostMsgSend
				"grpc.error":   abortedError.Error(), // added by PostMsgSend
				"ctx.key1":     "v1",                 // from the context
				"ctx.key2":     "v2",                 // from the context
			},
			expectedMsg: "started call",
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			ctx := createContext()
			if len(tc.fieldsInCtx) != 0 {
				ctx = grpcmwloggingv2.InjectFields(ctx, tc.fieldsInCtx)
			}
			opts := evaluateServerOpt([]Option{WithLogOnEvents(tc.loggableEvents...)})
			// opts.rpcType = rpcTypeUnary
			var actualLoggerFuncCalled bool
			mockReporter := &reporter{
				CallMeta: tc.callMeta,
				ctx:      ctx,
				kind:     "kind-stub",
				opts:     opts,
				fields:   grpcmwloggingv2.Fields{"d", 3, "e", "4"},
				logger: grpcmwloggingv2.LoggerFunc(
					// Customized logger function to verify the expected log message and fields
					func(c context.Context, level grpcmwloggingv2.Level, msg string, fields ...any) {
						actualLoggerFuncCalled = true
						require.Equal(t, tc.expectedMsg, msg)
						require.Equal(t, tc.expectedLevel, level)
						actualFields := ConvertLoggingFields(fields)
						require.Equal(t, tc.expectedFields, actualFields)
					}),
			}
			mockReporter.PostMsgReceive(tc.payload, tc.err, time.Duration(31425926))
			require.Equal(t, tc.loggerFuncCalled, actualLoggerFuncCalled)
		})
	}
}
