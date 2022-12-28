package stats

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestCommitGraphInfoForRepository(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	for _, tc := range []struct {
		desc         string
		setup        func(t *testing.T, repo *localrepo.Repo)
		expectedErr  error
		expectedInfo CommitGraphInfo
	}{
		{
			desc:         "no commit graph filter",
			setup:        func(*testing.T, *localrepo.Repo) {},
			expectedInfo: CommitGraphInfo{},
		},
		{
			desc: "single commit graph without bloom filter",
			setup: func(t *testing.T, repo *localrepo.Repo) {
				repoPath, err := repo.Path()
				require.NoError(t, err)
				git.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable")
			},
			expectedInfo: CommitGraphInfo{
				Exists: true,
			},
		},
		{
			desc: "single commit graph with bloom filter",
			setup: func(t *testing.T, repo *localrepo.Repo) {
				repoPath, err := repo.Path()
				require.NoError(t, err)
				git.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--changed-paths")
			},
			expectedInfo: CommitGraphInfo{
				Exists:          true,
				HasBloomFilters: true,
			},
		},
		{
			desc: "single commit graph with generation numbers",
			setup: func(t *testing.T, repo *localrepo.Repo) {
				repoPath, err := repo.Path()
				require.NoError(t, err)
				git.Exec(t, cfg, "-C", repoPath,
					"-c", "commitGraph.generationVersion=2",
					"commit-graph", "write", "--reachable", "--changed-paths",
				)
			},
			expectedInfo: CommitGraphInfo{
				Exists:            true,
				HasBloomFilters:   true,
				HasGenerationData: true,
			},
		},
		{
			desc: "split commit graph without bloom filter",
			setup: func(t *testing.T, repo *localrepo.Repo) {
				repoPath, err := repo.Path()
				require.NoError(t, err)
				git.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--split")
			},
			expectedInfo: CommitGraphInfo{
				Exists:                 true,
				CommitGraphChainLength: 1,
			},
		},
		{
			desc: "split commit graph with bloom filter",
			setup: func(t *testing.T, repo *localrepo.Repo) {
				repoPath, err := repo.Path()
				require.NoError(t, err)
				git.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--split", "--changed-paths")
			},
			expectedInfo: CommitGraphInfo{
				Exists:                 true,
				CommitGraphChainLength: 1,
				HasBloomFilters:        true,
			},
		},
		{
			desc: "split commit-graph with generation numbers",
			setup: func(t *testing.T, repo *localrepo.Repo) {
				repoPath, err := repo.Path()
				require.NoError(t, err)
				git.Exec(t, cfg, "-C", repoPath,
					"-c", "commitGraph.generationVersion=2",
					"commit-graph", "write", "--reachable", "--split", "--changed-paths",
				)
			},
			expectedInfo: CommitGraphInfo{
				Exists:                 true,
				CommitGraphChainLength: 1,
				HasBloomFilters:        true,
				HasGenerationData:      true,
			},
		},
		{
			desc: "split commit-graph with generation data overflow",
			setup: func(t *testing.T, repo *localrepo.Repo) {
				// We write two commits, where the parent commit is far away in the
				// future and its child commit is in the past. This means we'll have
				// to write a corrected committer date, and because the corrected
				// date is longer than 31 bits we'll have to also write overflow
				// data.
				futureParent := localrepo.WriteTestCommit(t, repo,
					localrepo.WithCommitterDate(time.Date(2077, 1, 1, 0, 0, 0, 0, time.UTC)))

				localrepo.WriteTestCommit(t, repo,
					localrepo.WithBranch("overflow"),
					localrepo.WithParents(futureParent),
					localrepo.WithCommitterDate(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)),
				)

				repoPath, err := repo.Path()
				require.NoError(t, err)

				git.Exec(t, cfg, "-C", repoPath,
					"-c", "commitGraph.generationVersion=2",
					"commit-graph", "write", "--reachable", "--split", "--changed-paths",
				)
			},
			expectedInfo: CommitGraphInfo{
				Exists:                    true,
				CommitGraphChainLength:    1,
				HasBloomFilters:           true,
				HasGenerationData:         true,
				HasGenerationDataOverflow: true,
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, repoPath := git.CreateRepository(t, ctx, cfg, git.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})
			repo := localrepo.NewTestRepo(t, cfg, repoProto)
			localrepo.WriteTestCommit(t, repo, localrepo.WithBranch("main"))
			tc.setup(t, repo)

			info, err := CommitGraphInfoForRepository(repoPath)
			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedInfo, info)
		})
	}
}
