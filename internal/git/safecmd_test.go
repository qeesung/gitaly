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
		option git.Option
		valid  bool
	}{
		// valid Flag inputs
		{option: git.Flag{"-k"}, valid: true},
		{option: git.Flag{"-K"}, valid: true},
		{option: git.Flag{"--asdf"}, valid: true},
		{option: git.Flag{"--asdf-qwer"}, valid: true},
		{option: git.Flag{"--asdf=qwerty"}, valid: true},
		{option: git.Flag{"-D=A"}, valid: true},
		{option: git.Flag{"-D="}, valid: true},

		// valid ValueFlag inputs
		{option: git.ValueFlag{"-k", "adsf"}, valid: true},
		{option: git.ValueFlag{"-k", "--anything"}, valid: true},
		{option: git.ValueFlag{"-k", ""}, valid: true},

		// valid SubSubCmd inputs
		{option: git.SubSubCmd{"meow"}, valid: true},

		// valid FlagCombo inputs

		// invalid Flag inputs
		{option: git.Flag{"-*"}},          // invalid character
		{option: git.Flag{"a"}},           // missing dash
		{option: git.Flag{"[["}},          // suspicious characters
		{option: git.Flag{"||"}},          // suspicious characters
		{option: git.Flag{"asdf=qwerty"}}, // missing dash

		// invalid ValueFlag inputs
		{option: git.ValueFlag{"k", "asdf"}}, // missing dash

		// invalid SubSubCmd inputs
		{option: git.SubSubCmd{"--meow"}}, // cannot start with dash
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
		globals []git.Option
		subCmd  git.SubCmd
		errMsg  string
	}{
		{
			subCmd: git.SubCmd{Name: "--meow"},
			errMsg: "invalid sub command name \"--meow\"",
		},
		{
			subCmd: git.SubCmd{
				Name:  "meow",
				Flags: []git.Option{git.Flag{"woof"}},
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
				Flags: []git.Option{git.SubSubCmd{"-invalid"}},
			},
			errMsg: "positional arg \"-invalid\" cannot start with dash '-'",
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
		globals    []git.Option
		subCmd     git.SubCmd
		expectArgs []string
	}{
		{
			subCmd:     git.SubCmd{Name: "meow"},
			expectArgs: []string{"meow"},
		},
		{
			globals: []git.Option{
				git.Flag{"--aaaa-bbbb"},
			},
			subCmd:     git.SubCmd{Name: "cccc"},
			expectArgs: []string{"--aaaa-bbbb", "cccc"},
		},
		{
			subCmd: git.SubCmd{
				Name:        "meow",
				Args:        []string{""},
				PostSepArgs: []string{"-woof", ""},
			},
			expectArgs: []string{"meow", "", "--", "-woof", ""},
		},
		{
			globals: []git.Option{
				git.Flag{"-a"},
				git.ValueFlag{"-b", "c"},
			},
			subCmd: git.SubCmd{
				Name: "d",
				Flags: []git.Option{
					git.Flag{"-e"},
					git.ValueFlag{"-f", "g"},
					git.Flag{"-h=i"},
				},
				Args:        []string{"1", "2"},
				PostSepArgs: []string{"3", "4", "5"},
			},
			expectArgs: []string{"-a", "-b", "c", "d", "-e", "-f", "g", "-h=i", "1", "2", "--", "3", "4", "5"},
		},
		{
			subCmd: git.SubCmd{
				Name: "noun",
				Flags: []git.Option{
					git.SubSubCmd{"verb"},
					git.OutputToStdout,
					git.Flag{"--adjective"},
				},
			},
			expectArgs: []string{"noun", "verb", "-", "--adjective"},
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
