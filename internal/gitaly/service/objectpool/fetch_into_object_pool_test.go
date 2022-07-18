//go:build !gitaly_test_sha256

package objectpool

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/labkit/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestFetchIntoObjectPool_Success(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.FetchIntoObjectPoolOptimizeRepository).Run(t, testFetchIntoObjectPoolSuccess)
}

func testFetchIntoObjectPoolSuccess(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg, repo, repoPath, locator, client := setup(ctx, t)

	repoCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(t.Name()))

	pool := initObjectPool(t, cfg, cfg.Storages[0])
	_, err := client.CreateObjectPool(ctx, &gitalypb.CreateObjectPoolRequest{
		ObjectPool: pool.ToProto(),
		Origin:     repo,
	})
	require.NoError(t, err)

	req := &gitalypb.FetchIntoObjectPoolRequest{
		ObjectPool: pool.ToProto(),
		Origin:     repo,
		Repack:     true,
	}

	_, err = client.FetchIntoObjectPool(ctx, req)
	require.NoError(t, err)

	pool = rewrittenObjectPool(ctx, t, cfg, pool)

	require.True(t, pool.IsValid(), "ensure underlying repository is valid")

	// No problems
	gittest.Exec(t, cfg, "-C", pool.FullPath(), "fsck")

	// Verify that the newly written commit exists in the repository now.
	gittest.Exec(t, cfg, "-C", pool.FullPath(), "rev-parse", "--verify", repoCommit.String())

	if featureflag.FetchIntoObjectPoolOptimizeRepository.IsDisabled(ctx) {
		// We used to verify here how objects have been packed. This doesn't make a lot of
		// sense anymore in the new world though, where we rely on the housekeeping package
		// to do repository maintenance for us. We thus only check for this when the feature
		// flag is disabled.
		packFiles, err := filepath.Glob(filepath.Join(pool.FullPath(), "objects", "pack", "pack-*.pack"))
		require.NoError(t, err)
		require.Len(t, packFiles, 1, "ensure commits got packed")

		packContents := gittest.Exec(t, cfg, "-C", pool.FullPath(), "verify-pack", "-v", packFiles[0])
		require.Contains(t, string(packContents), repoCommit.String())
	}

	_, err = client.FetchIntoObjectPool(ctx, req)
	require.NoError(t, err, "calling FetchIntoObjectPool twice should be OK")
	require.True(t, pool.IsValid(), "ensure that pool is valid")

	// Simulate a broken ref
	poolPath, err := locator.GetRepoPath(pool)
	require.NoError(t, err)
	brokenRef := filepath.Join(poolPath, "refs", "heads", "broken")
	require.NoError(t, os.MkdirAll(filepath.Dir(brokenRef), 0o755))
	require.NoError(t, os.WriteFile(brokenRef, []byte{}, 0o777))

	oldTime := time.Now().Add(-25 * time.Hour)
	require.NoError(t, os.Chtimes(brokenRef, oldTime, oldTime))

	_, err = client.FetchIntoObjectPool(ctx, req)
	require.NoError(t, err)

	_, err = os.Stat(brokenRef)
	require.Error(t, err, "Expected refs/heads/broken to be deleted")
}

func TestFetchIntoObjectPool_hooks(t *testing.T) {
	cfg := testcfg.Build(t)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg, git.WithHooksPath(testhelper.TempDir(t)))

	cfg.SocketPath = runObjectPoolServer(t, cfg, config.NewLocator(cfg), testhelper.NewDiscardingLogger(t), testserver.WithGitCommandFactory(gitCmdFactory))

	ctx := testhelper.Context(t)
	repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	conn, err := grpc.Dial(cfg.SocketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer testhelper.MustClose(t, conn)

	client := gitalypb.NewObjectPoolServiceClient(conn)

	pool := initObjectPool(t, cfg, cfg.Storages[0])
	_, err = client.CreateObjectPool(ctx, &gitalypb.CreateObjectPoolRequest{
		ObjectPool: pool.ToProto(),
		Origin:     repo,
	})
	require.NoError(t, err)

	// Set up a custom reference-transaction hook which simply exits failure. This asserts that
	// the RPC doesn't invoke any reference-transaction.
	testhelper.WriteExecutable(t, filepath.Join(gitCmdFactory.HooksPath(ctx), "reference-transaction"), []byte("#!/bin/sh\nexit 1\n"))

	req := &gitalypb.FetchIntoObjectPoolRequest{
		ObjectPool: pool.ToProto(),
		Origin:     repo,
		Repack:     true,
	}

	_, err = client.FetchIntoObjectPool(ctx, req)
	testhelper.RequireGrpcError(t, status.Error(codes.Internal, "fetch into object pool: exit status 128, stderr: \"fatal: ref updates aborted by hook\\n\""), err)
}

func TestFetchIntoObjectPool_CollectLogStatistics(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.FetchIntoObjectPoolOptimizeRepository).Run(t, testFetchIntoObjectPoolCollectLogStatistics)
}

func testFetchIntoObjectPoolCollectLogStatistics(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)

	locator := config.NewLocator(cfg)

	logger, hook := test.NewNullLogger()
	cfg.SocketPath = runObjectPoolServer(t, cfg, locator, logger)

	ctx = ctxlogrus.ToContext(ctx, log.WithField("test", "logging"))
	repo, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	conn, err := grpc.Dial(cfg.SocketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { testhelper.MustClose(t, conn) })
	client := gitalypb.NewObjectPoolServiceClient(conn)

	pool := initObjectPool(t, cfg, cfg.Storages[0])
	_, err = client.CreateObjectPool(ctx, &gitalypb.CreateObjectPoolRequest{
		ObjectPool: pool.ToProto(),
		Origin:     repo,
	})
	require.NoError(t, err)

	req := &gitalypb.FetchIntoObjectPoolRequest{
		ObjectPool: pool.ToProto(),
		Origin:     repo,
		Repack:     true,
	}

	_, err = client.FetchIntoObjectPool(ctx, req)
	require.NoError(t, err)

	const key = "count_objects"
	for _, logEntry := range hook.AllEntries() {
		if stats, ok := logEntry.Data[key]; ok {
			require.IsType(t, map[string]interface{}{}, stats)

			var keys []string
			for key := range stats.(map[string]interface{}) {
				keys = append(keys, key)
			}

			require.ElementsMatch(t, []string{
				"count",
				"garbage",
				"in-pack",
				"packs",
				"prune-packable",
				"size",
				"size-garbage",
				"size-pack",
			}, keys)
			return
		}
	}
	require.FailNow(t, "no info about statistics")
}

func TestFetchIntoObjectPool_Failure(t *testing.T) {
	cfgBuilder := testcfg.NewGitalyCfgBuilder()
	cfg, repos := cfgBuilder.BuildWithRepoAt(t, t.Name())

	locator := config.NewLocator(cfg)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)
	txManager := transaction.NewManager(cfg, backchannel.NewRegistry())

	server := NewServer(
		locator,
		gitCmdFactory,
		catfileCache,
		txManager,
		housekeeping.NewManager(cfg.Prometheus, txManager),
	)
	ctx := testhelper.Context(t)

	pool := initObjectPool(t, cfg, cfg.Storages[0])

	poolWithDifferentStorage := pool.ToProto()
	poolWithDifferentStorage.Repository.StorageName = "some other storage"

	testCases := []struct {
		description string
		request     *gitalypb.FetchIntoObjectPoolRequest
		code        codes.Code
		errMsg      string
	}{
		{
			description: "empty origin",
			request: &gitalypb.FetchIntoObjectPoolRequest{
				ObjectPool: pool.ToProto(),
			},
			code:   codes.InvalidArgument,
			errMsg: "origin is empty",
		},
		{
			description: "empty pool",
			request: &gitalypb.FetchIntoObjectPoolRequest{
				Origin: repos[0],
			},
			code:   codes.InvalidArgument,
			errMsg: "object pool is empty",
		},
		{
			description: "origin and pool do not share the same storage",
			request: &gitalypb.FetchIntoObjectPoolRequest{
				Origin:     repos[0],
				ObjectPool: poolWithDifferentStorage,
			},
			code:   codes.InvalidArgument,
			errMsg: "origin has different storage than object pool",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			_, err := server.FetchIntoObjectPool(ctx, tc.request)
			require.Error(t, err)
			testhelper.RequireGrpcCode(t, err, tc.code)
			assert.Contains(t, err.Error(), tc.errMsg)
		})
	}
}
