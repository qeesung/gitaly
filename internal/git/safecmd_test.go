package git_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestFlagValidation(t *testing.T) {
	for _, tt := range []struct {
		option git.Flag
		valid  bool
	}{
		// valid Flag1 inputs
		{option: git.Flag1{"-k"}, valid: true},
		{option: git.Flag1{"-K"}, valid: true},
		{option: git.Flag1{"--asdf"}, valid: true},
		{option: git.Flag1{"--asdf-qwer"}, valid: true},

		// valid Flag2 inputs
		{option: git.Flag2{"-k", "adsf"}, valid: true},
		{option: git.Flag2{"-k", "--anything"}, valid: true},
		{option: git.Flag2{"-k", ""}, valid: true},

		// valid FlagCombo inputs
		{option: git.FlagCombo{"--asdf=qwerty"}, valid: true},
		{option: git.FlagCombo{"-D=A"}, valid: true},

		// invalid Flag1 inputs
		{option: git.Flag1{"-aa"}},      // too many chars for single dash
		{option: git.Flag1{"-*"}},       // invalid character
		{option: git.Flag1{"a"}},        // missing dash
		{option: git.Flag1{"--a--b"}},   // too many consecutive interior dashes
		{option: git.Flag1{"--asdf-"}},  // trailing dash
		{option: git.Flag1{"--as-df-"}}, // trailing dash
		{option: git.Flag1{"[["}},       // suspicious characters
		{option: git.Flag1{"||"}},       // suspicious characters

		// invalid Flag2 inputs
		{option: git.Flag2{"k", "asdf"}}, // missing dash

		// invalid FlagCombo inputs
		{option: git.FlagCombo{"asdf=qwerty"}}, // missing dash
		{option: git.FlagCombo{"--asdf="}},     // value cannot be empty
		{option: git.FlagCombo{"-D="}},         // value cannot be empty
		{option: git.FlagCombo{"-asdfqwerty"}}, // missing '='
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

func TestSafeCmdInvalidArg(t *testing.T) {
	for _, tt := range []struct {
		globals []git.Flag
		subCmd  git.SubCmd
		errMsg  string
	}{
		{
			globals: []git.Flag{git.Flag1{"-ks"}},
			errMsg:  "flag \"-ks\" failed regex validation",
		},
		{
			subCmd: git.SubCmd{Name: "--meow"},
			errMsg: "invalid sub command name \"--meow\"",
		},
		{
			subCmd: git.SubCmd{
				Name:  "meow",
				Flags: []git.Flag{git.Flag1{"woof"}},
			},
			errMsg: "flag \"woof\" failed regex validation",
		},
		{
			subCmd: git.SubCmd{
				Name: "meow",
				Args: []string{"--tweet"},
			},
			errMsg: "positional arg \"--tweet\" cannot start with dash '-'",
		},
		{
			subCmd: git.SubCmd{
				Name:  "meow",
				Flags: []git.Flag{git.FlagCombo{"--animal="}},
			},
			errMsg: "combination flag \"--animal=\" failed validation",
		},
	} {
		_, err := git.SafeCmd(
			context.Background(),
			&gitalypb.Repository{},
			tt.globals,
			tt.subCmd,
		)
		require.EqualError(t, err, tt.errMsg)
		require.True(t, git.IsInvalidArgErr(err))
	}
}

func TestSafeCmdValid(t *testing.T) {
	testRepo, _, cleanup := testhelper.NewTestRepo(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for _, tt := range []struct {
		globals    []git.Flag
		subCmd     git.SubCmd
		expectArgs []string
	}{
		{
			subCmd:     git.SubCmd{Name: "meow"},
			expectArgs: []string{"meow"},
		},
		{
			globals: []git.Flag{
				git.Flag1{"--aaaa-bbbb"},
			},
			subCmd:     git.SubCmd{Name: "cccc"},
			expectArgs: []string{"--aaaa-bbbb", "cccc"},
		},
		{
			globals: []git.Flag{
				git.Flag1{"-a"},
				git.Flag2{"-b", "c"},
			},
			subCmd: git.SubCmd{
				Name: "d",
				Flags: []git.Flag{
					git.Flag1{"-e"},
					git.Flag2{"-f", "g"},
					git.FlagCombo{"-h=i"},
				},
				Args:        []string{"1", "2"},
				PostSepArgs: []string{"3", "4", "5"},
			},
			expectArgs: []string{"-a", "-b", "c", "d", "-e", "-f", "g", "-h=i", "1", "2", "--", "3", "4", "5"},
		},
	} {
		cmd, err := git.SafeCmd(ctx, testRepo, tt.globals, tt.subCmd)
		require.NoError(t, err)
		// ignore first 3 indeterministic args (executable path and repo args)
		require.Equal(t, tt.expectArgs, cmd.Args()[3:])

		cmd, err = git.SafeStdinCmd(ctx, testRepo, tt.globals, tt.subCmd)
		require.NoError(t, err)
		require.Equal(t, tt.expectArgs, cmd.Args()[3:])

		cmd, err = git.SafeBareCmd(ctx, nil, nil, nil, nil, tt.globals, tt.subCmd)
		require.NoError(t, err)
		// ignore first indeterministic arg (executable path)
		require.Equal(t, tt.expectArgs, cmd.Args()[1:])

		cmd, err = git.SafeCmdWithoutRepo(ctx, tt.globals, tt.subCmd)
		require.NoError(t, err)
		require.Equal(t, tt.expectArgs, cmd.Args()[1:])
	}
}
