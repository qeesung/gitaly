package manager_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	housekeepingmgr "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/manager"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
)

func TestPruneIfNeeded(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)

	for _, tc := range []struct {
		desc               string
		isPool             bool
		looseObjects       []string
		expectedLogEntries map[string]string
		looseObjectTime    *time.Time
	}{
		{
			desc:               "no objects",
			looseObjects:       nil,
			expectedLogEntries: map[string]string{},
		},
		{
			desc: "object not in 17 shard",
			looseObjects: []string{
				filepath.Join("ab/12345"),
			},
			expectedLogEntries: map[string]string{
				"packed_objects_geometric": "success",
				"written_bitmap":           "success",
				"written_multi_pack_index": "success",
			},
		},
		{
			desc: "object in 17 shard",
			looseObjects: []string{
				filepath.Join("17/12345"),
			},
			expectedLogEntries: map[string]string{
				"packed_objects_geometric": "success",
				"written_bitmap":           "success",
				"written_multi_pack_index": "success",
			},
		},
		{
			desc: "objects in different shards",
			looseObjects: []string{
				filepath.Join("ab/12345"),
				filepath.Join("cd/12345"),
				filepath.Join("12/12345"),
				filepath.Join("17/12345"),
			},
			expectedLogEntries: map[string]string{
				"packed_objects_geometric": "success",
				"written_bitmap":           "success",
				"written_multi_pack_index": "success",
			},
		},
		{
			desc:   "exceeding boundary on pool",
			isPool: true,
			looseObjects: func() []string {
				// we need to hard-code since this is an external test and doesn't have
				// access to housekeeping.looseObjectLimit
				looseObjects := make([]string, 1024+1)

				for i := range looseObjects {
					looseObjects[i] = filepath.Join(fmt.Sprintf("17/%d", i))
				}

				return looseObjects
			}(),
			expectedLogEntries: map[string]string{
				"packed_objects_geometric": "success",
				"written_bitmap":           "success",
				"written_multi_pack_index": "success",
			},
		},
		{
			desc: "on boundary shouldn't prune",
			looseObjects: func() []string {
				// we need to hard-code since this is an external test and doesn't have
				// access to housekeeping.looseObjectLimit
				looseObjects := make([]string, 1024)

				for i := range looseObjects {
					looseObjects[i] = filepath.Join(fmt.Sprintf("17/%d", i))
				}

				return looseObjects
			}(),
			looseObjectTime: func() *time.Time {
				t := time.Now().Add(stats.StaleObjectsGracePeriod).Add(-1 * time.Minute)
				return &t
			}(),
			expectedLogEntries: map[string]string{
				"packed_objects_geometric": "success",
				"written_bitmap":           "success",
				"written_multi_pack_index": "success",
			},
		},
		{
			desc: "exceeding boundary should prune",
			looseObjects: func() []string {
				// we need to hard-code since this is an external test and doesn't have
				// access to housekeeping.looseObjectLimit
				looseObjects := make([]string, 1024+1)

				for i := range looseObjects {
					looseObjects[i] = filepath.Join(fmt.Sprintf("17/%d", i))
				}

				return looseObjects
			}(),
			looseObjectTime: func() *time.Time {
				t := time.Now().Add(stats.StaleObjectsGracePeriod).Add(-1 * time.Minute)
				return &t
			}(),
			expectedLogEntries: map[string]string{
				"packed_objects_geometric": "success",
				"pruned_objects":           "success",
				"written_bitmap":           "success",
				"written_multi_pack_index": "success",
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			createRepoCfg := gittest.CreateRepositoryConfig{}
			if tc.isPool {
				createRepoCfg.RelativePath = gittest.NewObjectPoolName(t)
			}

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, createRepoCfg)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			for _, looseObjectPath := range tc.looseObjects {
				looseObjectPath := filepath.Join(repoPath, "objects", looseObjectPath)
				require.NoError(t, os.MkdirAll(filepath.Dir(looseObjectPath), perm.SharedDir))

				looseObjectFile, err := os.Create(looseObjectPath)
				require.NoError(t, err)
				testhelper.MustClose(t, looseObjectFile)

				if tc.looseObjectTime != nil {
					err := os.Chtimes(looseObjectPath, *tc.looseObjectTime, *tc.looseObjectTime)
					require.NoError(t, err)
				}
			}

			logger := testhelper.NewLogger(t)
			hook := testhelper.AddLoggerHook(logger)

			// In reftables, we always write the commit graph.
			if testhelper.IsReftableEnabled() {
				tc.expectedLogEntries["written_commit_graph_full"] = "success"
			}

			require.NoError(t, housekeepingmgr.New(cfg.Prometheus, logger, nil, nil).OptimizeRepository(ctx, repo))
			require.Equal(t, tc.expectedLogEntries, hook.LastEntry().Data["optimizations"])
		})
	}
}
