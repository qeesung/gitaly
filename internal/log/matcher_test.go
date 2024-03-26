package log

import (
	"context"
	"testing"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestLogMatcher(t *testing.T) {
	methodNames := []string{
		"/grpc.health.v1.Health/Check",
		"/gitaly.SmartHTTPService/InfoRefsUploadPack",
		"/gitaly.SmartHTTPService/PostUploadPackWithSidechannel",
	}

	for _, tc := range []struct {
		desc             string
		skip             string
		only             string
		shouldLogMethods []string
	}{
		{
			desc:             "default setting",
			skip:             "",
			only:             "",
			shouldLogMethods: []string{"InfoRefsUploadPack", "PostUploadPackWithSidechannel"},
		},
		{
			desc:             "allow all",
			skip:             "",
			only:             ".",
			shouldLogMethods: []string{"Check", "InfoRefsUploadPack", "PostUploadPackWithSidechannel"},
		},
		{
			desc:             "only log Check",
			skip:             "",
			only:             "^/grpc.health.v1.Health/Check$",
			shouldLogMethods: []string{"Check"},
		},
		{
			desc:             "skip log Check",
			skip:             "^/grpc.health.v1.Health/Check$",
			only:             "",
			shouldLogMethods: []string{"InfoRefsUploadPack", "PostUploadPackWithSidechannel"},
		},
		{
			// If condition 'only' exists, ignore condition 'skip'
			desc:             "only log Check and ignore skip setting",
			skip:             "^/grpc.health.v1.Health/Check$",
			only:             "^/grpc.health.v1.Health/Check$",
			shouldLogMethods: []string{"Check"},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Setenv("GITALY_LOG_REQUEST_METHOD_DENY_PATTERN", tc.skip)
			t.Setenv("GITALY_LOG_REQUEST_METHOD_ALLOW_PATTERN", tc.only)

			ctx := context.Background()
			logger, hook := NewTestLogger()

			interceptor := selector.UnaryServerInterceptor(logger.UnaryServerInterceptor(nil), NewLogMatcher())

			for _, methodName := range methodNames {
				_, err := interceptor(
					ctx,
					nil,
					&grpc.UnaryServerInfo{FullMethod: methodName},
					func(ctx context.Context, req interface{}) (interface{}, error) {
						return nil, nil
					},
				)
				require.NoError(t, err)
			}

			entries := hook.Entries()
			require.Len(t, entries, len(tc.shouldLogMethods))
			for idx, entry := range entries {
				require.Equal(t, entry.Msg, "finished unary call with code OK")

				require.Len(t, entry.Attrs, 1)
				require.Equal(t, "grpc.method", entry.Attrs[0].Key)
				require.Equal(t, tc.shouldLogMethods[idx], entry.Attrs[0].Value.String())
			}
		})
	}
}
