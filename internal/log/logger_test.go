package log

import (
	"testing"

	grpcmwloggingv2 "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/stretchr/testify/require"
)

func TestConvertLoggingFields(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		desc     string
		input    grpcmwloggingv2.Fields
		expected map[string]any
	}{
		{
			desc:     "Converting v2 logging fields to map[string]any, even number of fields",
			input:    grpcmwloggingv2.Fields{"k1", "v1", "k2", "v2"},
			expected: map[string]any{"k1": "v1", "k2": "v2"},
		},
		{
			desc:     "Converting v2 logging fields to map[string]any, odd number of fields",
			input:    grpcmwloggingv2.Fields{"k1", "v1", "k2"},
			expected: map[string]any{"k1": "v1", "k2": ""},
		},
		{
			desc:     "Converting v2 logging fields to map[string]any, duplicate keys",
			input:    grpcmwloggingv2.Fields{"k1", "v1", "k1", "v2"},
			expected: map[string]any{"k1": "v2"},
		},
		{
			desc:     "Converting v2 logging fields to map[string]any, empty input",
			input:    grpcmwloggingv2.Fields{},
			expected: map[string]any{},
		},
		{
			desc:     "Converting v2 logging fields to map[string]any, nil input",
			input:    nil,
			expected: map[string]any{},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			actual := ConvertLoggingFields(tc.input)
			require.Equal(t, tc.expected, actual)
		})
	}
}
