package repository

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestPruneUnreachableObjects(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	setObjectTime := func(t *testing.T, repoPath string, objectID git.ObjectID, when time.Time) {
		looseObjectPath := filepath.Join(repoPath, "objects", objectID.String()[:2], objectID.String()[2:])
		require.NoError(t, os.Chtimes(looseObjectPath, when, when))
	}

	t.Run("unset repository", func(t *testing.T) {
		_, err := client.PruneUnreachableObjects(ctx, &gitalypb.PruneUnreachableObjectsRequest{})
		testhelper.RequireGrpcError(t, structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet), err)
	})

	t.Run("missing repository", func(t *testing.T) {
		relativePath := gittest.NewRepositoryName(t)

		_, err := client.PruneUnreachableObjects(ctx, &gitalypb.PruneUnreachableObjectsRequest{
			Repository: &gitalypb.Repository{
				StorageName:  cfg.Storages[0].Name,
				RelativePath: relativePath,
			},
		})
		testhelper.RequireGrpcError(t, testhelper.ToInterceptedMetadata(
			structerr.New("%w", storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, relativePath)),
		), err)
	})

	t.Run("empty repository", func(t *testing.T) {
		repo, _ := gittest.CreateRepository(t, ctx, cfg)

		_, err := client.PruneUnreachableObjects(ctx, &gitalypb.PruneUnreachableObjectsRequest{
			Repository: repo,
		})
		require.NoError(t, err)
	})

	t.Run("repository with reachable objects", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		// Create the commit and a branch pointing to it to make it reachable.
		commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))

		_, err := client.PruneUnreachableObjects(ctx, &gitalypb.PruneUnreachableObjectsRequest{
			Repository: repo,
		})
		require.NoError(t, err)

		// Verify we can still read the commit.
		gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "--verify", commitID.String()+"^{commit}")
	})

	t.Run("repository with recent unreachable objects", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		// Create the commit, but don't create a reference pointing to it.
		commitID := gittest.WriteCommit(t, cfg, repoPath)
		// Set the object time to something that's close to 30 minutes, but gives us enough
		// room to not cause flakes.
		setObjectTime(t, repoPath, commitID, time.Now().Add(-28*time.Minute))

		_, err := client.PruneUnreachableObjects(ctx, &gitalypb.PruneUnreachableObjectsRequest{
			Repository: repo,
		})
		require.NoError(t, err)

		if testhelper.WithOrWithoutWAL(true, false) {
			// With transactions, all unreachable objects are deleted in a pruning run. Transactions
			// prevent races in concurrently making objects reachable and pruning them.
			gittest.RequireObjectNotExists(t, cfg, repoPath, commitID)
		} else {
			// Without transactions, unreachable objects will be kept in a cruft pack before being removed
			// after a grace period. This guards against races in making a reference reachable and
			// concurrently pruning it.
			gittest.RequireObjectExists(t, cfg, repoPath, commitID)
		}
	})

	t.Run("repository with old unreachable objects", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		// Create the commit, but don't create a reference pointing to it.
		commitID := gittest.WriteCommit(t, cfg, repoPath)
		setObjectTime(t, repoPath, commitID, time.Now().Add(-31*time.Minute))

		_, err := client.PruneUnreachableObjects(ctx, &gitalypb.PruneUnreachableObjectsRequest{
			Repository: repo,
		})
		require.NoError(t, err)

		cmd := gittest.NewCommand(t, cfg, "-C", repoPath, "rev-parse", "--verify", commitID.String()+"^{commit}")
		output, err := cmd.CombinedOutput()
		require.Error(t, err)
		require.Equal(t, "fatal: Needed a single revision\n", string(output))
	})

	t.Run("repository with mixed objects", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		reachableOldCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("a"), gittest.WithBranch("branch"))
		setObjectTime(t, repoPath, reachableOldCommit, time.Now().Add(-31*time.Minute))

		unreachableRecentCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("b"))
		setObjectTime(t, repoPath, unreachableRecentCommit, time.Now().Add(-28*time.Minute))

		unreachableOldCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("c"))
		setObjectTime(t, repoPath, unreachableOldCommit, time.Now().Add(-31*time.Minute))

		_, err := client.PruneUnreachableObjects(ctx, &gitalypb.PruneUnreachableObjectsRequest{
			Repository: repo,
		})
		require.NoError(t, err)

		// The reachable old recent commit should still exist.
		gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "--verify", reachableOldCommit.String()+"^{commit}")

		if testhelper.WithOrWithoutWAL(true, false) {
			// With transactions, all unreachable objects are deleted in a pruning run. Transactions
			// prevent races in concurrently making objects reachable and pruning them.
			gittest.RequireObjectNotExists(t, cfg, repoPath, unreachableRecentCommit)
		} else {
			// Without transactions, unreachable objects will be kept in a cruft pack before being removed
			// after a grace period. This guards against races in making a reference reachable and
			// concurrently pruning it.
			gittest.RequireObjectExists(t, cfg, repoPath, unreachableRecentCommit)
		}

		// But the unreachable old commit should have been pruned.
		cmd := gittest.NewCommand(t, cfg, "-C", repoPath, "rev-parse", "--verify", unreachableOldCommit.String()+"^{commit}")
		output, err := cmd.CombinedOutput()
		require.Error(t, err)
		require.Equal(t, "fatal: Needed a single revision\n", string(output))
	})

	t.Run("repository with commit-graph", func(t *testing.T) {
		repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

		// Write two commits into the repository and create a commit-graph. The second
		// commit will become unreachable and will be pruned, but will be contained in the
		// commit-graph.
		rootCommitID := gittest.WriteCommit(t, cfg, repoPath)
		unreachableCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(rootCommitID), gittest.WithBranch("main"))
		gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--split", "--changed-paths")

		// Reset the "main" branch back to the initial root commit ID and prune the now
		// unreachable second commit.
		gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/main", rootCommitID.String())

		// Modify the modification time of the unreachable commit ID so that it will be
		// pruned.
		setObjectTime(t, repoPath, unreachableCommitID, time.Now().Add(-30*time.Minute))

		_, err := client.PruneUnreachableObjects(ctx, &gitalypb.PruneUnreachableObjectsRequest{
			Repository: repo,
		})
		require.NoError(t, err)

		// The commit-graph chain should have been rewritten by PruneUnreachableObjects so
		// that it doesn't refer to the pruned commit anymore.
		gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "verify")
	})

	t.Run("repository with recent objects in cruft pack", func(t *testing.T) {
		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		// Write two commits into the repository and create a commit-graph. The second
		// commit will become unreachable and will be pruned, but will be contained in the
		// commit-graph.
		reachableCommitID := gittest.WriteCommit(t, cfg, repoPath)
		unreachableCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(reachableCommitID), gittest.WithBranch("main"))

		// Reset the "main" branch back to the initial root commit ID.
		gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/main", reachableCommitID.String())

		// We now repack the repository with cruft packs. The result should be that we have
		// two packs in the repository.
		gittest.Exec(t, cfg, "-C", repoPath, "repack", "--cruft", "-d")
		packfilesInfo, err := stats.PackfilesInfoForRepository(repo)
		require.NoError(t, err)
		require.EqualValues(t, 2, packfilesInfo.Count)
		require.EqualValues(t, 1, packfilesInfo.CruftCount)

		_, err = client.PruneUnreachableObjects(ctx, &gitalypb.PruneUnreachableObjectsRequest{
			Repository: repoProto,
		})
		require.NoError(t, err)

		// Reachable objects should always be kept.
		gittest.RequireObjectExists(t, cfg, repoPath, reachableCommitID)
		if testhelper.WithOrWithoutWAL(true, false) {
			// With transactions, all unreachable objects are deleted in a pruning run. Transactions
			// prevent races in concurrently making objects reachable and pruning them.
			gittest.RequireObjectNotExists(t, cfg, repoPath, unreachableCommitID)
		} else {
			// Without transactions, unreachable objects will be kept in a cruft pack before being removed
			// after a grace period. This guards against races in making a reference reachable and
			// concurrently pruning it.
			gittest.RequireObjectExists(t, cfg, repoPath, unreachableCommitID)
		}
	})

	t.Run("repository with stale objects in cruft pack", func(t *testing.T) {
		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		// Write two commits into the repository and create a commit-graph. The second
		// commit will become unreachable and will be pruned, but will be contained in the
		// commit-graph.
		reachableCommitID := gittest.WriteCommit(t, cfg, repoPath)
		unreachableCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(reachableCommitID), gittest.WithBranch("main"))

		// Set the object time of both commits to be older than our 30 minute grace period.
		// As a result, they would both be removed via cruft packs if they were to be
		// considered unreachable.
		setObjectTime(t, repoPath, reachableCommitID, time.Now().Add(-31*time.Minute))
		setObjectTime(t, repoPath, unreachableCommitID, time.Now().Add(-31*time.Minute))

		// Reset the "main" branch back to the initial root commit ID.
		gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/main", reachableCommitID.String())

		// We now repack the repository with cruft packs. The result should be that we have
		// two packs in the repository.
		gittest.Exec(t, cfg, "-C", repoPath, "repack", "--cruft", "-d")
		packfilesInfo, err := stats.PackfilesInfoForRepository(repo)
		require.NoError(t, err)
		require.EqualValues(t, 2, packfilesInfo.Count)
		require.EqualValues(t, 1, packfilesInfo.CruftCount)

		_, err = client.PruneUnreachableObjects(ctx, &gitalypb.PruneUnreachableObjectsRequest{
			Repository: repoProto,
		})
		require.NoError(t, err)

		// The reachable commit should exist, but the unreachable one shouldn't.
		gittest.RequireObjectExists(t, cfg, repoPath, reachableCommitID)
		gittest.RequireObjectNotExists(t, cfg, repoPath, unreachableCommitID)
	})

	t.Run("object pool", func(t *testing.T) {
		repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			RelativePath: gittest.NewObjectPoolName(t),
		})

		_, err := client.PruneUnreachableObjects(ctx, &gitalypb.PruneUnreachableObjectsRequest{
			Repository: repo,
		})

		testhelper.RequireGrpcError(t, structerr.NewInvalidArgument("pruning objects for object pool"), err)
	})
}
