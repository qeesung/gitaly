//go:build !gitaly_test_sha256

package git2go

import (
	"context"
	"encoding/gob"
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

func (b *Executor) FeatureFlags(ctx context.Context, repo repository.GitRepo) ([]FeatureFlag, error) {
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

	return result.Flags, err
}

var (
	featureA = featureflag.NewFeatureFlag("feature_a", "", "", false)
	featureB = featureflag.NewFeatureFlag("feature_b", "", "", true)
)

func TestExecutor_explicitFeatureFlags(t *testing.T) {
	testhelper.NewFeatureSets(featureA, featureB).Run(t, testExecutorFeatureFlags)
}

func testExecutorFeatureFlags(t *testing.T, ctx context.Context) {
	cfg := testcfg.Build(t)
	testcfg.BuildGitalyGit2Go(t, cfg)

	repoProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	executor := NewExecutor(cfg, gittest.NewCommandFactory(t, cfg), config.NewLocator(cfg))

	flags, err := executor.FeatureFlags(ctx, repo)
	require.NoError(t, err)

	require.Subset(t, flags, []FeatureFlag{
		{
			Name:        "feature_a",
			MetadataKey: "gitaly-feature-feature-a",
			Value:       featureA.IsEnabled(ctx),
		},
		{
			Name:        "feature_b",
			MetadataKey: "gitaly-feature-feature-b",
			Value:       featureB.IsEnabled(ctx),
		},
	}, flags)
}
