package objectpool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestCompleteForkCreationFlow(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, sourceRepository, _, _, objectPoolClient := setup(t, ctx, testserver.WithDisablePraefect())

	repositoryClient := gitalypb.NewRepositoryServiceClient(
		objectPoolClient.(clientWithConn).conn,
	)

	forkRepository := &gitalypb.Repository{
		StorageName:  sourceRepository.StorageName,
		RelativePath: gittest.NewRepositoryName(t),
	}

	// Inject the Gitaly's address information in the context. CreateFork uses this to
	// fetch from the source repository.
	ctx = testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))
	// Build GitalySSH as CreateFork uses to perform the fetch.
	testcfg.BuildGitalySSH(t, cfg)

	// Rails sends a RepositoryExists request before creating the fork as well.
	repositoryExistsResponse, err := repositoryClient.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
		Repository: forkRepository,
	})
	require.NoError(t, err)
	testhelper.ProtoEqual(t, &gitalypb.RepositoryExistsResponse{
		Exists: false,
	}, repositoryExistsResponse)

	createForkResponse, err := repositoryClient.CreateFork(ctx, &gitalypb.CreateForkRequest{
		Repository:       forkRepository,
		SourceRepository: sourceRepository,
	})
	require.NoError(t, err)
	testhelper.ProtoEqual(t, &gitalypb.CreateForkResponse{}, createForkResponse)

	// Create an object pool from the source repository.
	objectPool, _, _ := createObjectPool(t, ctx, cfg, sourceRepository)

	// Link the source repository itself to the object pool.
	linkSourceToObjectPoolResponse, err := objectPoolClient.LinkRepositoryToObjectPool(ctx, &gitalypb.LinkRepositoryToObjectPoolRequest{
		ObjectPool: objectPool,
		Repository: sourceRepository,
	})
	require.NoError(t, err)
	testhelper.ProtoEqual(t, &gitalypb.LinkRepositoryToObjectPoolResponse{}, linkSourceToObjectPoolResponse)

	// Link the fork to the object pool.
	linkForkToObjectPoolResponse, err := objectPoolClient.LinkRepositoryToObjectPool(ctx, &gitalypb.LinkRepositoryToObjectPoolRequest{
		ObjectPool: objectPool,
		Repository: forkRepository,
	})
	require.NoError(t, err)
	testhelper.ProtoEqual(t, &gitalypb.LinkRepositoryToObjectPoolResponse{}, linkForkToObjectPoolResponse)

	// Ensure the source is linked to the pool now.
	getSourceObjectPoolResponse, err := objectPoolClient.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{
		Repository: sourceRepository,
	})
	require.NoError(t, err)
	testhelper.ProtoEqual(t, &gitalypb.GetObjectPoolResponse{
		ObjectPool: objectPool,
	}, getSourceObjectPoolResponse)

	// Ensure the fork is linked to the pool now.
	getForkObjectPoolResponse, err := objectPoolClient.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{
		Repository: forkRepository,
	})
	require.NoError(t, err)
	testhelper.ProtoEqual(t, &gitalypb.GetObjectPoolResponse{
		ObjectPool: objectPool,
	}, getForkObjectPoolResponse)
}

func TestLink(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, _, _, client := setup(t, ctx, testserver.WithDisablePraefect())

	localRepo := localrepo.NewTestRepo(t, cfg, repo)
	poolProto, _, poolPath := createObjectPool(t, ctx, cfg, repo)

	// Mock object in the pool, which should be available to the pool members
	// after linking
	poolCommitID := gittest.WriteCommit(t, cfg, poolPath,
		gittest.WithBranch("pool-test-branch"))

	for _, tc := range []struct {
		desc        string
		request     *gitalypb.LinkRepositoryToObjectPoolRequest
		expectedErr error
	}{
		{
			desc: "unset repository",
			request: &gitalypb.LinkRepositoryToObjectPoolRequest{
				Repository: nil,
				ObjectPool: poolProto,
			},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "unset object pool",
			request: &gitalypb.LinkRepositoryToObjectPoolRequest{
				Repository: repo,
				ObjectPool: nil,
			},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "successful",
			request: &gitalypb.LinkRepositoryToObjectPoolRequest{
				Repository: repo,
				ObjectPool: poolProto,
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.LinkRepositoryToObjectPool(ctx, tc.request)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
			if tc.expectedErr != nil {
				return
			}

			commit, err := localRepo.ReadCommit(ctx, git.Revision(poolCommitID))
			require.NoError(t, err)
			require.NotNil(t, commit)
			require.Equal(t, poolCommitID.String(), commit.Id)
		})
	}
}

func TestLink_idempotent(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repoProto, _, _, client := setup(t, ctx)

	poolProto, _, _ := createObjectPool(t, ctx, cfg, repoProto)

	request := &gitalypb.LinkRepositoryToObjectPoolRequest{
		Repository: repoProto,
		ObjectPool: poolProto,
	}

	_, err := client.LinkRepositoryToObjectPool(ctx, request)
	require.NoError(t, err)

	_, err = client.LinkRepositoryToObjectPool(ctx, request)
	require.NoError(t, err)
}

func TestLink_noClobber(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repoProto, repoPath, _, client := setup(t, ctx)
	poolProto, _, _ := createObjectPool(t, ctx, cfg, repoProto)

	alternatesFile := filepath.Join(repoPath, "objects/info/alternates")
	require.NoFileExists(t, alternatesFile)

	contentBefore := "mock/objects\n"
	require.NoError(t, os.WriteFile(alternatesFile, []byte(contentBefore), perm.PrivateWriteOnceFile))

	request := &gitalypb.LinkRepositoryToObjectPoolRequest{
		Repository: repoProto,
		ObjectPool: poolProto,
	}

	_, err := client.LinkRepositoryToObjectPool(ctx, request)
	testhelper.RequireGrpcError(t, structerr.NewInternal("unexpected alternates content: %q", "mock/objects"), err)

	contentAfter := testhelper.MustReadFile(t, alternatesFile)
	require.Equal(t, contentBefore, string(contentAfter), "contents of existing alternates file should not have changed")
}

func TestLink_noPool(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	cfg, repo, _, _, client := setup(t, ctx)

	poolRelativePath := gittest.NewObjectPoolName(t)

	_, err := client.LinkRepositoryToObjectPool(ctx, &gitalypb.LinkRepositoryToObjectPoolRequest{
		Repository: repo,
		ObjectPool: &gitalypb.ObjectPool{
			Repository: &gitalypb.Repository{
				StorageName:  cfg.Storages[0].Name,
				RelativePath: poolRelativePath,
			},
		},
	})

	testhelper.RequireGrpcError(t, testhelper.GitalyOrPraefect(
		testhelper.WithOrWithoutWAL(
			testhelper.WithInterceptedMetadataItems(
				structerr.NewNotFound("repository not found"),
				structerr.MetadataItem{Key: "relative_path", Value: poolRelativePath},
				structerr.MetadataItem{Key: "storage_name", Value: cfg.Storages[0].Name},
			),
			structerr.NewFailedPrecondition("object pool is not a valid git repository"),
		),
		testhelper.WithInterceptedMetadataItems(
			structerr.NewNotFound("mutator call: route repository mutator: resolve additional replica path: additional repository not found"),
			structerr.MetadataItem{Key: "relative_path", Value: poolRelativePath},
			structerr.MetadataItem{Key: "storage_name", Value: cfg.Storages[0].Name},
		),
	), err)
}
