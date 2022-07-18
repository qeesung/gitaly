//go:build !gitaly_test_sha256

package git

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateRevision(t *testing.T) {
	for _, tc := range []struct {
		desc        string
		revision    string
		expectedErr error
	}{
		{
			desc:        "empty revision",
			revision:    "",
			expectedErr: fmt.Errorf("empty revision"),
		},
		{
			desc:     "valid revision",
			revision: "foo/bar",
		},
		{
			desc:        "leading dash",
			revision:    "-foo/bar",
			expectedErr: fmt.Errorf("revision can't start with '-'"),
		},
		{
			desc:     "intermediate dash",
			revision: "foo-bar",
		},
		{
			desc:        "space",
			revision:    "foo bar",
			expectedErr: fmt.Errorf("revision can't contain whitespace"),
		},
		{
			desc:        "newline",
			revision:    "foo\nbar",
			expectedErr: fmt.Errorf("revision can't contain whitespace"),
		},
		{
			desc:        "tab",
			revision:    "foo\tbar",
			expectedErr: fmt.Errorf("revision can't contain whitespace"),
		},
		{
			desc:        "carriage-return",
			revision:    "foo\rbar",
			expectedErr: fmt.Errorf("revision can't contain whitespace"),
		},
		{
			desc:        "NUL-byte",
			revision:    "foo\x00bar",
			expectedErr: fmt.Errorf("revision can't contain NUL"),
		},
		{
			desc:        "colon",
			revision:    "foo/bar:baz",
			expectedErr: fmt.Errorf("revision can't contain ':'"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			require.Equal(t, tc.expectedErr, ValidateRevision([]byte(tc.revision)))

			// `ValidateRevision()` and `ValidateRevisionAllowEmpty()` behave the same,
			// except in the case where the revision is empty. In that case, the latter
			// does not return an error.
			if tc.revision == "" {
				tc.expectedErr = nil
			}
			require.Equal(t, tc.expectedErr, ValidateRevisionAllowEmpty([]byte(tc.revision)))
		})
	}
}
