//go:build !gitaly_test_sha256

package commit

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

func TestLanguages(t *testing.T) {
	testhelper.NewFeatureSets(featureflag.GoLanguageStats).
		Run(t, testLanguagesFeatured)
}

func testLanguagesFeatured(t *testing.T, ctx context.Context) {
	t.Parallel()
	cfg := testcfg.Build(t, testcfg.WithRealLinguist())

	cfg.SocketPath = startTestServices(t, cfg)

	repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	client := newCommitServiceClient(t, cfg.SocketPath)

	request := &gitalypb.CommitLanguagesRequest{
		Repository: repo,
		Revision:   []byte("cb19058ecc02d01f8e4290b7e79cafd16a8839b6"),
	}

	resp, err := client.CommitLanguages(ctx, request)
	require.NoError(t, err)

	expectedLanguages := []*gitalypb.CommitLanguagesResponse_Language{
		{Name: "Ruby", Share: 65.28394, Color: "#701516", Bytes: 2943},
		{Name: "JavaScript", Share: 22.493345, Color: "#f1e05a", Bytes: 1014},
		{Name: "HTML", Share: 7.741792, Color: "#e34c26", Bytes: 349},
		{Name: "CoffeeScript", Share: 2.373558, Color: "#244776", Bytes: 107},
		{Name: "Modula-2", Share: 2.1073646, Color: "#10253f", Bytes: 95},
	}

	testhelper.ProtoEqual(t, expectedLanguages, resp.Languages)
}

func TestFileCountIsZeroWhenFeatureIsDisabled(t *testing.T) {
	testhelper.NewFeatureSets(featureflag.GoLanguageStats).
		Run(t, testFileCountIsZeroWhenFeatureIsDisabled)
}

func testFileCountIsZeroWhenFeatureIsDisabled(t *testing.T, ctx context.Context) {
	t.Parallel()

	_, repo, _, client := setupCommitServiceWithRepo(ctx, t)

	request := &gitalypb.CommitLanguagesRequest{
		Repository: repo,
		Revision:   []byte("cb19058ecc02d01f8e4290b7e79cafd16a8839b6"),
	}

	resp, err := client.CommitLanguages(ctx, request)
	require.NoError(t, err)

	require.NotZero(t, len(resp.Languages), "number of languages in response")

	for i := range resp.Languages {
		actualLanguage := resp.Languages[i]
		require.Equal(t, uint32(0), actualLanguage.FileCount)
	}
}

func TestLanguagesEmptyRevision(t *testing.T) {
	testhelper.NewFeatureSets(featureflag.GoLanguageStats).
		Run(t, testLanguagesEmptyRevisionFeatured)
}

func testLanguagesEmptyRevisionFeatured(t *testing.T, ctx context.Context) {
	t.Parallel()

	_, repo, _, client := setupCommitServiceWithRepo(ctx, t)

	request := &gitalypb.CommitLanguagesRequest{
		Repository: repo,
	}
	resp, err := client.CommitLanguages(ctx, request)
	require.NoError(t, err)

	require.NotZero(t, len(resp.Languages), "number of languages in response")

	foundRuby := false
	for _, l := range resp.Languages {
		if l.Name == "Ruby" {
			foundRuby = true
		}
	}
	require.True(t, foundRuby, "expected to find Ruby as a language on HEAD")
}

func TestInvalidCommitLanguagesRequestRevision(t *testing.T) {
	testhelper.NewFeatureSets(featureflag.GoLanguageStats).
		Run(t, testInvalidCommitLanguagesRequestRevisionFeatured)
}

func testInvalidCommitLanguagesRequestRevisionFeatured(t *testing.T, ctx context.Context) {
	t.Parallel()

	_, repo, _, client := setupCommitServiceWithRepo(ctx, t)

	_, err := client.CommitLanguages(ctx, &gitalypb.CommitLanguagesRequest{
		Repository: repo,
		Revision:   []byte("--output=/meow"),
	})
	testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
}

func TestAmbiguousRefCommitLanguagesRequestRevision(t *testing.T) {
	testhelper.NewFeatureSets(featureflag.GoLanguageStats).
		Run(t, testAmbiguousRefCommitLanguagesRequestRevisionFeatured)
}

func testAmbiguousRefCommitLanguagesRequestRevisionFeatured(t *testing.T, ctx context.Context) {
	t.Parallel()

	_, repo, _, client := setupCommitServiceWithRepo(ctx, t)

	// gitlab-test repo has both a branch and a tag named 'v1.1.0'
	// b83d6e391c22777fca1ed3012fce84f633d7fed0 refs/heads/v1.1.0
	// 8a2a6eb295bb170b34c24c76c49ed0e9b2eaf34b refs/tags/v1.1.0
	_, err := client.CommitLanguages(ctx, &gitalypb.CommitLanguagesRequest{
		Repository: repo,
		Revision:   []byte("v1.1.0"),
	})
	require.NoError(t, err)
}
