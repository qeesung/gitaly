package log

import (
	"io"
	"os"

	grpcmwlogrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/sirupsen/logrus"
)

const (
	// GitalyLogDirEnvKey defines the environment variable used to specify the Gitaly log directory
	GitalyLogDirEnvKey = "GITALY_LOG_DIR"
	// LogTimestampFormat defines the timestamp format in log files
	LogTimestampFormat = "2006-01-02T15:04:05.000"
	// LogTimestampFormatUTC defines the utc timestamp format in log files
	LogTimestampFormatUTC = "2006-01-02T15:04:05.000Z"
)

type utcFormatter struct {
	logrus.Formatter
}

func (u utcFormatter) Format(e *logrus.Entry) ([]byte, error) {
	e.Time = e.Time.UTC()
	return u.Formatter.Format(e)
}

// UTCJsonFormatter returns a Formatter that formats a logrus Entry's as json and converts the time
// field into UTC
func UTCJsonFormatter() logrus.Formatter {
	return &utcFormatter{Formatter: &logrus.JSONFormatter{TimestampFormat: LogTimestampFormatUTC}}
}

// UTCTextFormatter returns a Formatter that formats a logrus Entry's as text and converts the time
// field into UTC
func UTCTextFormatter() logrus.Formatter {
	return &utcFormatter{Formatter: &logrus.TextFormatter{TimestampFormat: LogTimestampFormatUTC}}
}

// defaultLogger is the logger that is returned via `Default()` and via logrus' direct log invocations like e.g.
// `logrus.Info()`. By default it is configured to log to standard output, but in practice it should be configured via
// a call to `Configure()` after the configuration has been loaded.
var defaultLogger = func() *logrus.Logger {
	logger := logrus.StandardLogger()
	logger.Out = os.Stdout
	return logger
}()

// Config contains logging configuration values
type Config struct {
	Dir    string `toml:"dir,omitempty" json:"dir"`
	Format string `toml:"format,omitempty" json:"format"`
	Level  string `toml:"level,omitempty" json:"level"`
}

// Configure configures the default and gRPC loggers. The gRPC logger's log level will be mapped in order to decrease
// its default verbosity.
func Configure(out io.Writer, format string, level string, hooks ...logrus.Hook) {
	configure(defaultLogger, out, format, level, hooks...)

	// We replace the gRPC logger with a custom one because the default one is too chatty.
	grpcLogger := logrus.New()
	configure(grpcLogger, out, format, mapGRPCLogLevel(level), hooks...)
	grpcmwlogrus.ReplaceGrpcLogger(grpcLogger.WithField("pid", os.Getpid()))
}

func configure(logger *logrus.Logger, out io.Writer, format, level string, hooks ...logrus.Hook) {
	var formatter logrus.Formatter
	switch format {
	case "json":
		formatter = UTCJsonFormatter()
	case "", "text":
		formatter = UTCTextFormatter()
	default:
		logrus.WithField("format", format).Fatal("invalid logger format")
	}

	logrusLevel, err := logrus.ParseLevel(level)
	if err != nil {
		logrusLevel = logrus.InfoLevel
	}

	logger.Out = out
	logger.SetLevel(logrusLevel)
	logger.Formatter = formatter
	for _, hook := range hooks {
		logger.Hooks.Add(hook)
	}
}

func mapGRPCLogLevel(level string) string {
	// Honor grpc-go's debug settings: https://github.com/grpc/grpc-go#how-to-turn-on-logging
	switch os.Getenv("GRPC_GO_LOG_SEVERITY_LEVEL") {
	case "ERROR", "error":
		return "error"
	case "WARNING", "warning":
		return "warning"
	case "INFO", "info":
		return "info"
	}

	// grpc-go is too verbose at level 'info'. So when config.toml requests
	// level info, we tell grpc-go to log at 'warn' instead.
	if level == "info" {
		return "warning"
	}

	return level
}

// Default returns the default logger that has been configured via `Configure()`.
func Default() *logrus.Entry { return defaultLogger.WithField("pid", os.Getpid()) }
