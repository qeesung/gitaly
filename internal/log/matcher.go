package log

import (
	"context"
	"regexp"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/env"
)

const (
	defaultLogRequestMethodAllowPattern = ""
	defaultLogRequestMethodDenyPattern  = "^/grpc.health.v1.Health/Check$"
)

// NewLogMatcher provides a new logMatcher.
func NewLogMatcher() *logMatcher {
	return &logMatcher{}
}

type logMatcher struct{}

// Match satisfies the selector.Match interface, this allows to
// include/exclude RPCs while logging.
func (l logMatcher) Match(ctx context.Context, callMeta interceptors.CallMeta) bool {
	// If "GITALY_LOG_REQUEST_METHOD_ALLOW_PATTERN" ENV variable is set,
	// logger will only keep the log whose "fullMethodName" matches it.
	if pattern := env.GetString("GITALY_LOG_REQUEST_METHOD_ALLOW_PATTERN",
		defaultLogRequestMethodAllowPattern); pattern != "" {
		methodRegex := regexp.MustCompile(pattern)

		return methodRegex.MatchString(callMeta.FullMethod())
	}

	// If "GITALY_LOG_REQUEST_METHOD_DENY_PATTERN" ENV variable is set,
	// logger will filter out the log whose "fullMethodName" matches it.
	// Note that "GITALY_LOG_REQUEST_METHOD_ALLOW_PATTERN" takes preference.
	if pattern := env.GetString("GITALY_LOG_REQUEST_METHOD_DENY_PATTERN",
		defaultLogRequestMethodDenyPattern); pattern != "" {
		methodRegex := regexp.MustCompile(pattern)

		return !methodRegex.MatchString(callMeta.FullMethod())
	}

	return true
}
