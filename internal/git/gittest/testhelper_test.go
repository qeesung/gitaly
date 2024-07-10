package gittest

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

// setup sets up a test configuration and repository. Ideally we'd use our central test helpers to
// do this, but because of an import cycle we can't.
func setup(tb testing.TB) (config.Cfg, *gitalypb.Repository, string) {
	tb.Helper()

	ctx := testhelper.Context(tb)
	cfg := testcfg.Build(tb)

	repo, repoPath := CreateRepository(tb, ctx, cfg, CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	return cfg, repo, repoPath
}
