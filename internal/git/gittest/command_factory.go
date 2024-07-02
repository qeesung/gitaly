package gittest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

// NewCommandFactory creates a new Git command factory.
func NewCommandFactory(tb testing.TB, cfg config.Cfg, opts ...git.ExecCommandFactoryOption) *git.ExecCommandFactory {
	tb.Helper()
	factory, cleanup, err := git.NewExecCommandFactory(cfg, testhelper.SharedLogger(tb), opts...)
	if err != nil {
		if strings.Contains(err.Error(), "no such file or directory") {
			require.FailNowf(tb, "Gitaly tests execute against the bundled Git environment. Have you run `make build-bundled-git`? Error: %s", err.Error())
		}
	}
	require.NoError(tb, err)
	tb.Cleanup(cleanup)
	return factory
}
