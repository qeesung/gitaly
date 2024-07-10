package log

import (
	"context"

	grpcmwlogrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	grpcmwloggingv2 "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// Fields contains key-value pairs of structured logging data.
type Fields = logrus.Fields

// Logger is the logging type used by Gitaly.
type Logger interface {
	WithField(key string, value any) Logger
	WithFields(fields Fields) Logger
	WithError(err error) Logger

	Debug(msg string)
	Info(msg string)
	Warn(msg string)
	Error(msg string)

	DebugContext(ctx context.Context, msg string)
	InfoContext(ctx context.Context, msg string)
	WarnContext(ctx context.Context, msg string)
	ErrorContext(ctx context.Context, msg string)

	StreamServerInterceptor(...grpcmwlogrus.Option) grpc.StreamServerInterceptor
	UnaryServerInterceptor(...grpcmwlogrus.Option) grpc.UnaryServerInterceptor
}

// LogrusLogger is an implementation of the Logger interface that is implemented via a `logrus.FieldLogger`.
type LogrusLogger struct {
	entry *logrus.Entry
}

// FromLogrusEntry constructs a new Gitaly-specific logger from a `logrus.Logger`.
func FromLogrusEntry(entry *logrus.Entry) LogrusLogger {
	return LogrusLogger{entry: entry}
}

// LogrusEntry returns the `logrus.Entry` that backs this logger. Note that this interface only exists during the
// transition period and will be eventually removed. It is thus heavily discouraged to use it.
//
// Deprecated: This will be removed once all callsites have been converted to do something that is independent of the
// logrus logger.
func (l LogrusLogger) LogrusEntry() *logrus.Entry {
	return l.entry
}

// WithField creates a new logger with the given field appended.
func (l LogrusLogger) WithField(key string, value any) Logger {
	return LogrusLogger{entry: l.entry.WithField(key, value)}
}

// WithFields creates a new logger with the given fields appended.
func (l LogrusLogger) WithFields(fields Fields) Logger {
	return LogrusLogger{entry: l.entry.WithFields(fields)}
}

// WithError creates a new logger with an appended error field.
func (l LogrusLogger) WithError(err error) Logger {
	return LogrusLogger{entry: l.entry.WithError(err)}
}

// Debug writes a log message at debug level.
func (l LogrusLogger) Debug(msg string) {
	l.entry.Debug(msg)
}

// Info writes a log message at info level.
func (l LogrusLogger) Info(msg string) {
	l.entry.Info(msg)
}

// Warn writes a log message at warn level.
func (l LogrusLogger) Warn(msg string) {
	l.entry.Warn(msg)
}

// Error writes a log message at error level.
func (l LogrusLogger) Error(msg string) {
	l.entry.Error(msg)
}

// ToContext injects the logger into the given context so that it can be retrieved via `FromContext()`.
func (l LogrusLogger) ToContext(ctx context.Context) context.Context {
	return ctxlogrus.ToContext(ctx, l.entry)
}

// StreamServerInterceptor creates a gRPC interceptor that generates log messages for streaming RPC calls.
func (l LogrusLogger) StreamServerInterceptor(opts ...grpcmwlogrus.Option) grpc.StreamServerInterceptor {
	return grpcmwlogrus.StreamServerInterceptor(l.entry, opts...)
}

// UnaryServerInterceptor creates a gRPC interceptor that generates log messages for unary RPC calls.
func (l LogrusLogger) UnaryServerInterceptor(opts ...grpcmwlogrus.Option) grpc.UnaryServerInterceptor {
	return grpcmwlogrus.UnaryServerInterceptor(l.entry, opts...)
}

func (l LogrusLogger) log(ctx context.Context, level logrus.Level, msg string) {
	middlewareFields := ConvertLoggingFields(grpcmwloggingv2.ExtractFields(ctx))
	l.entry.WithFields(ctxlogrus.Extract(ctx).Data).WithFields(middlewareFields).Log(level, msg)
}

// DebugContext logs a new log message at Debug level. Fields added to the context via AddFields will be appended.
func (l LogrusLogger) DebugContext(ctx context.Context, msg string) {
	l.log(ctx, logrus.DebugLevel, msg)
}

// InfoContext logs a new log message at Info level. Fields added to the context via AddFields will be appended.
func (l LogrusLogger) InfoContext(ctx context.Context, msg string) {
	l.log(ctx, logrus.InfoLevel, msg)
}

// WarnContext logs a new log message at Warn level. Fields added to the context via AddFields will be appended.
func (l LogrusLogger) WarnContext(ctx context.Context, msg string) {
	l.log(ctx, logrus.WarnLevel, msg)
}

// ErrorContext level. Fields added to the context via AddFields will be appended.
func (l LogrusLogger) ErrorContext(ctx context.Context, msg string) {
	l.log(ctx, logrus.ErrorLevel, msg)
}

// Data returns the data stored inside the logger entry.
func (l LogrusLogger) Data() Fields {
	return l.entry.Data
}

// FromContext extracts the logger from the context. If no logger has been injected then this will
// return a discarding logger.
func FromContext(ctx context.Context) LogrusLogger {
	return LogrusLogger{
		entry: ctxlogrus.Extract(ctx),
	}
}

// AddFields adds the given log fields to the context so that it will be used by any logging function like
// `InfoContext()` that receives a context as input.
func AddFields(ctx context.Context, fields Fields) {
	ctxlogrus.AddFields(ctx, fields)
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
