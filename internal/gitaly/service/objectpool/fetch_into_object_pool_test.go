package objectpool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/proto"
)

func TestFetchIntoObjectPool_Success(t *testing.T) {
	testhelper.SkipWithReftable(t, "reads refs directly from the filesystem")

	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, repoPath, _, client := setup(t, ctx)

	parentID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	poolProto, _, poolPath := createObjectPool(t, ctx, cfg, repo)

	// Create a new commit after having created the object pool. This commit exists only in the
	// pool member, but not in the pool itself.
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(parentID), gittest.WithBranch("main"))
	gittest.RequireObjectExists(t, cfg, repoPath, commitID)
	gittest.RequireObjectNotExists(t, cfg, poolPath, commitID)

	req := &gitalypb.FetchIntoObjectPoolRequest{
		ObjectPool: poolProto,
		Origin:     repo,
	}

	// Now we update the object pool, which should pull the new commit into the pool.
	_, err := client.FetchIntoObjectPool(ctx, req)
	require.NoError(t, err)

	// Verify that the object pool is still consistent and that it's got the new commit now.
	gittest.Exec(t, cfg, "-C", poolPath, "fsck")
	gittest.RequireObjectExists(t, cfg, poolPath, commitID)

	// Re-fetching the pool should be just fine.
	_, err = client.FetchIntoObjectPool(ctx, req)
	require.NoError(t, err)

	// We now create a broken reference that is all-empty and stale. Normally, such references
	// break many Git commands, including git-fetch(1). We should know to prune stale broken
	// references though and thus be able to recover.
	brokenRef := filepath.Join(poolPath, "refs", "heads", "broken")
	require.NoError(t, os.MkdirAll(filepath.Dir(brokenRef), perm.SharedDir))
	require.NoError(t, os.WriteFile(brokenRef, []byte{}, perm.PublicFile))
	oldTime := time.Now().Add(-25 * time.Hour)
	require.NoError(t, os.Chtimes(brokenRef, oldTime, oldTime))

	// So the fetch should be successful, and...
	_, err = client.FetchIntoObjectPool(ctx, req)
	require.NoError(t, err)

	// The handler executes housekeeping to remove broken references. While this may have been a reasonable
	// pre-caution, it's not the RPC handlers job to remove broken state. Further, these changes are not voted
	// on with Praefect. Disable this assertion if transactions are enabled as the transaction manager is
	// responsible for preventing broken state in the repository.
	if !testhelper.IsWALEnabled() {
		// ... it should have pruned the broken reference.
		require.NoFileExists(t, brokenRef)
	}
}

func TestFetchIntoObjectPool_transactional(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

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

	cfg := testcfg.Build(t)
	cfg.SocketPath = runObjectPoolServer(
		t, cfg, config.NewLocator(cfg),
		testhelper.SharedLogger(t),
		testserver.WithTransactionManager(&txManager),
		// We need to disable Praefect given that we replace transactions with our own logic
		// here.
		testserver.WithDisablePraefect(),
	)
	testcfg.BuildGitalyHooks(t, cfg)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	conn, err := grpc.Dial(cfg.SocketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer testhelper.MustClose(t, conn)

	client := gitalypb.NewObjectPoolServiceClient(conn)

	poolProto, pool, poolPath := createObjectPool(t, ctx, cfg, repo)

	// Inject transaction information so that FetchIntoObjectPool knows to perform
	// transactional voting.
	ctx, err = txinfo.InjectTransaction(peer.NewContext(ctx, &peer.Peer{}), 1, "node", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	t.Run("without changed data", func(t *testing.T) {
		votes = nil

		_, err = client.FetchIntoObjectPool(ctx, &gitalypb.FetchIntoObjectPoolRequest{
			ObjectPool: poolProto,
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
			ObjectPool: poolProto,
			Origin:     repo,
		})
		require.NoError(t, err)

		vote := voting.VoteFromData([]byte(fmt.Sprintf(
			"%s %s refs/remotes/origin/heads/new-branch\n", gittest.DefaultObjectHash.ZeroOID, repoCommit,
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
		gittest.WriteCommit(t, cfg, poolPath, gittest.WithParents(), gittest.WithReference(reference))

		_, err = client.FetchIntoObjectPool(ctx, &gitalypb.FetchIntoObjectPoolRequest{
			ObjectPool: poolProto,
			Origin:     repo,
		})
		require.NoError(t, err)

		// We expect a single vote on the reference we have deleted.
		vote := voting.VoteFromData([]byte(fmt.Sprintf(
			"%[1]s %[1]s %s\n", gittest.DefaultObjectHash.ZeroOID, reference,
		)))
		require.Equal(t, []voting.Vote{vote, vote}, votes)

		exists, err := pool.Repo.HasRevision(ctx, git.Revision(reference))
		require.NoError(t, err)
		require.False(t, exists)
	})
}

func TestFetchIntoObjectPool_CollectLogStatistics(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)

	locator := config.NewLocator(cfg)

	logger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(logger)
	cfg.SocketPath = runObjectPoolServer(t, cfg, locator, logger)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))

	conn, err := grpc.Dial(cfg.SocketPath, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { testhelper.MustClose(t, conn) })
	client := gitalypb.NewObjectPoolServiceClient(conn)

	poolProto, _, _ := createObjectPool(t, ctx, cfg, repo)

	req := &gitalypb.FetchIntoObjectPoolRequest{
		ObjectPool: poolProto,
		Origin:     repo,
	}

	_, err = client.FetchIntoObjectPool(ctx, req)
	require.NoError(t, err)

	for _, logEntry := range hook.AllEntries() {
		if repoInfo, ok := logEntry.Data["repository_info"]; ok {
			require.IsType(t, stats.RepositoryInfo{}, repoInfo)
			return
		}
	}
	require.FailNow(t, "no info about statistics")
}

func TestFetchIntoObjectPool_Failure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, _, _, client := setup(t, ctx, testserver.WithDisablePraefect())
	poolProto, _, _ := createObjectPool(t, ctx, cfg, repo)

	poolWithDifferentStorage := proto.Clone(poolProto).(*gitalypb.ObjectPool)
	poolWithDifferentStorage.Repository.StorageName = "some other storage"

	for _, tc := range []struct {
		description string
		request     *gitalypb.FetchIntoObjectPoolRequest
		code        codes.Code
		errMsg      string
	}{
		{
			description: "empty origin",
			request: &gitalypb.FetchIntoObjectPoolRequest{
				ObjectPool: poolProto,
			},
			code:   codes.InvalidArgument,
			errMsg: "origin is empty",
		},
		{
			description: "empty pool",
			request: &gitalypb.FetchIntoObjectPoolRequest{
				Origin: repo,
			},
			code: codes.InvalidArgument,
			errMsg: func() string {
				if testhelper.IsWALEnabled() {
					return storage.ErrRepositoryNotSet.Error()
				}

				return "object pool is empty"
			}(),
		},
		{
			description: "origin and pool do not share the same storage",
			request: &gitalypb.FetchIntoObjectPoolRequest{
				Origin:     repo,
				ObjectPool: poolWithDifferentStorage,
			},
			code: codes.InvalidArgument,
			errMsg: func() string {
				if testhelper.IsWALEnabled() {
					return "storage name not found"
				}

				return "origin has different storage than object pool"
			}(),
		},
	} {
		t.Run(tc.description, func(t *testing.T) {
			_, err := client.FetchIntoObjectPool(ctx, tc.request)
			require.Error(t, err)
			testhelper.RequireGrpcCode(t, err, tc.code)
			assert.Contains(t, err.Error(), tc.errMsg)
		})
	}
}

func TestFetchIntoObjectPool_dfConflict(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, repoPath, _, client := setup(t, ctx)
	poolProto, _, poolPath := createObjectPool(t, ctx, cfg, repo)

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))

	// Perform an initial fetch into the object pool with the given object that exists in the
	// pool member's repository.
	_, err := client.FetchIntoObjectPool(ctx, &gitalypb.FetchIntoObjectPoolRequest{
		ObjectPool: poolProto,
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
		ObjectPool: poolProto,
		Origin:     repo,
	})
	require.NoError(t, err)

	// Verify that the conflicting reference exists now.
	gittest.Exec(t, cfg, "-C", poolPath, "rev-parse", "refs/remotes/origin/heads/branch/conflict")
}
