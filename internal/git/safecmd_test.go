package git_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
)

func TestOptionValidation(t *testing.T) {
	for _, tt := range []struct {
		option git.Option
		valid  bool
	}{
		// valid options
		{
			option: git.Option1{"-k"},
			valid:  true,
		},
		{
			option: git.Option1{"-K"},
			valid:  true,
		},
		// invalid options
		{option: git.Option1{"-aa"}},     // too many chars for single dash
		{option: git.Option1{"-*"}},      // invalid character
		{option: git.Option1{"a"}},       // missing dash
		{option: git.Option1{"--a--b"}},  // too many consecutive interior dashes
		{option: git.Option1{"--asdf-"}}, // trailing dash
	} {
		args, err := tt.option.ValidateArgs()
		if tt.valid {
			require.NoError(t, err)
		} else {
			require.Error(t, err,
				"expected error, but args %v passed validation", args)
			require.True(t, git.IsInvalidArgErr(err))
		}
	}
}
