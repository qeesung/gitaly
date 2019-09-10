package git_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestOptionValidation(t *testing.T) {
	for _, tt := range []struct {
		option git.Option
		valid  bool
	}{
		// valid Option1 inputs
		{option: git.Option1{"-k"}, valid: true},
		{option: git.Option1{"-K"}, valid: true},
		{option: git.Option1{"--asdf"}, valid: true},
		{option: git.Option1{"--asdf-qwer"}, valid: true},

		// valid Option2 inputs
		{option: git.Option2{"-k", "adsf"}, valid: true},
		{option: git.Option2{"-k", "--anything"}, valid: true},

		// invalid Option1 inputs
		{option: git.Option1{"-aa"}},      // too many chars for single dash
		{option: git.Option1{"-*"}},       // invalid character
		{option: git.Option1{"a"}},        // missing dash
		{option: git.Option1{"--a--b"}},   // too many consecutive interior dashes
		{option: git.Option1{"--asdf-"}},  // trailing dash
		{option: git.Option1{"--as-df-"}}, // trailing dash

		// invalid Option2 inputs
		{option: git.Option2{"-k", ""}}, // missing/empty value
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
			globals: []git.Option{git.Option1{"-ks"}},
			errMsg:  "flag \"-ks\" failed regex validation",
		},
		{
			subCmd: git.SubCmd{Name: "--meow"},
			errMsg: "invalid sub command name \"--meow\"",
		},
		{
			subCmd: git.SubCmd{
				Name:    "meow",
				Options: []git.Option{git.Option1{"woof"}},
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
				Name:        "meow",
				PostSepArgs: []string{"--oink"},
			},
			errMsg: "positional arg \"--oink\" cannot start with dash '-'",
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
			globals: []git.Option{git.Option1{"-a"}},
			subCmd: git.SubCmd{
				Name:        "b",
				Options:     []git.Option{git.Option2{"-c", "d"}},
				Args:        []string{"e"},
				PostSepArgs: []string{"f", "g"},
			},
			expectArgs: []string{"-a", "b", "-c", "d", "e", "--", "f", "g"},
		},
	} {
		cmd, err := git.SafeCmd(ctx, testRepo, tt.globals, tt.subCmd)
		require.NoError(t, err)
		// ignore first 3 indeterministic args (executable and repo args)
		require.Equal(t, tt.expectArgs, cmd.Args()[3:])
	}
}
