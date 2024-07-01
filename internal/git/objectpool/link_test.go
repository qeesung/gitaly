package objectpool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"google.golang.org/grpc/peer"
)

func TestLink(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	requireHasBitmap := func(t *testing.T, repo *localrepo.Repo, expected bool) {
		packfilesInfo, err := stats.PackfilesInfoForRepository(ctx, repo)
		require.NoError(t, err)
		require.Equal(t, expected, packfilesInfo.Bitmap.Exists)
	}

	getRelAltPath := func(t *testing.T, repo, poolRepo *localrepo.Repo) string {
		relAltPath, err := filepath.Rel(
			filepath.Join(gittest.RepositoryPath(t, ctx, repo), "objects"),
			filepath.Join(gittest.RepositoryPath(t, ctx, poolRepo), "objects"),
		)
		require.NoError(t, err)

		return relAltPath
	}

	type setupData struct {
		cfg           config.Cfg
		repo          *localrepo.Repo
		pool          *ObjectPool
		txManager     transaction.Manager
		expectedVotes []transaction.PhasedVote
		expectedError error
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T, ctx context.Context) setupData
	}{
		{
			desc: "repository linked to object pool",
			setup: func(t *testing.T, ctx context.Context) setupData {
				cfg, pool, repo := setupObjectPool(t, ctx)

				// The repository is linked to the object pool via the Git alternates file. Prior to
				// linking, the repository should not contain an alternates file.
				altPath, err := repo.InfoAlternatesPath(ctx)
				require.NoError(t, err)
				require.NoFileExists(t, altPath)

				return setupData{
					cfg:  cfg,
					repo: repo,
					pool: pool,
				}
			},
		},
		{
			desc: "repository bitmap removed",
			setup: func(t *testing.T, ctx context.Context) setupData {
				cfg, pool, repo := setupObjectPool(t, ctx)
				poolPath := gittest.RepositoryPath(t, ctx, pool)
				repoPath := gittest.RepositoryPath(t, ctx, repo)

				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"))

				// Pull in all references from the repository.
				gittest.Exec(t, cfg, "-C", poolPath, "fetch", repoPath, "+refs/*:refs/*")

				// Repack both the object pool and the pool member such that they both have bitmaps.
				// The repository bitmap is expected to be removed after being successfully linked
				// to an object pool.
				gittest.Exec(t, cfg, "-C", poolPath, "repack", "-adb")
				requireHasBitmap(t, pool.Repo, true)
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-adb")
				requireHasBitmap(t, repo, true)

				return setupData{
					cfg:  cfg,
					repo: repo,
					pool: pool,
				}
			},
		},
		{
			desc: "rewrite absolute alternate path",
			setup: func(t *testing.T, ctx context.Context) setupData {
				cfg, pool, repo := setupObjectPool(t, ctx)

				altPath, err := repo.InfoAlternatesPath(ctx)
				require.NoError(t, err)

				// Link the repository to object pool using the absolute path of the object pool.
				// The alternates file should be rewritten to use the relative path.
				poolObjectsPath := gittest.RepositoryPath(t, ctx, pool, "objects")
				require.NoError(t, os.WriteFile(altPath, []byte(poolObjectsPath), perm.SharedFile))

				return setupData{
					cfg:  cfg,
					repo: repo,
					pool: pool,
				}
			},
		},
		{
			desc: "repository already linked",
			setup: func(t *testing.T, ctx context.Context) setupData {
				cfg, pool, repo := setupObjectPool(t, ctx)

				// When the repository is already linked to the object pool, the link operation does
				// nothing and completes normally.
				altPath, err := repo.InfoAlternatesPath(ctx)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(altPath, []byte(getRelAltPath(t, repo, pool.Repo)), perm.SharedFile))

				return setupData{
					cfg:  cfg,
					repo: repo,
					pool: pool,
				}
			},
		},
		{
			desc: "repository linked to different object pool",
			setup: func(t *testing.T, ctx context.Context) setupData {
				cfg, pool, repo := setupObjectPool(t, ctx)

				// If the repository alternates file already references a different object pool, the
				// linking operation fails.
				altPath, err := repo.InfoAlternatesPath(ctx)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(altPath, []byte("../different/object/pool"), perm.SharedFile))

				return setupData{
					cfg:           cfg,
					repo:          repo,
					pool:          pool,
					expectedError: errors.New("unexpected alternates content: \"../different/object/pool\""),
				}
			},
		},
		{
			desc: "transactional repository link",
			setup: func(t *testing.T, ctx context.Context) setupData {
				cfg, pool, repo := setupObjectPool(t, ctx)

				// Inject transaction manager to record transactions.
				txManager := transaction.NewTrackingManager()
				pool.txManager = txManager

				// When transactions are enabled, the contents of the alternate file are voted on.
				expectedVote := voting.VoteFromData([]byte(getRelAltPath(t, repo, pool.Repo)))

				return setupData{
					cfg:       cfg,
					repo:      repo,
					pool:      pool,
					txManager: txManager,
					expectedVotes: []transaction.PhasedVote{
						{Vote: expectedVote, Phase: voting.Prepared},
						{Vote: expectedVote, Phase: voting.Committed},
					},
				}
			},
		},
		{
			desc: "transactional repository already linked",
			setup: func(t *testing.T, ctx context.Context) setupData {
				cfg, pool, repo := setupObjectPool(t, ctx)

				// Inject transaction manager to record transactions.
				txManager := transaction.NewTrackingManager()
				pool.txManager = txManager

				// When transactions are enabled, a vote is cast if the repository is already linked
				// to the same object pool.
				altPath, err := repo.InfoAlternatesPath(ctx)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(altPath, []byte(getRelAltPath(t, repo, pool.Repo)), perm.SharedFile))

				return setupData{
					cfg:       cfg,
					repo:      repo,
					pool:      pool,
					txManager: txManager,
					expectedVotes: []transaction.PhasedVote{
						{Vote: voting.VoteFromData([]byte("repository linked")), Phase: voting.Committed},
					},
				}
			},
		},
		{
			desc: "repository link transaction fails",
			setup: func(t *testing.T, ctx context.Context) setupData {
				cfg, pool, repo := setupObjectPool(t, ctx)

				// Simulate transaction failure to validate that error is returned.
				txManager := &transaction.MockManager{
					VoteFn: func(context.Context, txinfo.Transaction, voting.Vote, voting.Phase) error {
						return errors.New("transaction failed")
					},
				}
				pool.txManager = txManager

				poolPath := gittest.RepositoryPath(t, ctx, pool)
				repoPath := gittest.RepositoryPath(t, ctx, repo)

				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"))

				// Pull in all references from the repository.
				gittest.Exec(t, cfg, "-C", poolPath, "fetch", repoPath, "+refs/*:refs/*")

				// Repack both the object pool and the pool member such that they both have bitmaps.
				// When a transaction fails, the bitmaps are expected to remain intact.
				gittest.Exec(t, cfg, "-C", poolPath, "repack", "-adb")
				requireHasBitmap(t, pool.Repo, true)
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-adb")
				requireHasBitmap(t, repo, true)

				return setupData{
					cfg:           cfg,
					repo:          repo,
					pool:          pool,
					txManager:     txManager,
					expectedError: errors.New("committing alternates: voting on locked file: preimage vote: transaction failed"),
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t, ctx)

			repoPath := gittest.RepositoryPath(t, ctx, setup.repo)

			// Capture the Git alternates file state in the repository before performing the link.
			altInfoBefore, err := stats.AlternatesInfoForRepository(repoPath)
			require.NoError(t, err)

			// Check if the repository and pool repository has a bitmap. Pool repository bitmaps are
			// expected to remain unchanged.
			repoPackInfo, err := stats.PackfilesInfoForRepository(ctx, setup.repo)
			require.NoError(t, err)
			poolPackInfo, err := stats.PackfilesInfoForRepository(ctx, setup.pool.Repo)
			require.NoError(t, err)

			// If the testcase uses transaction manager, inject transaction into context.
			ctx := ctx
			if setup.txManager != nil {
				ctx = peer.NewContext(ctx, &peer.Peer{
					AuthInfo: backchannel.WithID(nil, 1234),
				})
				ctx, err = txinfo.InjectTransaction(ctx, 1, "node", true)
				require.NoError(t, err)
			}

			// After a successful link, the repository is expected to have an alternates file which
			// references the linked object pool.
			expectedAltInfo := stats.AlternatesInfo{
				Exists:            true,
				ObjectDirectories: []string{getRelAltPath(t, setup.repo, setup.pool.Repo)},
			}

			// After successfully linking the repository to its pool it should not have a bitmap
			// anymore as Git does not allow for multiple bitmaps to exist.
			expectedRepoBitmap := false

			err = setup.pool.Link(ctx, setup.repo)
			if setup.expectedError != nil {
				require.ErrorContains(t, err, setup.expectedError.Error())

				// If object pool linking fails, the Git alternate file state of the repository is
				// expected to remain unchanged.
				expectedAltInfo = altInfoBefore
				expectedRepoBitmap = repoPackInfo.Bitmap.Exists
			} else {
				require.NoError(t, err)
			}

			// Validate the state of the repository Git alternates file after the link is performed.
			altInfoAfter, err := stats.AlternatesInfoForRepository(repoPath)
			require.NoError(t, err)
			require.Equal(t, expectedAltInfo.Exists, altInfoAfter.Exists)
			require.Equal(t, expectedAltInfo.ObjectDirectories, altInfoAfter.ObjectDirectories)

			// An alternates file is expected to contain a single relative path.
			if altInfoAfter.Exists {
				require.Len(t, altInfoAfter.ObjectDirectories, 1)
				require.True(t,
					strings.HasPrefix(altInfoAfter.ObjectDirectories[0], "../"),
					"expected %q to be relative path", altInfoAfter.ObjectDirectories[0],
				)
			}

			// Validate bitmap state for repository and object pool. Bitmaps in the pool repository
			// should match the state prior to repository link.
			requireHasBitmap(t, setup.repo, expectedRepoBitmap)
			requireHasBitmap(t, setup.pool.Repo, poolPackInfo.Bitmap.Exists)

			require.Empty(t, gittest.Exec(t, setup.cfg, "-C", gittest.RepositoryPath(t, ctx, setup.pool), "remote"))

			// Sanity-check that the repository is still consistent.
			gittest.Exec(t, setup.cfg, "-C", repoPath, "fsck")

			// Validate that expected transaction votes are received.
			var votes []transaction.PhasedVote
			if trackingManager, ok := setup.txManager.(*transaction.TrackingManager); ok {
				votes = trackingManager.Votes()
			}
			require.Equal(t, setup.expectedVotes, votes)
		})
	}
}
