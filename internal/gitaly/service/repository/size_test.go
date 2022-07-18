//go:build !gitaly_test_sha256

package repository

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/quarantine"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
)

// We assume that the combined size of the Git objects in the test
// repository, even in optimally packed state, is greater than this.
const testRepoMinSizeKB = 10000

func TestRepositorySize_SuccessfulRequest(t *testing.T) {
	t.Parallel()

	featureSet := testhelper.NewFeatureSets(featureflag.RevlistForRepoSize)

	featureSet.Run(t, testSuccessfulRepositorySizeRequest)
	featureSet.Run(t, testSuccessfulRepositorySizeRequestPoolMember)
}

func testSuccessfulRepositorySizeRequestPoolMember(t *testing.T, ctx context.Context) {
	cfg := testcfg.Build(t)

	repoClient, serverSocketPath := runRepositoryService(t, cfg, nil)
	cfg.SocketPath = serverSocketPath

	objectPoolClient := newObjectPoolClient(t, cfg, serverSocketPath)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	sizeRequest := &gitalypb.RepositorySizeRequest{Repository: repo}
	response, err := repoClient.RepositorySize(ctx, sizeRequest)
	require.NoError(t, err)

	sizeBeforePool := response.GetSize()

	storage := cfg.Storages[0]
	relativePath := gittest.NewObjectPoolName(t)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	// create an object pool
	gittest.InitRepoDir(t, storage.Path, relativePath)
	pool, err := objectpool.NewObjectPool(
		config.NewLocator(cfg),
		gittest.NewCommandFactory(t, cfg),
		catfileCache,
		nil,
		nil,
		storage.Name,
		relativePath,
	)
	require.NoError(t, err)

	_, err = objectPoolClient.CreateObjectPool(
		ctx,
		&gitalypb.CreateObjectPoolRequest{
			ObjectPool: pool.ToProto(),
			Origin:     repo,
		})
	require.NoError(t, err)

	_, err = objectPoolClient.LinkRepositoryToObjectPool(
		ctx,
		&gitalypb.LinkRepositoryToObjectPoolRequest{
			Repository: repo,
			ObjectPool: pool.ToProto(),
		},
	)
	require.NoError(t, err)

	gittest.Exec(t, cfg, "-C", repoPath, "gc")

	response, err = repoClient.RepositorySize(ctx, sizeRequest)
	require.NoError(t, err)

	if featureflag.RevlistForRepoSize.IsEnabled(ctx) {
		assert.Equal(t, int64(0), response.GetSize())
	} else {
		assert.Less(t, response.GetSize(), sizeBeforePool)
	}
}

func testSuccessfulRepositorySizeRequest(t *testing.T, ctx context.Context) {
	cfg, repo, repoPath, client := setupRepositoryService(ctx, t)

	request := &gitalypb.RepositorySizeRequest{Repository: repo}
	response, err := client.RepositorySize(ctx, request)
	require.NoError(t, err)

	require.True(t,
		response.Size > testRepoMinSizeKB,
		"repository size %d should be at least %d", response.Size, testRepoMinSizeKB,
	)

	blob := bytes.Repeat([]byte("a"), 1000)
	blobOID := gittest.WriteBlob(t, cfg, repoPath, blob)
	treeOID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{
			OID:  blobOID,
			Mode: "100644",
			Path: "1kbblob",
		},
	})
	commitOID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTree(treeOID))

	gittest.WriteRef(t, cfg, repoPath, git.ReferenceName("refs/keep-around/keep1"), commitOID)
	gittest.WriteRef(t, cfg, repoPath, git.ReferenceName("refs/merge-requests/1123"), commitOID)
	gittest.WriteRef(t, cfg, repoPath, git.ReferenceName("refs/pipelines/pipeline2"), commitOID)
	gittest.WriteRef(t, cfg, repoPath, git.ReferenceName("refs/environments/env1"), commitOID)

	responseAfterRefs, err := client.RepositorySize(ctx, request)
	require.NoError(t, err)

	if featureflag.RevlistForRepoSize.IsEnabled(ctx) {
		assert.Equal(
			t,
			response.Size,
			responseAfterRefs.Size,
			"excluded refs do not contribute to the repository size",
		)
	} else {
		assert.Less(t, response.Size, responseAfterRefs.Size)
	}
}

func TestRepositorySize_FailedRequest(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.RevlistForRepoSize).
		Run(t, testFailedRepositorySizeRequest)
}

func testFailedRepositorySizeRequest(t *testing.T, ctx context.Context) {
	_, client := setupRepositoryServiceWithoutRepo(t)

	testCases := []struct {
		description string
		repo        *gitalypb.Repository
	}{
		{
			description: "Invalid repo",
			repo:        &gitalypb.Repository{StorageName: "fake", RelativePath: "path"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.description, func(t *testing.T) {
			request := &gitalypb.RepositorySizeRequest{
				Repository: testCase.repo,
			}
			_, err := client.RepositorySize(ctx, request)
			testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestRepositorySize_SuccessfulGetObjectDirectorySizeRequest(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.RevlistForRepoSize).
		Run(t, testSuccessfulGetObjectDirectorySizeRequest)
}

func testSuccessfulGetObjectDirectorySizeRequest(t *testing.T, ctx context.Context) {
	_, repo, _, client := setupRepositoryService(ctx, t)
	repo.GitObjectDirectory = "objects/"

	request := &gitalypb.GetObjectDirectorySizeRequest{Repository: repo}
	response, err := client.GetObjectDirectorySize(ctx, request)
	require.NoError(t, err)

	require.True(t,
		response.Size > testRepoMinSizeKB,
		"repository size %d should be at least %d", response.Size, testRepoMinSizeKB,
	)
}

func TestRepositorySize_GetObjectDirectorySize_quarantine(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.RevlistForRepoSize).
		Run(t, testGetObjectDirectorySizeQuarantine)
}

func testGetObjectDirectorySizeQuarantine(t *testing.T, ctx context.Context) {
	cfg, client := setupRepositoryServiceWithoutRepo(t)
	locator := config.NewLocator(cfg)

	t.Run("quarantined repo", func(t *testing.T) {
		repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			Seed: gittest.SeedGitLabTest,
		})

		quarantine, err := quarantine.New(ctx, gittest.RewrittenRepository(ctx, t, cfg, repo), locator)
		require.NoError(t, err)

		// quarantine.New in Gitaly would receive an already rewritten repository. Gitaly would then calculate
		// the quarantine directories based on the rewritten relative path. That quarantine would then be looped
		// through Rails, which would then send a request with the quarantine object directories set based on the
		// rewritten relative path but with the original relative path of the repository. Since we're using the production
		// helpers here, we need to manually substitute the rewritten relative path with the original one when sending
		// it back through the API.
		quarantinedRepo := quarantine.QuarantinedRepo()
		quarantinedRepo.RelativePath = repo.RelativePath

		response, err := client.GetObjectDirectorySize(ctx, &gitalypb.GetObjectDirectorySizeRequest{
			Repository: quarantinedRepo,
		})
		require.NoError(t, err)
		require.NotNil(t, response)

		// Due to platform incompatibilities we can't assert the exact size of bytes: on
		// some, the directory entry is counted, on some it's not.
		require.Less(t, response.Size, int64(10))
	})

	t.Run("quarantined repo with different relative path", func(t *testing.T) {
		repo1, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			Seed: gittest.SeedGitLabTest,
		})
		quarantine1, err := quarantine.New(ctx, gittest.RewrittenRepository(ctx, t, cfg, repo1), locator)
		require.NoError(t, err)

		repo2, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
			Seed: gittest.SeedGitLabTest,
		})
		quarantine2, err := quarantine.New(ctx, gittest.RewrittenRepository(ctx, t, cfg, repo2), locator)
		require.NoError(t, err)

		// We swap out the the object directories of both quarantines. So while both are
		// valid, we still expect that this RPC call fails because we detect that the
		// swapped-in quarantine directory does not belong to our repository.
		repo := proto.Clone(quarantine1.QuarantinedRepo()).(*gitalypb.Repository)
		repo.GitObjectDirectory = quarantine2.QuarantinedRepo().GetGitObjectDirectory()
		// quarantine.New in Gitaly would receive an already rewritten repository. Gitaly would then calculate
		// the quarantine directories based on the rewritten relative path. That quarantine would then be looped
		// through Rails, which would then send a request with the quarantine object directories set based on the
		// rewritten relative path but with the original relative path of the repository. Since we're using the production
		// helpers here, we need to manually substitute the rewritten relative path with the original one when sending
		// it back through the API.
		repo.RelativePath = repo1.RelativePath

		response, err := client.GetObjectDirectorySize(ctx, &gitalypb.GetObjectDirectorySizeRequest{
			Repository: repo,
		})
		require.Error(t, err, "rpc error: code = InvalidArgument desc = GetObjectDirectoryPath: relative path escapes root directory")
		require.Nil(t, response)
	})
}
