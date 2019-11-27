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
	}{
		{"", nil, false},
		{"flag", nil, false},
		{"flag", map[string]string{"flag": "true"}, false},
		{"flag", map[string]string{"gitaly-feature-flag": "TRUE"}, false},
		{"flag_underscore", map[string]string{"gitaly-feature-flag-underscore": "true"}, true},
		{"flag-dash", map[string]string{"gitaly-feature-flag-dash": "true"}, true},
	}

	for _, tc := range testCases {
		md := metadata.New(tc.headers)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		assert.Equal(t, tc.enabled, IsEnabled(ctx, tc.flag), tc.flag)
		assert.NotEqual(t, tc.enabled, IsDisabled(ctx, tc.flag), tc.flag)
	}
}
