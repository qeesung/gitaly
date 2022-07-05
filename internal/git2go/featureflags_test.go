package git2go

import (
	"context"
	"encoding/gob"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func (b *Executor) FeatureFlags(ctx context.Context, repo repository.GitRepo) (featureflag.Raw, error) {
	output, err := b.run(ctx, repo, nil, "feature-flags")
	if err != nil {
		return nil, err
	}

	var result FeatureFlags
	if err := gob.NewDecoder(output).Decode(&result); err != nil {
		return nil, err
	}

	if result.Err != nil {
		return nil, result.Err
	}

	return result.Raw, err
}

var (
	featureA = featureflag.NewFeatureFlag("feature-a", "", "", false)
	featureB = featureflag.NewFeatureFlag("feature-b", "", "", true)
)

func TestFeatureFlagsExecutor_FeatureFlags(t *testing.T) {
	testhelper.NewFeatureSets(featureA, featureB).
		Run(t, testExecutorFeatureFlags)
}

func testExecutorFeatureFlags(t *testing.T, ctx context.Context) {
	cfg := testcfg.Build(t)
	testcfg.BuildGitalyGit2Go(t, cfg)

	repoProto, _ := gittest.InitRepo(t, cfg, cfg.Storages[0])

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	executor := NewExecutor(cfg, gittest.NewCommandFactory(t, cfg), config.NewLocator(cfg))

	raw, err := executor.FeatureFlags(ctx, repo)
	require.NoError(t, err)

	require.Equal(t, strconv.FormatBool(featureA.IsEnabled(ctx)), raw["gitaly-feature-feature-a"])
	require.Equal(t, strconv.FormatBool(featureB.IsEnabled(ctx)), raw["gitaly-feature-feature-b"])
}
