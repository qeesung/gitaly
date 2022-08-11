//go:build !gitaly_test_sha256

package stats

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestIsMissingBloomFilters(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		desc   string
		args   []string
		result bool
	}{
		{
			desc:   "no commit graph filter",
			args:   nil,
			result: true,
		},
		{
			desc:   "commit graph without Bloom filter",
			args:   []string{"commit-graph", "write", "--reachable", "--split"},
			result: true,
		},
		{
			desc:   "commit graph with Bloom filter",
			args:   []string{"commit-graph", "write", "--reachable", "--split", "--changed-paths"},
			result: false,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)
			cfg := testcfg.Build(t)

			_, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
				Seed:                   gittest.SeedGitLabTest,
			})

			if len(tc.args) > 0 {
				gittest.Exec(t, cfg, append([]string{"-C", repoPath}, tc.args...)...)
			}

			result, err := IsMissingBloomFilters(repoPath)
			require.NoError(t, err)
			require.Equal(t, tc.result, result)
		})
	}
}
