//go:build !gitaly_test_sha256

package maintenance

import (
	"context"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	repo "gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/tick"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

type mockOptimizer struct {
	t      testing.TB
	actual []repo.GitRepo
	cfg    config.Cfg
}

func (mo *mockOptimizer) OptimizeRepository(ctx context.Context, repository repo.GitRepo) error {
	mo.actual = append(mo.actual, repository)
	l := config.NewLocator(mo.cfg)
	gitCmdFactory := gittest.NewCommandFactory(mo.t, mo.cfg)
	catfileCache := catfile.NewCache(mo.cfg)
	mo.t.Cleanup(catfileCache.Stop)
	txManager := transaction.NewManager(mo.cfg, backchannel.NewRegistry())
	housekeepingManager := housekeeping.NewManager(mo.cfg.Prometheus, txManager)

	return housekeepingManager.OptimizeRepository(ctx, localrepo.New(l, gitCmdFactory, catfileCache, repository))
}

func TestOptimizeReposRandomly(t *testing.T) {
	cfgBuilder := testcfg.NewGitalyCfgBuilder(testcfg.WithStorages("0", "1", "2"))
	cfg := cfgBuilder.Build(t)

	for _, storage := range cfg.Storages {
		gittest.Exec(t, cfg, "init", "--bare", filepath.Join(storage.Path, "a"))
		gittest.Exec(t, cfg, "init", "--bare", filepath.Join(storage.Path, "b"))
	}

	cfg.Storages = append(cfg.Storages, config.Storage{
		Name: "duplicate",
		Path: cfg.Storages[0].Path,
	})
	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc     string
		storages []string
		expected []*gitalypb.Repository
	}{
		{
			desc:     "two storages",
			storages: []string{"0", "1"},
			expected: []*gitalypb.Repository{
				{RelativePath: "a", StorageName: "0"},
				{RelativePath: "a", StorageName: "1"},
				{RelativePath: "b", StorageName: "0"},
				{RelativePath: "b", StorageName: "1"},
			},
		},
		{
			desc:     "duplicate storages",
			storages: []string{"0", "1", "duplicate"},
			expected: []*gitalypb.Repository{
				{RelativePath: "a", StorageName: "0"},
				{RelativePath: "a", StorageName: "1"},
				{RelativePath: "b", StorageName: "0"},
				{RelativePath: "b", StorageName: "1"},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tickerDone := false
			tickerCount := 0

			ticker := tick.NewManualTicker()
			ticker.ResetFunc = func() {
				tickerCount++
				ticker.Tick()
			}
			ticker.StopFunc = func() {
				tickerDone = true
			}

			mo := &mockOptimizer{
				t:   t,
				cfg: cfg,
			}
			walker := OptimizeReposRandomly(cfg.Storages, mo, ticker, rand.New(rand.NewSource(1)))

			require.NoError(t, walker(ctx, testhelper.NewDiscardingLogEntry(t), tc.storages))
			require.ElementsMatch(t, tc.expected, mo.actual)
			require.True(t, tickerDone)
			// We expect one more tick than optimized repositories because of the
			// initial tick up front to re-start the timer.
			require.Equal(t, len(tc.expected)+1, tickerCount)
		})
	}
}
