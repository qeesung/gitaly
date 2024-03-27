package objectpool

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	housekeepingmgr "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/manager"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/peer"
)

func TestDisconnect(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	logger := testhelper.SharedLogger(t)

	type setupData struct {
		repository      *localrepo.Repo
		txManager       transaction.Manager
		expectedObjects []git.ObjectID
		expectedVotes   []transaction.PhasedVote
		expectedError   error
	}

	// setupRepoWithObjectPool creates a repository and an object pool that are linked together.
	setupRepoWithObjectPool := func(t *testing.T, ctx context.Context) (*localrepo.Repo, *ObjectPool) {
		repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		txManager := transaction.NewManager(cfg, logger, nil)
		catfileCache := catfile.NewCache(cfg)
		t.Cleanup(catfileCache.Stop)

		pool, err := Create(
			ctx,
			logger,
			config.NewLocator(cfg),
			gittest.NewCommandFactory(t, cfg, git.WithSkipHooks()),
			catfileCache,
			txManager,
			housekeepingmgr.New(cfg.Prometheus, logger, txManager, nil),
			&gitalypb.ObjectPool{
				Repository: &gitalypb.Repository{
					StorageName:  cfg.Storages[0].Name,
					RelativePath: gittest.NewObjectPoolName(t),
				},
			},
			repo,
		)
		require.NoError(t, err)

		require.NoError(t, pool.Link(ctx, repo))

		return repo, pool
	}

	// setupRepoWithAlternates creates a repository with an alternates file containing the specified
	// contents. This is used to test invalid Git alternate file configurations.
	setupRepoWithAlternates := func(t *testing.T, ctx context.Context, altContent string) *localrepo.Repo {
		t.Helper()

		repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		altPath, err := repo.InfoAlternatesPath()
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(altPath, []byte(altContent), perm.SharedFile))

		return repo
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T, ctx context.Context) setupData
	}{
		{
			desc: "disconnect repository with object pool",
			setup: func(t *testing.T, ctx context.Context) setupData {
				repo, pool := setupRepoWithObjectPool(t, ctx)

				// Write a commit directly to the object pool. This commit is later checked to make
				// sure it is present after the disconnect.
				poolPath, err := pool.Path()
				require.NoError(t, err)
				commitID := gittest.WriteCommit(t, cfg, poolPath)

				// Write a reference in the main repository to the commit in the object pool. This
				// makes the repository dependent on the object pool and enables connectivity
				// validation of the repository.
				repoPath, err := repo.Path()
				require.NoError(t, err)
				gittest.WriteRef(t, cfg, repoPath, "refs/heads/main", commitID)

				return setupData{
					repository:      repo,
					expectedObjects: []git.ObjectID{commitID},
				}
			},
		},
		{
			desc: "disconnect repository fails connectivity check",
			setup: func(t *testing.T, ctx context.Context) setupData {
				repo, pool := setupRepoWithObjectPool(t, ctx)

				// Write a commit directly to the object pool. This commit will be referenced by the
				// main repository.
				poolPath, err := pool.Path()
				require.NoError(t, err)
				commitID := gittest.WriteCommit(t, cfg, poolPath)

				// Write a reference in the main repository to the commit in the object pool. This
				// makes the repository dependent on the object pool and enables connectivity
				// validation of the repository.
				repoPath, err := repo.Path()
				require.NoError(t, err)
				gittest.WriteRef(t, cfg, repoPath, "refs/heads/main", commitID)

				// Prune the object pool repository to remove the newly created commit. This results
				// in a corrupt main repository and should cause the repository to fail the
				// connectivity check performed after object pool disconnection.
				gittest.Exec(t, cfg, "-C", poolPath, "prune")

				return setupData{
					repository:    repo,
					expectedError: errors.New("git connectivity error while disconnected: exit status 128"),
				}
			},
		},
		{
			desc: "disconnect repository without object pool",
			setup: func(t *testing.T, ctx context.Context) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				repo := localrepo.NewTestRepo(t, cfg, repoProto)

				// Write a commit to the repository. Disconnecting a repository with no object pool
				// should have no effect on objects present in the repository.
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

				return setupData{
					repository:      repo,
					expectedObjects: []git.ObjectID{commitID},
				}
			},
		},
		{
			desc: "Git alternates with comments",
			setup: func(t *testing.T, ctx context.Context) setupData {
				repo, _ := setupRepoWithObjectPool(t, ctx)
				repoPath, err := repo.Path()
				require.NoError(t, err)

				// A Git alternates file may contain comments, it is important that the alternate
				// file is parsed correctly to not include comments. Comments are added to the
				// repositories alternate file to validate that it does not cause errors.
				altInfo, err := stats.AlternatesInfoForRepository(repoPath)
				require.NoError(t, err)
				altObjectDir := fmt.Sprintf("# foo\n%s\n# bar\n", altInfo.ObjectDirectories[0])

				altPath, err := repo.InfoAlternatesPath()
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(altPath, []byte(altObjectDir), perm.SharedFile))

				return setupData{
					repository: repo,
				}
			},
		},
		{
			desc: "multiple Git alternates",
			setup: func(t *testing.T, ctx context.Context) setupData {
				// If the Git alternates file contains multiple entries, the repository fails to be
				// disconnected.
				altContents := "/foo/bar\n/qux/baz\n"

				return setupData{
					repository:    setupRepoWithAlternates(t, ctx, altContents),
					expectedError: errors.New("multiple alternate object directories"),
				}
			},
		},
		{
			desc: "Git alternate does not exist",
			setup: func(t *testing.T, ctx context.Context) setupData {
				// If the path provided in the Git alternates file does not exist, the repository
				// fails to be disconnected.
				altContents := "/does/not/exist/\n"

				return setupData{
					repository: setupRepoWithAlternates(t, ctx, altContents),
					expectedError: &fs.PathError{
						Op:   "stat",
						Path: strings.TrimSpace(altContents),
						Err:  syscall.ENOENT,
					},
				}
			},
		},
		{
			desc: "Git alternate is not a directory",
			setup: func(t *testing.T, ctx context.Context) setupData {
				// If the path provided in the Git alternates file does not point to a directory,
				// the repository fails to be disconnected.
				altContents := "../HEAD\n"

				return setupData{
					repository:    setupRepoWithAlternates(t, ctx, altContents),
					expectedError: errors.New("alternate object entry is not a directory"),
				}
			},
		},
		{
			desc: "transactional disconnect successful with object pool",
			setup: func(t *testing.T, ctx context.Context) setupData {
				// If a repository is linked to an object pool, the transaction should contain
				// migrate and disconnect votes.
				repo, _ := setupRepoWithObjectPool(t, ctx)

				return setupData{
					repository: repo,
					txManager:  transaction.NewTrackingManager(),
					expectedVotes: []transaction.PhasedVote{
						{Vote: voting.VoteFromData([]byte("migrate objects")), Phase: voting.Prepared},
						{Vote: voting.VoteFromData([]byte("migrate objects")), Phase: voting.Committed},
						{Vote: voting.VoteFromData([]byte("disconnect alternate")), Phase: voting.Prepared},
						{Vote: voting.VoteFromData([]byte("disconnect alternate")), Phase: voting.Committed},
					},
				}
			},
		},
		{
			desc: "transactional disconnect successful without object pool",
			setup: func(t *testing.T, ctx context.Context) setupData {
				// If a repository is not linked to an object pool, the transaction should contain a
				// vote indicating there is no alternate for the repository.
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				repo := localrepo.NewTestRepo(t, cfg, repoProto)

				return setupData{
					repository: repo,
					txManager:  transaction.NewTrackingManager(),
					expectedVotes: []transaction.PhasedVote{
						{Vote: voting.VoteFromData([]byte("no alternates")), Phase: voting.Committed},
					},
				}
			},
		},
		{
			desc: "transactional disconnect fails",
			setup: func(t *testing.T, ctx context.Context) setupData {
				repo, _ := setupRepoWithObjectPool(t, ctx)

				// Simulate transaction failure to validate that rollback is performed.
				txManager := &transaction.MockManager{
					VoteFn: func(_ context.Context, _ txinfo.Transaction, _ voting.Vote, _ voting.Phase) error {
						// Return an error to simulate transaction failure.
						return errors.New("transaction failed")
					},
				}

				return setupData{
					repository:    repo,
					txManager:     txManager,
					expectedError: errors.New("preparatory vote for migrating objects: transaction failed"),
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t, ctx)

			repoPath, err := setup.repository.Path()
			require.NoError(t, err)

			altInfoBefore, err := stats.AlternatesInfoForRepository(repoPath)
			require.NoError(t, err)

			// If testcase uses transaction manager, inject transaction into context.
			ctx := ctx
			if setup.txManager != nil {
				ctx = peer.NewContext(ctx, &peer.Peer{})
				ctx, err = txinfo.InjectTransaction(ctx, 1, "primary", true)
				require.NoError(t, err)
				ctx = metadata.IncomingToOutgoing(ctx)
				ctx = testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))
			}

			disconnectErr := Disconnect(ctx, setup.repository, logger, setup.txManager)

			altInfoAfter, err := stats.AlternatesInfoForRepository(repoPath)
			require.NoError(t, err)

			if setup.expectedError != nil {
				require.ErrorContains(t, disconnectErr, setup.expectedError.Error())

				// If an error occurs, the Git alternates file should remain and be unchanged.
				require.Equal(t, altInfoBefore, altInfoAfter)
				return
			}
			require.NoError(t, disconnectErr)

			// Objects in the object pool are migrated to the repository and should be available.
			for _, oid := range setup.expectedObjects {
				gittest.RequireObjectExists(t, cfg, repoPath, oid)
			}

			// After the repository is disconnected from object pool, no alternates file should be
			// present in the repository.
			require.False(t, altInfoAfter.Exists)

			// The repository should be in a valid state after an object pool is disconnected.
			gittest.Exec(t, cfg, "-C", repoPath, "fsck")

			if setup.txManager != nil {
				trackingManager, ok := setup.txManager.(*transaction.TrackingManager)
				require.True(t, ok, "tracking manager required for validating votes")
				require.Equal(t, setup.expectedVotes, trackingManager.Votes())
			}
		})
	}
}

func TestRemoveAlternatesIfOk(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	assertAlternates := func(t *testing.T, altPath string, altContent string) {
		t.Helper()

		actualContent := testhelper.MustReadFile(t, altPath)

		require.Equal(t, altContent, string(actualContent), "%s content after fsck failure", altPath)
	}

	t.Run("pack files are missing", func(t *testing.T) {
		cfg := testcfg.Build(t)
		logger := testhelper.NewLogger(t)
		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{SkipCreationViaService: true})

		repo := localrepo.NewTestRepo(t, cfg, repoProto)
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
		gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad")

		// Change the alternates file to point to an empty directory. This is only done to
		// assert that we correctly restore the file if the repository doesn't pass the
		// consistency checks when trying to remove the alternates file.
		altPath, err := repo.InfoAlternatesPath()
		require.NoError(t, err)
		altContent := testhelper.TempDir(t) + "\n"
		require.NoError(t, os.WriteFile(altPath, []byte(altContent), perm.SharedFile))

		// Intentionally break the repository so that the consistency check will cause an
		// error.
		require.NoError(t, os.RemoveAll(filepath.Join(repoPath, "objects", "pack")))

		// Now we try to remove the alternates file. This is expected to fail due to the
		// consistency check.
		altBackup := altPath + ".backup"
		err = removeAlternatesIfOk(ctx, repo, altPath, altBackup, logger, nil)
		require.Error(t, err, "removeAlternatesIfOk should fail")
		require.IsType(t, &connectivityError{}, err, "error must be because of fsck")

		// We expect objects/info/alternates to have been restored when removeAlternatesIfOk
		// returned.
		assertAlternates(t, altPath, altContent)
		// We expect the backup alternates file to still exist.
		assertAlternates(t, altBackup, altContent)
	})

	t.Run("commit graph exists but object is missing from odb", func(t *testing.T) {
		cfg := testcfg.Build(t)
		logger := testhelper.NewLogger(t)
		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{SkipCreationViaService: true})

		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		altPath, err := repo.InfoAlternatesPath()
		require.NoError(t, err)
		altContent := testhelper.TempDir(t) + "\n"
		require.NoError(t, os.WriteFile(altPath, []byte(altContent), perm.SharedFile))

		// In order to test the scenario where a commit is in a commit graph but not in the
		// object database, we will first write a new commit, write the commit graph, then
		// remove that commit object from the object database.
		parentOID := gittest.WriteCommit(t, cfg, repoPath)
		commitOID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(parentOID), gittest.WithBranch("main"))
		gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write")

		// We now manually remove the object. It thus exists in the commit-graph, but not in
		// the ODB anymore while still being reachable. We should notice that the repository
		// is corrupted.
		require.NoError(t, os.Remove(filepath.Join(repoPath, "objects", string(commitOID)[0:2], string(commitOID)[2:])))

		// Now when we try to remove the alternates file we should notice the corruption and
		// abort.
		altBackup := altPath + ".backup"
		err = removeAlternatesIfOk(ctx, repo, altPath, altBackup, logger, nil)
		require.Error(t, err, "removeAlternatesIfOk should fail")

		var connectivityErr *connectivityError
		require.True(t, errors.As(err, &connectivityErr), "error must be because of connectivity check")
		var exitError *exec.ExitError
		require.True(t, errors.As(connectivityErr.error, &exitError), "error must be because of fsck")

		// We expect objects/info/alternates to have been restored when
		// removeAlternatesIfOk returned.
		assertAlternates(t, altPath, altContent)
		// We expect the backup alternates file to still exist.
		assertAlternates(t, altBackup, altContent)
	})
}
