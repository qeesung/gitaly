package log

import (
	"context"
	"sync"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors"
	grpcmwloggingv2 "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	grpcmwselector "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
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

	StreamServerInterceptor(grpcmwloggingv2.Logger, ...Option) grpc.StreamServerInterceptor
	UnaryServerInterceptor(grpcmwloggingv2.Logger, ...Option) grpc.UnaryServerInterceptor

	ReplaceFields(fields Fields) Logger
}

// threadSafeEntry is a wrapper around logrus.Entry with lock that provides thread safety.
type threadSafeEntry struct {
	entry *logrus.Entry
	lock  *sync.Mutex
}

// LogrusLogger is an implementation of the Logger interface that is implemented via a `logrus.FieldLogger`.
type LogrusLogger struct {
	entryWrapper *threadSafeEntry
}

// FromLogrusEntry constructs a new Gitaly-specific logger from a `logrus.Logger`.
func FromLogrusEntry(entry *logrus.Entry) LogrusLogger {
	return LogrusLogger{entryWrapper: &threadSafeEntry{entry: entry, lock: &sync.Mutex{}}}
}

// LogrusEntry returns the `logrus.Entry` that backs this logger. Note that this interface only exists during the
// transition period and will be eventually removed. It is thus heavily discouraged to use it.
//
// Deprecated: This will be removed once all callsites have been converted to do something that is independent of the
// logrus logger.
func (l LogrusLogger) LogrusEntry() *logrus.Entry {
	l.entryWrapper.lock.Lock()
	defer l.entryWrapper.lock.Unlock()
	return l.entryWrapper.entry
}

// WithField creates a new logger with the given field appended.
func (l LogrusLogger) WithField(key string, value any) Logger {
	l.entryWrapper.lock.Lock()
	defer l.entryWrapper.lock.Unlock()
	return LogrusLogger{
		entryWrapper: &threadSafeEntry{
			l.entryWrapper.entry.WithField(key, value),
			l.entryWrapper.lock,
		},
	}
}

// ReplaceFields creates a new logger with old fields truncated and replaced by new fields.
func (l LogrusLogger) ReplaceFields(fields Fields) Logger {
	l.entryWrapper.lock.Lock()
	defer l.entryWrapper.lock.Unlock()
	l.entryWrapper.entry.Data = Fields{}
	return LogrusLogger{
		entryWrapper: &threadSafeEntry{
			l.entryWrapper.entry.WithFields(fields),
			l.entryWrapper.lock,
		},
	}
}

// WithFields creates a new logger with the given fields appended.
func (l LogrusLogger) WithFields(fields Fields) Logger {
	l.entryWrapper.lock.Lock()
	defer l.entryWrapper.lock.Unlock()
	return LogrusLogger{
		entryWrapper: &threadSafeEntry{
			l.entryWrapper.entry.WithFields(fields),
			l.entryWrapper.lock,
		},
	}
}

// WithError creates a new logger with an appended error field.
func (l LogrusLogger) WithError(err error) Logger {
	l.entryWrapper.lock.Lock()
	defer l.entryWrapper.lock.Unlock()
	return LogrusLogger{
		entryWrapper: &threadSafeEntry{
			l.entryWrapper.entry.WithError(err),
			l.entryWrapper.lock,
		},
	}
}

// Debug writes a log message at debug level.
func (l LogrusLogger) Debug(msg string) {
	l.entryWrapper.lock.Lock()
	defer l.entryWrapper.lock.Unlock()
	l.entryWrapper.entry.Debug(msg)
}

// Info writes a log message at info level.
func (l LogrusLogger) Info(msg string) {
	l.entryWrapper.lock.Lock()
	defer l.entryWrapper.lock.Unlock()
	l.entryWrapper.entry.Info(msg)
}

// Warn writes a log message at warn level.
func (l LogrusLogger) Warn(msg string) {
	l.entryWrapper.lock.Lock()
	defer l.entryWrapper.lock.Unlock()
	l.entryWrapper.entry.Warn(msg)
}

// Error writes a log message at error level.
func (l LogrusLogger) Error(msg string) {
	l.entryWrapper.lock.Lock()
	defer l.entryWrapper.lock.Unlock()
	l.entryWrapper.entry.Error(msg)
}

// toContext injects the logger into the given context so that it can be retrieved via `FromContext()`.
func (l LogrusLogger) toContext(ctx context.Context) context.Context {
	if l.entryWrapper == nil {
		return ctxlogrus.ToContext(ctx, nil)
	}
	return ctxlogrus.ToContext(ctx, l.entryWrapper.entry)
}

// StreamServerInterceptor creates a new stream server interceptor. The loggerFunc is the function that will be called to
// log messages; options are the Option slice that can be used to configure the interceptor.
func (l LogrusLogger) StreamServerInterceptor(loggerFunc grpcmwloggingv2.Logger, options ...Option) grpc.StreamServerInterceptor {
	o := evaluateServerOpt(options)
	interceptor := interceptors.StreamServerInterceptor(reportable(loggerFunc, o))
	if o.matcher != nil {
		interceptor = grpcmwselector.StreamServerInterceptor(interceptor, *o.matcher)
	}
	return interceptor
}

// UnaryServerInterceptor creates a new unary server interceptor. The loggerFunc is the function that will be called to
// log messages; options are the Option slice that can be used to configure the interceptor.
func (l LogrusLogger) UnaryServerInterceptor(loggerFunc grpcmwloggingv2.Logger, options ...Option) grpc.UnaryServerInterceptor {
	o := evaluateServerOpt(options)
	interceptor := interceptors.UnaryServerInterceptor(reportable(loggerFunc, o))
	if o.matcher != nil {
		interceptor = grpcmwselector.UnaryServerInterceptor(interceptor, *o.matcher)
	}
	return interceptor
}

func (l LogrusLogger) log(ctx context.Context, level logrus.Level, msg string) {
	l.entryWrapper.lock.Lock()
	defer l.entryWrapper.lock.Unlock()
	middlewareFields := ConvertLoggingFields(grpcmwloggingv2.ExtractFields(ctx))
	l.entryWrapper.entry.WithFields(ctxlogrus.Extract(ctx).Data).WithFields(middlewareFields).Log(level, msg)
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

// fromContext extracts the logger from the context. If no logger has been injected then this will return a discarding
// logger.
func fromContext(ctx context.Context) LogrusLogger {
	return LogrusLogger{
		entryWrapper: &threadSafeEntry{
			ctxlogrus.Extract(ctx),
			&sync.Mutex{},
		},
	}
}

// AddFields adds the given log fields to the context so that it will be used by any logging function like
// `InfoContext()` that receives a context as input.
func AddFields(ctx context.Context, fields Fields) {
	ctxlogrus.AddFields(ctx, fields)
}
