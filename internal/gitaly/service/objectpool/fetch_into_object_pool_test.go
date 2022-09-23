//go:build !gitaly_test_sha256

package objectpool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/labkit/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
)

func TestFetchIntoObjectPool_Success(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
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
	}

	_, err = client.FetchIntoObjectPool(ctx, req)
	require.NoError(t, err)

	pool = rewrittenObjectPool(ctx, t, cfg, pool)

	require.True(t, pool.IsValid(), "ensure underlying repository is valid")

	// No problems
	gittest.Exec(t, cfg, "-C", pool.FullPath(), "fsck")

	// Verify that the newly written commit exists in the repository now.
	gittest.Exec(t, cfg, "-C", pool.FullPath(), "rev-parse", "--verify", repoCommit.String())

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

func TestFetchIntoObjectPool_transactional(t *testing.T) {
	t.Parallel()

	var votes []voting.Vote
	var votesMutex sync.Mutex
	txManager := transaction.MockManager{
		VoteFn: func(_ context.Context, _ txinfo.Transaction, vote voting.Vote, _ voting.Phase) error {
			votesMutex.Lock()
			defer votesMutex.Unlock()
			votes = append(votes, vote)
			return nil
		},
	}

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	cfg.SocketPath = runObjectPoolServer(
		t, cfg, config.NewLocator(cfg),
		testhelper.NewDiscardingLogger(t),
		testserver.WithTransactionManager(&txManager),
		// We need to disable Praefect given that we replace transactions with our own logic
		// here.
		testserver.WithDisablePraefect(),
	)
	testcfg.BuildGitalyHooks(t, cfg)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

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

	// CreateObjectPool has a bug because it will leave the configuration of the origin remote
	// in the gitconfig. This will get cleaned up at a later point by our housekeeping logic, so
	// it doesn't hurt much in the first place to have it. But the cleanup logic would trigger
	// another transactional vote which we want to avoid, so we simply unset the configuration
	// here.
	gittest.Exec(t, cfg, "-C", pool.FullPath(), "config", "--unset", "remote.origin.url")

	// Inject transaction information so that FetchInotObjectPool knows to perform
	// transactional voting.
	ctx, err = txinfo.InjectTransaction(peer.NewContext(ctx, &peer.Peer{}), 1, "node", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	t.Run("without changed data", func(t *testing.T) {
		votes = nil

		_, err = client.FetchIntoObjectPool(ctx, &gitalypb.FetchIntoObjectPoolRequest{
			ObjectPool: pool.ToProto(),
			Origin:     repo,
		})
		require.NoError(t, err)

		require.Equal(t, []voting.Vote{
			// We expect to see two votes that demonstrate we're voting on no deleted
			// references.
			voting.VoteFromData(nil), voting.VoteFromData(nil),
			// It is a bug though that we don't have a vote on the unchanged references
			// in git-fetch(1).
		}, votes)
	})

	t.Run("with a new reference", func(t *testing.T) {
		votes = nil

		// Create a new reference that we'd in fact fetch into the object pool so that we
		// know that something will be voted on.
		repoCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(), gittest.WithBranch("new-branch"))

		_, err = client.FetchIntoObjectPool(ctx, &gitalypb.FetchIntoObjectPoolRequest{
			ObjectPool: pool.ToProto(),
			Origin:     repo,
		})
		require.NoError(t, err)

		vote := voting.VoteFromData([]byte(fmt.Sprintf(
			"%s %s refs/remotes/origin/heads/new-branch\n", git.ObjectHashSHA1.ZeroOID, repoCommit,
		)))
		require.Equal(t, []voting.Vote{
			// The first two votes stem from the fact that we're voting on no
			// deleted references.
			voting.VoteFromData(nil), voting.VoteFromData(nil),
			// And the other two votes are from the new branch we pull in.
			vote, vote,
		}, votes)
	})

	t.Run("with a stale reference in pool", func(t *testing.T) {
		votes = nil

		reference := "refs/remotes/origin/heads/to-be-pruned"

		// Create a commit in the pool repository itself. Right now, we don't touch this
		// commit at all, but this will change in one of the next commits.
		gittest.WriteCommit(t, cfg, pool.FullPath(), gittest.WithParents(), gittest.WithReference(reference))

		_, err = client.FetchIntoObjectPool(ctx, &gitalypb.FetchIntoObjectPoolRequest{
			ObjectPool: pool.ToProto(),
			Origin:     repo,
		})
		require.NoError(t, err)

		// We expect a single vote on the reference we have deleted.
		vote := voting.VoteFromData([]byte(fmt.Sprintf(
			"%[1]s %[1]s %s\n", git.ObjectHashSHA1.ZeroOID, reference,
		)))
		require.Equal(t, []voting.Vote{vote, vote}, votes)

		exists, err := pool.Repo.HasRevision(ctx, git.Revision(reference))
		require.NoError(t, err)
		require.False(t, exists)
	})
}

func TestFetchIntoObjectPool_CollectLogStatistics(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)

	locator := config.NewLocator(cfg)

	logger, hook := test.NewNullLogger()
	cfg.SocketPath = runObjectPoolServer(t, cfg, locator, logger)

	ctx := testhelper.Context(t)
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

func TestFetchIntoObjectPool_dfConflict(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, repoPath, _, client := setup(ctx, t)

	pool := initObjectPool(t, cfg, cfg.Storages[0])
	_, err := client.CreateObjectPool(ctx, &gitalypb.CreateObjectPoolRequest{
		ObjectPool: pool.ToProto(),
		Origin:     repo,
	})
	require.NoError(t, err)

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))

	// Perform an initial fetch into the object pool with the given object that exists in the
	// pool member's repository.
	_, err = client.FetchIntoObjectPool(ctx, &gitalypb.FetchIntoObjectPoolRequest{
		ObjectPool: pool.ToProto(),
		Origin:     repo,
	})
	require.NoError(t, err)

	// Now we delete the reference in the pool member and create a new reference that has the
	// same prefix, but is stored in a subdirectory. This will create a D/F conflict.
	gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "-d", "refs/heads/branch")
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch/conflict"))

	// Verify that we can still fetch into the object pool regardless of the D/F conflict. While
	// it is not possible to store both references at the same time due to the conflict, we
	// should know to delete the old conflicting reference and replace it with the new one.
	_, err = client.FetchIntoObjectPool(ctx, &gitalypb.FetchIntoObjectPoolRequest{
		ObjectPool: pool.ToProto(),
		Origin:     repo,
	})
	require.NoError(t, err)

	poolPath, err := config.NewLocator(cfg).GetRepoPath(gittest.RewrittenRepository(ctx, t, cfg, pool.ToProto().GetRepository()))
	require.NoError(t, err)

	// Verify that the conflicting reference exists now.
	gittest.Exec(t, cfg, "-C", poolPath, "rev-parse", "refs/remotes/origin/heads/branch/conflict")
}
