package backup_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"golang.org/x/exp/slices"
)

func removeHeadReference(refs []git.Reference) []git.Reference {
	for i := range refs {
		if refs[i].Name == "HEAD" {
			return slices.Delete(refs, i, i+1)
		}
	}

	return refs
}

func TestRemoteRepository_ResetRefs(t *testing.T) {
	cfg := testcfg.Build(t)
	cfg.SocketPath = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)
	ctx := testhelper.Context(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	pool := client.NewPool()
	defer testhelper.MustClose(t, pool)

	conn, err := pool.Dial(ctx, cfg.SocketPath, "")
	require.NoError(t, err)

	rr := backup.NewRemoteRepository(repo, conn)

	// Create some commits
	c0 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
	c1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(c0), gittest.WithBranch("main"))
	c2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(c1), gittest.WithBranch("branch-1"))

	// "Snapshot" the refs to pretend this is our backup.
	backupRefState, err := rr.ListRefs(ctx)
	require.NoError(t, err)
	backupRefState = removeHeadReference(backupRefState)

	// Create some more commits
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(c1), gittest.WithBranch("main"))
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(c2), gittest.WithBranch("branch-1"))
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(c2), gittest.WithBranch("branch-2"))

	intermediateRefState, err := rr.ListRefs(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, len(intermediateRefState)) // 3 branches + HEAD

	// Reset the state of the refs to the backup.
	require.NoError(t, rr.ResetRefs(ctx, backupRefState))

	actualRefState, err := rr.ListRefs(ctx)
	require.NoError(t, err)

	actualRefState = removeHeadReference(actualRefState)
	require.Equal(t, backupRefState, actualRefState)
}

func TestLocalRepository_ResetRefs(t *testing.T) {
	if testhelper.IsPraefectEnabled() {
		t.Skip("local backup manager expects to operate on the local filesystem so cannot operate through praefect")
	}

	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	txManager := transaction.NewTrackingManager()
	repoCounter := counter.NewRepositoryCounter(cfg.Storages)
	locator := config.NewLocator(cfg)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	lr := localrepo.New(testhelper.SharedLogger(t), locator, gitCmdFactory, catfileCache, repo)
	localRepo := backup.NewLocalRepository(
		testhelper.SharedLogger(t),
		locator,
		gitCmdFactory,
		txManager,
		repoCounter,
		lr)

	// Create some commits
	c0 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
	c1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(c0), gittest.WithBranch("main"))
	c2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(c1), gittest.WithBranch("branch-1"))

	// "Snapshot" the refs to pretend this is our backup.
	backupRefState, err := lr.GetReferences(ctx)
	require.NoError(t, err)

	// Create some more commits
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(c1), gittest.WithBranch("main"))
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(c2), gittest.WithBranch("branch-1"))
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(c2), gittest.WithBranch("branch-2"))

	intermediateRefState, err := lr.GetReferences(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, len(intermediateRefState)) // 3 branches

	// Reset the state of the refs to the backup.
	require.NoError(t, localRepo.ResetRefs(ctx, backupRefState))
	actualRefState, err := lr.GetReferences(ctx)
	require.NoError(t, err)

	require.Equal(t, backupRefState, actualRefState)
}
