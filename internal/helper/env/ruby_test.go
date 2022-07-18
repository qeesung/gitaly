//go:build !gitaly_test_sha256

package env

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAllowedRubyEnvironment(t *testing.T) {
	for _, tc := range []struct {
		desc     string
		input    []string
		expected []string
	}{
		{
			desc: "empty",
		},
		{
			desc: "unrelated env",
			input: []string{
				"FOO=bar",
				"BAR=baz",
			},
		},
		{
			desc: "allowed envvars",
			input: []string{
				"BUNDLE_PATH=a",
				"BUNDLE_APP_CONFIG=b",
				"BUNDLE_USER_CONFIG=c",
				"GEM_HOME=y",
			},
			expected: []string{
				"BUNDLE_PATH=a",
				"BUNDLE_APP_CONFIG=b",
				"BUNDLE_USER_CONFIG=c",
				"GEM_HOME=y",
			},
		},
		{
			desc: "mixed",
			input: []string{
				"FOO=bar",
				"BUNDLE_PATH=a",
				"FOO=bar",
				"BUNDLE_APP_CONFIG=b",
				"FOO=bar",
				"BUNDLE_USER_CONFIG=c",
				"FOO=bar",
				"GEM_HOME=d",
				"FOO=bar",
			},
			expected: []string{
				"BUNDLE_PATH=a",
				"BUNDLE_APP_CONFIG=b",
				"BUNDLE_USER_CONFIG=c",
				"GEM_HOME=d",
			},
		},
		{
			desc: "almost-prefixes",
			input: []string{
				"BUNDLE_PATHx=a",
				"BUNDLE_APP_CONFIGx=b",
				"BUNDLE_USER_CONFIGx=c",
				"GEM_HOMEx=d",
			},
		},
		{
			desc: "duplicate entries",
			input: []string{
				"GEM_HOME=first",
				"BUNDLE_APP_CONFIG=first",
				"BUNDLE_USER_CONFIG=first",
				"BUNDLE_PATH=first",
				"BUNDLE_PATH=second",
				"BUNDLE_USER_CONFIG=second",
				"BUNDLE_APP_CONFIG=second",
				"GEM_HOME=second",
			},
			expected: []string{
				"GEM_HOME=first",
				"BUNDLE_APP_CONFIG=first",
				"BUNDLE_USER_CONFIG=first",
				"BUNDLE_PATH=first",
				"BUNDLE_PATH=second",
				"BUNDLE_USER_CONFIG=second",
				"BUNDLE_APP_CONFIG=second",
				"GEM_HOME=second",
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, tc.expected, AllowedRubyEnvironment(tc.input))
		})
	}
}
