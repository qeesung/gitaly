package log

import (
	"context"
	"log/slog"
	"path"

	grpc_logrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

type Hook interface {
	Entries() []HookEntry
}

type HookEntry struct {
	Msg   string
	Attrs []slog.Attr
}

// testLogger satisifies the logger interface and provides
// access to log entries added, this helps validation in tests.
var _ Logger = &testLogger{}

type testLogger struct {
	entries []HookEntry
}

// Entries implements Hook.
func (s *testLogger) Entries() []HookEntry {
	return s.entries
}

// Debug implements Logger.
func (s *testLogger) Debug(msg string) {
	panic("unimplemented")
}

// DebugContext implements Logger.
func (s *testLogger) DebugContext(ctx context.Context, msg string) {
	panic("unimplemented")
}

// Error implements Logger.
func (s *testLogger) Error(msg string) {
	panic("unimplemented")
}

// ErrorContext implements Logger.
func (s *testLogger) ErrorContext(ctx context.Context, msg string) {
	panic("unimplemented")
}

// Info implements Logger.
func (s *testLogger) Info(msg string) {
	panic("unimplemented")
}

// InfoContext implements Logger.
func (s *testLogger) InfoContext(ctx context.Context, msg string) {
	panic("unimplemented")
}

// StreamServerInterceptor implements Logger.
func (s *testLogger) StreamServerInterceptor(...grpc_logrus.Option) grpc.StreamServerInterceptor {
	panic("unimplemented")
}

// UnaryServerInterceptor implements Logger.
func (s *testLogger) UnaryServerInterceptor(...grpc_logrus.Option) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		resp, err := handler(ctx, req)

		s.entries = append(s.entries, HookEntry{
			Msg: "finished unary call with code OK",
			Attrs: []slog.Attr{
				{
					Key:   "grpc.method",
					Value: slog.StringValue(path.Base(info.FullMethod)),
				},
			},
		})

		return resp, err
	}
}

// Warn implements Logger.
func (s *testLogger) Warn(msg string) {
	panic("unimplemented")
}

// WarnContext implements Logger.
func (s *testLogger) WarnContext(ctx context.Context, msg string) {
	panic("unimplemented")
}

// WithError implements Logger.
func (s *testLogger) WithError(err error) Logger {
	panic("unimplemented")
}

// WithField implements Logger.
func (s *testLogger) WithField(key string, value any) Logger {
	panic("unimplemented")
}

// WithFields implements Logger.
func (s *testLogger) WithFields(fields logrus.Fields) Logger {
	panic("unimplemented")
}

func NewTestLogger() (Logger, Hook) {
	logger := &testLogger{}
	return logger, logger
}
