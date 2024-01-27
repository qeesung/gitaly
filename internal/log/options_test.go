package log

import (
	"context"
	"testing"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/stretchr/testify/require"
)

func TestLoggerOptions(t *testing.T) {
	t.Run("WithTimestampFormat", func(t *testing.T) {
		format := "2006-01-02-gitaly-format-test"
		opt := evaluateServerOpt([]Option{WithTimestampFormat(format)})
		require.Equal(t, opt.timestampFormat, format)
	})

	t.Run("WithFiledProducers", func(t *testing.T) {
		fieldsProducers := []FieldsProducer{
			func(context.Context, error) Fields {
				return Fields{"a": 1}
			},
			func(context.Context, error) Fields {
				return Fields{"b": "test"}
			},
			func(ctx context.Context, err error) Fields {
				return Fields{"c": err.Error()}
			},
		}
		opt := evaluateServerOpt([]Option{WithFiledProducers(fieldsProducers...)})
		require.Equal(t, opt.filedProducers, fieldsProducers)
	})

	t.Run("WithLogEvents", func(t *testing.T) {
		events := []logging.LoggableEvent{
			logging.StartCall, logging.PayloadReceived, logging.PayloadSent, logging.FinishCall,
		}
		opt := evaluateServerOpt([]Option{WithLogOnEvents(events...)})
		require.Equal(t, opt.loggableEvents, events)
	})

	t.Run("WithMatcher", func(t *testing.T) {
		matcher := DeciderMatcher()
		opt := evaluateServerOpt([]Option{WithMatcher(matcher)})
		require.Equal(t, opt.matcher, matcher)
	})
}
