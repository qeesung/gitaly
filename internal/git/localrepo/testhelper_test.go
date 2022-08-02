package localrepo

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

type setupRepoConfig struct {
	// disableHooks will disable the use of hooks.
	disableHooks bool
}

type setupRepoOption func(*setupRepoConfig)

func withDisabledHooks() setupRepoOption {
	return func(cfg *setupRepoConfig) {
		cfg.disableHooks = true
	}
}

func setupRepo(t *testing.T, opts ...setupRepoOption) (config.Cfg, *Repo, string) {
	t.Helper()

	var setupRepoCfg setupRepoConfig
	for _, opt := range opts {
		opt(&setupRepoCfg)
	}

	cfg := testcfg.Build(t)

	var commandFactoryOpts []git.ExecCommandFactoryOption
	if setupRepoCfg.disableHooks {
		commandFactoryOpts = append(commandFactoryOpts, git.WithSkipHooks())
	}

	repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

	gitCmdFactory := gittest.NewCommandFactory(t, cfg, commandFactoryOpts...)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)
	return cfg, New(config.NewLocator(cfg), gitCmdFactory, catfileCache, repoProto), repoPath
}
