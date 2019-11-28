package featureflag

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/metadata"
)

func TestGRPCMetadataFeatureFlag(t *testing.T) {
	testCases := []struct {
		flag    string
		headers map[string]string
		enabled bool
		desc    string
	}{
		{"", nil, false, "empty name and no headers"},
		{"flag", nil, false, "no headers"},
		{"flag", map[string]string{"flag": "true"}, false, "no 'gitaly-feature' prefix in flag name"},
		{"flag", map[string]string{"gitaly-feature-flag": "TRUE"}, false, "not valid header value"},
		{"flag_under_score", map[string]string{"gitaly-feature-flag-under-score": "true"}, true, "flag name with underscores"},
		{"flag-dash-ok", map[string]string{"gitaly-feature-flag-dash-ok": "true"}, true, "flag name with dashes"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			md := metadata.New(tc.headers)
			ctx := metadata.NewIncomingContext(context.Background(), md)

			assert.Equal(t, tc.enabled, IsEnabled(ctx, tc.flag))
			assert.NotEqual(t, tc.enabled, IsDisabled(ctx, tc.flag))
		})
	}
}
