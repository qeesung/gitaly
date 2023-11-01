package sentryhandler

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	sentry "github.com/getsentry/sentry-go"
	grpcmw "github.com/grpc-ecosystem/go-grpc-middleware/v2"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/middleware/requestinfohandler"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

var (
	ignoredCodes = []codes.Code{
		// OK means there was no error
		codes.OK,
		// Canceled and DeadlineExceeded indicate clients that disappeared or lost interest
		codes.Canceled,
		codes.DeadlineExceeded,
		// We use FailedPrecondition to signal error conditions that are 'normal'
		codes.FailedPrecondition,
	}
	method2ignoredCodes = map[string][]codes.Code{
		"/gitaly.CommitService/TreeEntry": {
			// NotFound is returned when a file is not found.
			codes.NotFound,
		},
	}
)

// Option is an option that can be passed to UnaryLogHandler or StreamLogHandler in order to modify their default
// behaviour.
type Option func(cfg *config)

// WithEventReporter overrides the function that is used to report events to Sentry. The only intended purpose of this
// function is to override this function during tests.
func WithEventReporter(reporter func(*sentry.Event) *sentry.EventID) Option {
	return func(cfg *config) {
		cfg.eventReporter = reporter
	}
}

type config struct {
	eventReporter func(*sentry.Event) *sentry.EventID
}

func configFromOptions(opts ...Option) config {
	cfg := config{
		eventReporter: sentry.CaptureEvent,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	return cfg
}

type skipSubmissionKey struct{}

// UnaryLogHandler handles access times and errors for unary RPC's. Its default behaviour can be changed by passing
// Options.
func UnaryLogHandler(opts ...Option) grpc.UnaryServerInterceptor {
	cfg := configFromOptions(opts...)

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()

		ctx = context.WithValue(ctx, skipSubmissionKey{}, new(bool))

		resp, err := handler(ctx, req)
		if err != nil {
			logGrpcErrorToSentry(ctx, info.FullMethod, start, err, cfg.eventReporter)
		}

		return resp, err
	}
}

// StreamLogHandler handles access times and errors for stream RPC's. Its default behaviour can be changed by passing
// options.
func StreamLogHandler(opts ...Option) grpc.StreamServerInterceptor {
	cfg := configFromOptions(opts...)

	return func(srv interface{}, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()

		ctx := stream.Context()
		ctx = context.WithValue(ctx, skipSubmissionKey{}, new(bool))

		wrappedStream := grpcmw.WrapServerStream(stream)
		wrappedStream.WrappedContext = ctx

		err := handler(srv, wrappedStream)
		if err != nil {
			logGrpcErrorToSentry(ctx, info.FullMethod, start, err, cfg.eventReporter)
		}

		return err
	}
}

func methodToCulprit(methodName string) string {
	methodName = strings.TrimPrefix(methodName, "/gitaly.")
	methodName = strings.Replace(methodName, "/", "::", 1)
	return methodName
}

func logErrorToSentry(ctx context.Context, method string, err error) (code codes.Code, bypass bool) {
	code = structerr.GRPCCode(err)

	for _, ignoredCode := range ignoredCodes {
		if code == ignoredCode {
			return code, true
		}
	}

	for _, ignoredCode := range method2ignoredCodes[method] {
		if code == ignoredCode {
			return code, true
		}
	}

	skipSubmission, ok := ctx.Value(skipSubmissionKey{}).(*bool)
	if ok && *skipSubmission {
		return code, true
	}

	return code, false
}

func generateSentryEvent(ctx context.Context, method string, duration time.Duration, err error) *sentry.Event {
	grpcErrorCode, bypass := logErrorToSentry(ctx, method, err)
	if bypass {
		return nil
	}

	event := sentry.NewEvent()
	if info := requestinfohandler.Extract(ctx); info != nil {
		for k, v := range info.Tags() {
			event.Tags[k] = v
		}
	}

	for k, v := range map[string]string{
		"grpc.code":    grpcErrorCode.String(),
		"grpc.method":  method,
		"grpc.time_ms": fmt.Sprintf("%.0f", duration.Seconds()*1000),
		"system":       "grpc",
	} {
		event.Tags[k] = v
	}

	event.Message = err.Error()

	// Skip the stacktrace as it's not helpful in this context
	event.Exception = append(event.Exception, newException(err, nil))

	grpcMethod := methodToCulprit(method)

	// Details on fingerprinting
	// https://docs.sentry.io/learn/rollups/#customize-grouping-with-fingerprints
	event.Fingerprint = []string{"grpc", grpcMethod, grpcErrorCode.String()}
	event.Transaction = grpcMethod

	return event
}

func logGrpcErrorToSentry(ctx context.Context, method string, start time.Time, err error, reporter func(*sentry.Event) *sentry.EventID) {
	event := generateSentryEvent(ctx, method, time.Since(start), err)
	if event == nil {
		return
	}

	reporter(event)
}

var errorMsgPattern = regexp.MustCompile(`\A(\w+): (.+)\z`)

// newException constructs an Exception using provided Error and Stacktrace
func newException(err error, stacktrace *sentry.Stacktrace) sentry.Exception {
	msg := err.Error()
	ex := sentry.Exception{
		Stacktrace: stacktrace,
		Value:      msg,
		Type:       reflect.TypeOf(err).String(),
	}
	if m := errorMsgPattern.FindStringSubmatch(msg); m != nil {
		ex.Module, ex.Value = m[1], m[2]
	}
	return ex
}

// MarkToSkip propagate context with a special tag that signals to sentry handler that the error must not be reported.
func MarkToSkip(ctx context.Context) {
	skipSubmission, ok := ctx.Value(skipSubmissionKey{}).(*bool)
	if !ok {
		return
	}

	*skipSubmission = true
}
