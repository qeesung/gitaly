package gittest

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
)

// NewCommandFactory creates a new Git command factory.
func NewCommandFactory(tb testing.TB, cfg config.Cfg, opts ...git.ExecCommandFactoryOption) git.CommandFactory {
	tb.Helper()
	factory, cleanup, err := git.NewExecCommandFactory(cfg, opts...)
	require.NoError(tb, err)
	tb.Cleanup(cleanup)
	return factory
}

// GitSupportsStatusFlushing returns whether or not the current version of Git
// supports status flushing.
//nolint: revive
func GitSupportsStatusFlushing(t *testing.T, ctx context.Context, cfg config.Cfg) bool {
	version, err := NewCommandFactory(t, cfg).GitVersion(ctx)
	require.NoError(t, err)
	return version.FlushesUpdaterefStatus()
}
