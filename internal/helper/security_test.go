package helper

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidateRelativePath(t *testing.T) {
	for _, tc := range []struct {
		path    string
		cleaned string
		error   error
	}{
		{"/parent", "parent", nil},
		{"parent/", "parent", nil},
		{"/parent-with-suffix", "parent-with-suffix", nil},
		{"/subfolder", "subfolder", nil},
		{"subfolder", "subfolder", nil},
		{"subfolder/", "subfolder", nil},
		{"subfolder//", "subfolder", nil},
		{"subfolder/..", ".", nil},
		{"subfolder/../..", "", ErrRelativePathEscapesRoot},
		{"/..", "", ErrRelativePathEscapesRoot},
		{"..", "", ErrRelativePathEscapesRoot},
		{"../", "", ErrRelativePathEscapesRoot},
		{"", ".", nil},
		{".", ".", nil},
	} {
		const parent = "/parent"
		t.Run(parent+" and "+tc.path, func(t *testing.T) {
			cleaned, err := ValidateRelativePath(parent, tc.path)
			assert.Equal(t, tc.cleaned, cleaned)
			assert.Equal(t, tc.error, err)
		})
	}
}

func TestSanitizeString(t *testing.T) {
	testCases := []struct {
		input  string
		output string
	}{
		{"https://foo_the_user@gitlab.com/foo/bar", "https://[FILTERED]@gitlab.com/foo/bar"},
		{"https://foo_the_user:hUntEr1@gitlab.com/foo/bar", "https://[FILTERED]@gitlab.com/foo/bar"},
		{"proto://user:password@gitlab.com", "proto://[FILTERED]@gitlab.com"},
		{"some message proto://user:password@gitlab.com", "some message proto://[FILTERED]@gitlab.com"},
		{"test", "test"},
		{"ssh://@gitlab.com", "ssh://@gitlab.com"},
	}

	for _, tc := range testCases {
		assert.Equal(t, tc.output, SanitizeString(tc.input))
	}
}
