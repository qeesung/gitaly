package log

import (
	"bytes"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigure(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	testHook := NewURLSanitizerHook()

	for _, tc := range []struct {
		desc           string
		format         string
		level          string
		hooks          []logrus.Hook
		expectedLogger *logrus.Logger
	}{
		{
			desc:   "json format with info level",
			format: "json",
			expectedLogger: func() *logrus.Logger {
				logger := newLogger()
				logger.Out = &out
				logger.Formatter = UTCJsonFormatter()
				logger.Level = logrus.InfoLevel
				return logger
			}(),
		},
		{
			desc:   "text format with info level",
			format: "text",
			expectedLogger: func() *logrus.Logger {
				logger := newLogger()
				logger.Out = &out
				logger.Formatter = UTCTextFormatter()
				logger.Level = logrus.InfoLevel
				return logger
			}(),
		},
		{
			desc: "empty format with info level",
			expectedLogger: func() *logrus.Logger {
				logger := newLogger()
				logger.Out = &out
				logger.Formatter = UTCTextFormatter()
				logger.Level = logrus.InfoLevel
				return logger
			}(),
		},
		{
			desc:   "text format with debug level",
			format: "text",
			level:  "debug",
			expectedLogger: func() *logrus.Logger {
				logger := newLogger()
				logger.Out = &out
				logger.Formatter = UTCTextFormatter()
				logger.Level = logrus.DebugLevel
				return logger
			}(),
		},
		{
			desc:   "text format with invalid level",
			format: "text",
			level:  "invalid-level",
			expectedLogger: func() *logrus.Logger {
				logger := newLogger()
				logger.Out = &out
				logger.Formatter = UTCTextFormatter()
				logger.Level = logrus.InfoLevel
				return logger
			}(),
		},
		{
			desc:   "with hook",
			format: "text",
			level:  "info",
			hooks: []logrus.Hook{
				testHook,
			},
			expectedLogger: func() *logrus.Logger {
				logger := newLogger()
				logger.Out = &out
				logger.Formatter = UTCTextFormatter()
				logger.Level = logrus.InfoLevel
				logger.Hooks.Add(testHook)
				return logger
			}(),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			out.Reset()

			logger := newLogger()
			require.NoError(t, configure(logger, &out, tc.format, tc.level, tc.hooks...))

			// We cannot directly compare the loggers with each other because they contain function
			// pointers, so we have to check the relevant fields one by one.
			require.Equal(t, tc.expectedLogger.Out, logger.Out)
			require.Equal(t, tc.expectedLogger.Level, logger.Level)
			require.Equal(t, tc.expectedLogger.Hooks, logger.Hooks)
			require.Equal(t, tc.expectedLogger.Formatter, logger.Formatter)

			now := time.Now()
			nowUTCFormatted := now.UTC().Format(LogTimestampFormatUTC)

			message := "this is a logging message."

			entry := logger.WithTime(now)

			switch tc.level {
			case "debug":
				entry.Debug(message)
			case "warn":
				entry.Warn(message)
			case "error":
				entry.Error(message)
			case "", "info":
				entry.Info(message)
			default:
				entry.Info(message)
			}

			if tc.format != "" {
				assert.Contains(t, out.String(), nowUTCFormatted)
			}
			assert.Contains(t, out.String(), message)
		})
	}
}

func TestMapGRPCLogLevel(t *testing.T) {
	for _, tc := range []struct {
		desc             string
		environmentLevel string
		level            string
		expectedLevel    string
	}{
		{
			desc:          "error stays unmodified",
			level:         "error",
			expectedLevel: "error",
		},
		{
			desc:          "warning stays unmodified",
			level:         "warning",
			expectedLevel: "warning",
		},
		{
			desc:          "info gets mapped",
			level:         "info",
			expectedLevel: "warning",
		},
		{
			desc:          "empty level gets mapped",
			level:         "",
			expectedLevel: "warning",
		},
		{
			desc:             "environment overrides value",
			environmentLevel: "ERROR",
			level:            "info",
			expectedLevel:    "error",
		},
		{
			desc:             "info in environment does not get mapped",
			environmentLevel: "info",
			level:            "info",
			expectedLevel:    "info",
		},
		{
			desc:             "unknown value in environment uses level",
			environmentLevel: "unknown",
			level:            "warning",
			expectedLevel:    "warning",
		},
		{
			desc:             "unknown value in environment uses mapping for info",
			environmentLevel: "unknown",
			level:            "info",
			expectedLevel:    "warning",
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Setenv("GRPC_GO_LOG_SEVERITY_LEVEL", tc.environmentLevel)
			require.Equal(t, tc.expectedLevel, mapGRPCLogLevel(tc.level))
		})
	}
}
