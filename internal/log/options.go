package log

import (
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
)

var defaultOptions = &options{
	loggableEvents:    []logging.LoggableEvent{logging.StartCall, logging.FinishCall},
	codeFunc:          logging.DefaultErrorToCode,
	durationFieldFunc: logging.DefaultDurationToFields,
	matcher:           nil,
	// levelFunc depends if it's client or server.
	levelFunc:       nil,
	filedProducers:  make([]FieldsProducer, 0),
	timestampFormat: time.RFC3339,
}

type options struct {
	levelFunc         logging.CodeToLevel
	loggableEvents    []logging.LoggableEvent
	codeFunc          logging.ErrorToCode
	durationFieldFunc logging.DurationToFields
	matcher           *selector.Matcher
	timestampFormat   string
	filedProducers    []FieldsProducer
}

// Option is used to customize the interceptor behavior.
// Use the With* functions (e.g. WithTimestampFormat) to create an Option.
type Option func(*options)

func evaluateServerOpt(opts []Option) *options {
	optCopy := &options{}
	*optCopy = *defaultOptions
	optCopy.levelFunc = logging.DefaultServerCodeToLevel
	for _, o := range opts {
		o(optCopy)
	}
	return optCopy
}

func hasEvent(events []logging.LoggableEvent, event logging.LoggableEvent) bool {
	for _, e := range events {
		if e == event {
			return true
		}
	}
	return false
}

// WithFiledProducers customizes the log fields with FieldsProducer.
// The fields produced by the producers will be appended to the log fields
func WithFiledProducers(producers ...FieldsProducer) Option {
	return func(o *options) {
		o.filedProducers = producers
	}
}

// WithTimestampFormat customizes the timestamps emitted in the log fields.
func WithTimestampFormat(format string) Option {
	return func(o *options) {
		o.timestampFormat = format
	}
}

// WithLogOnEvents customizes on what events the gRPC interceptor should log on.
func WithLogOnEvents(events ...logging.LoggableEvent) Option {
	return func(o *options) {
		o.loggableEvents = events
	}
}

// WithMatcher customizes the matcher used to select the gRPC method calls to log.
func WithMatcher(matcher *selector.Matcher) Option {
	return func(o *options) {
		o.matcher = matcher
	}
}
