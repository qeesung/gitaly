package gittest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

// SkipIfGitVersionLessThan skips the test if the Git version in use is less than
// expected. The reason is printed out when skipping the test
func SkipIfGitVersionLessThan(tb testing.TB, ctx context.Context, cfg config.Cfg, expected git.Version, reason string) {
	cmdFactory, clean, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(tb))
	require.NoError(tb, err)
	defer clean()

	actual, err := cmdFactory.GitVersion(ctx)
	require.NoError(tb, err)

	if actual.LessThan(expected) {
		tb.Skipf("Unsupported Git version %q, expected minimum %q: %q", actual, expected, reason)
	}
}

// SkipIfGitVersion skips the test if the Git version is the same.
func SkipIfGitVersion(tb testing.TB, ctx context.Context, cfg config.Cfg, expected git.Version, reason string) {
	cmdFactory, clean, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(tb))
	require.NoError(tb, err)
	defer clean()

	actual, err := cmdFactory.GitVersion(ctx)
	require.NoError(tb, err)

	if actual.Equal(expected) {
		tb.Skipf("Unsupported Git version %q, expected %q: %q", actual, expected, reason)
	}
}

// IfSymrefUpdateSupported returns the appropriate value based on if symref updates are
// supported or not in the current git version.
func IfSymrefUpdateSupported[T any](tb testing.TB, ctx context.Context, cfg config.Cfg, yes, no T) T {
	cmdFactory, clean, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(tb))
	require.NoError(tb, err)
	defer clean()

	actual, err := cmdFactory.GitVersion(ctx)
	require.NoError(tb, err)

	if actual.SupportSymrefUpdates() {
		return yes
	}

	return no
}
