package housekeeping

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestWriteCommitGraphConfigForRepository(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	for _, tc := range []struct {
		desc           string
		setup          func(t *testing.T, repoPath string)
		expectedErr    error
		expectedConfig config.WriteCommitGraphConfig
	}{
		{
			desc: "without commit-graph",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
			},
			expectedConfig: config.WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		},
		{
			desc: "monolithic commit-graph without bloom filter",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable")
			},
			expectedConfig: config.WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		},
		{
			desc: "monolithic commit-graph with bloom filter",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--changed-paths")
			},
			expectedConfig: config.WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		},
		{
			desc: "split commit-graph without bloom filter",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--split")
			},
			expectedConfig: config.WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		},
		{
			desc: "split commit-graph with bloom filter without generation data",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath,
					"-c", "commitGraph.generationVersion=1",
					"commit-graph", "write", "--reachable", "--split", "--changed-paths",
				)
			},
			expectedConfig: config.WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		},
		{
			desc: "split commit-graph with bloom filter with generation data",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath,
					"-c", "commitGraph.generationVersion=2",
					"commit-graph", "write", "--reachable", "--split", "--changed-paths",
				)
			},
			expectedConfig: config.WriteCommitGraphConfig{
				ReplaceChain: false,
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			tc.setup(t, repoPath)

			config, err := WriteCommitGraphConfigForRepository(ctx, repo)
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedConfig, config)
		})
	}
}
