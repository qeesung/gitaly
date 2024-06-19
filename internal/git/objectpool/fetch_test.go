package objectpool

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/keyvalue"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestFetchFromOrigin_dangling(t *testing.T) {
	testWithAndWithoutTransaction(t, func(t *testing.T, cfg config.Cfg, newLocalRepo LocalRepoFactory) {
		ctx := testhelper.Context(t)

		pool, repo := setupObjectPoolWithCfg(t, ctx, cfg)
		poolPath := gittest.RepositoryPath(t, pool)
		repoPath := gittest.RepositoryPath(t, repo)

		// Write some reachable objects into the object pool member and fetch them into the pool.
		blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("contents"))
		treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
			{Mode: "100644", OID: blobID, Path: "reachable"},
		})
		commitID := gittest.WriteCommit(t, cfg, repoPath,
			gittest.WithTree(treeID),
			gittest.WithBranch("master"),
		)
		require.NoError(t, pool.FetchFromOrigin(ctx, repo, newLocalRepo))

		// We now write a bunch of objects into the object pool that are not referenced by anything.
		// These are thus "dangling".
		unreachableBlob := gittest.WriteBlob(t, cfg, poolPath, []byte("unreachable"))
		unreachableTree := gittest.WriteTree(t, cfg, poolPath, []gittest.TreeEntry{
			{Mode: "100644", OID: blobID, Path: "unreachable"},
		})
		unreachableCommit := gittest.WriteCommit(t, cfg, poolPath,
			gittest.WithMessage("unreachable"),
			gittest.WithTree(treeID),
		)
		unreachableTag := gittest.WriteTag(t, cfg, poolPath, "unreachable", commitID.Revision(), gittest.WriteTagConfig{
			Message: "unreachable",
		})
		// `WriteTag()` automatically creates a reference and thus makes the annotated tag
		// reachable. We thus delete the reference here again.
		gittest.Exec(t, cfg, "-C", poolPath, "update-ref", "-d", "refs/tags/unreachable")

		// git-fsck(1) should report the newly created unreachable objects as dangling.
		fsckBefore := gittest.Exec(t, cfg, "-C", poolPath, "fsck", "--connectivity-only", "--dangling")
		require.ElementsMatch(t, []string{
			fmt.Sprintf("dangling blob %s", unreachableBlob),
			fmt.Sprintf("dangling tag %s", unreachableTag),
			fmt.Sprintf("dangling commit %s", unreachableCommit),
			fmt.Sprintf("dangling tree %s", unreachableTree),
		}, strings.Split(text.ChompBytes(fsckBefore), "\n"))

		// We expect this second run to convert the dangling objects into non-dangling objects.
		require.NoError(t, pool.FetchFromOrigin(ctx, repo, newLocalRepo))

		// Each of the dangling objects should have gotten a new dangling reference.
		danglingRefs := gittest.Exec(t, cfg, "-C", poolPath, "for-each-ref", "--format=%(refname) %(objectname)", "refs/dangling/")
		require.ElementsMatch(t, []string{
			fmt.Sprintf("refs/dangling/%[1]s %[1]s", unreachableBlob),
			fmt.Sprintf("refs/dangling/%[1]s %[1]s", unreachableTree),
			fmt.Sprintf("refs/dangling/%[1]s %[1]s", unreachableTag),
			fmt.Sprintf("refs/dangling/%[1]s %[1]s", unreachableCommit),
		}, strings.Split(text.ChompBytes(danglingRefs), "\n"))
		// And git-fsck(1) shouldn't report the objects as dangling anymore.
		require.Empty(t, gittest.Exec(t, cfg, "-C", poolPath, "fsck", "--connectivity-only", "--dangling"))
	})
}

func TestFetchFromOrigin_fsck(t *testing.T) {
	testWithAndWithoutTransaction(t, func(t *testing.T, cfg config.Cfg, newLocalRepo LocalRepoFactory) {
		ctx := testhelper.Context(t)
		pool, repo := setupObjectPoolWithCfg(t, ctx, cfg)
		repoPath, err := repo.Path()
		require.NoError(t, err)

		require.NoError(t, pool.FetchFromOrigin(ctx, repo, newLocalRepo), "seed pool")

		// We're creating a new commit which has a root tree with duplicate entries. git-mktree(1)
		// allows us to create these trees just fine, but git-fsck(1) complains.
		gittest.WriteCommit(t, cfg, repoPath,
			gittest.WithTreeEntries(
				gittest.TreeEntry{OID: gittest.DefaultObjectHash.EmptyTreeOID, Path: "dup", Mode: "040000"},
				gittest.TreeEntry{OID: gittest.DefaultObjectHash.EmptyTreeOID, Path: "dup", Mode: "040000"},
			),
			gittest.WithBranch("branch"),
		)

		err = pool.FetchFromOrigin(ctx, repo, newLocalRepo)
		require.Error(t, err)
		require.Contains(t, err.Error(), "duplicateEntries: contains duplicate file entries")
	})
}

func TestFetchFromOrigin_deltaIslands(t *testing.T) {
	testWithAndWithoutTransaction(t, func(t *testing.T, cfg config.Cfg, newLocalRepo LocalRepoFactory) {
		ctx := testhelper.Context(t)
		pool, repo := setupObjectPoolWithCfg(t, ctx, cfg)
		poolPath := gittest.RepositoryPath(t, pool)
		repoPath := gittest.RepositoryPath(t, repo)

		require.NoError(t, pool.FetchFromOrigin(ctx, repo, newLocalRepo), "seed pool")
		require.NoError(t, pool.Link(ctx, repo))

		// With multi-pack-indices we don't do full repacks of repositories that
		// aggressively anymore, but in order to test delta islands we need to trigger one.
		// We thus write a second packfile so that `OptimizeRepository()` decides to
		// rewrite packfiles.
		gittest.WriteCommit(t, cfg, poolPath, gittest.WithBranch("irrelevant"))
		gittest.Exec(t, cfg, "-C", poolPath, "repack")

		// The setup of delta islands is done in the normal repository, and thus we pass `false`
		// for `isPoolRepo`. Verification whether we correctly handle repacking though happens in
		// the pool repository.
		gittest.TestDeltaIslands(t, cfg, repoPath, poolPath, false, func() error {
			return pool.FetchFromOrigin(ctx, repo, newLocalRepo)
		})
	})
}

func TestFetchFromOrigin_bitmapHashCache(t *testing.T) {
	testWithAndWithoutTransaction(t, func(t *testing.T, cfg config.Cfg, newLocalRepo LocalRepoFactory) {
		ctx := testhelper.Context(t)
		pool, repo := setupObjectPoolWithCfg(t, ctx, cfg)
		repoPath, err := repo.Path()
		require.NoError(t, err)

		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"))

		require.NoError(t, pool.FetchFromOrigin(ctx, repo, newLocalRepo))

		bitmaps, err := filepath.Glob(gittest.RepositoryPath(t, pool, "objects", "pack", "*.bitmap"))
		require.NoError(t, err)
		require.Len(t, bitmaps, 1)

		bitmapInfo, err := stats.BitmapInfoForPath(bitmaps[0])
		require.NoError(t, err)
		require.Equal(t, stats.BitmapInfo{
			Exists:         true,
			Version:        1,
			HasHashCache:   true,
			HasLookupTable: true,
		}, bitmapInfo)
	})
}

func TestFetchFromOrigin_refUpdates(t *testing.T) {
	testWithAndWithoutTransaction(t, func(t *testing.T, cfg config.Cfg, newLocalRepo LocalRepoFactory) {
		ctx := testhelper.Context(t)
		pool, repo := setupObjectPoolWithCfg(t, ctx, cfg)
		repoPath, err := repo.Path()
		require.NoError(t, err)

		poolPath := gittest.RepositoryPath(t, pool)

		// Seed the pool member with some preliminary data.
		oldRefs := map[string]git.ObjectID{}
		oldRefs["heads/csv"] = gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("csv"), gittest.WithMessage("old"))
		oldRefs["tags/v1.1.0"] = gittest.WriteTag(t, cfg, repoPath, "v1.1.0", oldRefs["heads/csv"].Revision())

		// We now fetch that data into the object pool and verify that it exists as expected.
		require.NoError(t, pool.FetchFromOrigin(ctx, repo, newLocalRepo))
		for ref, oid := range oldRefs {
			require.Equal(t, oid, gittest.ResolveRevision(t, cfg, poolPath, "refs/remotes/origin/"+ref))
		}

		// Next, we force-overwrite both old references with new objects.
		newRefs := map[string]git.ObjectID{}
		newRefs["heads/csv"] = gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("csv"), gittest.WithMessage("new"))
		newRefs["tags/v1.1.0"] = gittest.WriteTag(t, cfg, repoPath, "v1.1.0", newRefs["heads/csv"].Revision(), gittest.WriteTagConfig{
			Force: true,
		})

		// Create a bunch of additional references. This is to trigger OptimizeRepository to indeed
		// repack the loose references as we expect it to in this test. It's debatable whether we
		// should test this at all here given that this is business of the housekeeping package. But
		// it's easy enough to do, so it doesn't hurt.
		for i := 0; i < 32; i++ {
			branchName := fmt.Sprintf("branch-%d", i)
			newRefs["heads/"+branchName] = gittest.WriteCommit(t, cfg, repoPath,
				gittest.WithMessage(strconv.Itoa(i)),
				gittest.WithBranch(branchName),
			)
		}

		// Now we fetch again and verify that all references should have been updated accordingly.
		require.NoError(t, pool.FetchFromOrigin(ctx, repo, newLocalRepo))
		for ref, oid := range newRefs {
			require.Equal(t, oid, gittest.ResolveRevision(t, cfg, poolPath, "refs/remotes/origin/"+ref))
		}
	})
}

func TestFetchFromOrigin_refs(t *testing.T) {
	testWithAndWithoutTransaction(t, func(t *testing.T, cfg config.Cfg, newLocalRepo LocalRepoFactory) {
		ctx := testhelper.Context(t)
		pool, repo := setupObjectPoolWithCfg(t, ctx, cfg)
		repoPath, err := repo.Path()
		require.NoError(t, err)

		// Verify that the object pool ain't yet got any references.
		poolPath := gittest.RepositoryPath(t, pool)
		require.Empty(t, gittest.Exec(t, cfg, "-C", poolPath, "for-each-ref", "--format=%(refname)"))

		// Initialize the repository with a bunch of references.
		commitID := gittest.WriteCommit(t, cfg, repoPath)
		for _, ref := range []git.ReferenceName{"refs/heads/master", "refs/environments/1", "refs/tags/lightweight-tag"} {
			gittest.WriteRef(t, cfg, repoPath, ref, commitID)
		}
		gittest.WriteTag(t, cfg, repoPath, "annotated-tag", commitID.Revision(), gittest.WriteTagConfig{
			Message: "tag message",
		})

		// Fetch from the pool member. This should pull in all references we have just created in
		// that repository into the pool.
		require.NoError(t, pool.FetchFromOrigin(ctx, repo, newLocalRepo))
		require.Equal(t,
			[]string{
				"refs/remotes/origin/environments/1",
				"refs/remotes/origin/heads/master",
				"refs/remotes/origin/tags/annotated-tag",
				"refs/remotes/origin/tags/lightweight-tag",
			},
			strings.Split(text.ChompBytes(gittest.Exec(t, cfg, "-C", poolPath, "for-each-ref", "--format=%(refname)")), "\n"),
		)

		// We don't want to see "FETCH_HEAD" though: it's useless and may take quite some time to
		// write out in Git.
		require.NoFileExists(t, filepath.Join(poolPath, "FETCH_HEAD"))
	})
}

func TestFetchFromOrigin_missingPool(t *testing.T) {
	testWithAndWithoutTransaction(t, func(t *testing.T, cfg config.Cfg, newLocalRepo LocalRepoFactory) {
		ctx := testhelper.Context(t)
		pool, repo := setupObjectPoolWithCfg(t, ctx, cfg)

		// Remove the object pool to assert that we raise an error when fetching into a non-existent
		// object pool.
		require.NoError(t, pool.Remove(ctx))

		require.Equal(t, structerr.NewInvalidArgument("object pool does not exist"), pool.FetchFromOrigin(ctx, repo, newLocalRepo))
		require.False(t, pool.Exists())
	})
}

func TestObjectPool_logStats(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	type setupData struct {
		pool   *ObjectPool
		fields log.Fields
	}

	for _, tc := range []struct {
		desc           string
		setup          func(t *testing.T, logger log.Logger) setupData
		expectedFields log.Fields
	}{
		{
			desc: "empty object pool",
			setup: func(t *testing.T, logger log.Logger) setupData {
				_, pool, _ := setupObjectPool(t, ctx, withLogger(logger))
				return setupData{
					pool: pool,
					fields: log.Fields{
						"when":                "now",
						"references.dangling": referencedObjectTypes{},
						"references.normal":   referencedObjectTypes{},
						"repository_info": stats.RepositoryInfo{
							IsObjectPool: true,
							References: stats.ReferencesInfo{
								ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
								ReftableTables: gittest.FilesOrReftables(
									nil,
									[]stats.ReftableTable{
										{
											Size:           124,
											UpdateIndexMin: 1,
											UpdateIndexMax: 1,
										},
									}),
							},
						},
					},
				}
			},
		},
		{
			desc: "normal reference",
			setup: func(t *testing.T, logger log.Logger) setupData {
				cfg, pool, _ := setupObjectPool(t, ctx, withLogger(logger))
				gittest.WriteCommit(t, cfg, gittest.RepositoryPath(t, pool), gittest.WithBranch("main"))

				return setupData{
					pool: pool,
					fields: log.Fields{
						"when":                "now",
						"references.dangling": referencedObjectTypes{},
						"references.normal": referencedObjectTypes{
							Commits: 1,
						},
						"repository_info": stats.RepositoryInfo{
							IsObjectPool: true,
							LooseObjects: stats.LooseObjectsInfo{
								Count: 2,
								Size:  hashDependentSize(t, 142, 158),
							},
							References: stats.ReferencesInfo{
								ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
								LooseReferencesCount: gittest.FilesOrReftables[uint64](1, 0),
								ReftableTables: gittest.FilesOrReftables(
									nil,
									[]stats.ReftableTable{
										{
											Size:           165,
											UpdateIndexMin: 1,
											UpdateIndexMax: 2,
										},
									}),
							},
						},
					},
				}
			},
		},
		{
			desc: "dangling reference",
			setup: func(t *testing.T, logger log.Logger) setupData {
				cfg, pool, _ := setupObjectPool(t, ctx, withLogger(logger))
				gittest.WriteCommit(t, cfg, gittest.RepositoryPath(t, pool), gittest.WithReference("refs/dangling/commit"))

				return setupData{
					pool: pool,
					fields: log.Fields{
						"when": "now",
						"references.dangling": referencedObjectTypes{
							Commits: 1,
						},
						"references.normal": referencedObjectTypes{},
						"repository_info": stats.RepositoryInfo{
							IsObjectPool: true,
							LooseObjects: stats.LooseObjectsInfo{
								Count: 2,
								Size:  hashDependentSize(t, 142, 158),
							},
							References: stats.ReferencesInfo{
								ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
								LooseReferencesCount: gittest.FilesOrReftables[uint64](1, 0),
								ReftableTables: gittest.FilesOrReftables(
									nil,
									[]stats.ReftableTable{
										{
											Size:           171,
											UpdateIndexMin: 1,
											UpdateIndexMax: 2,
										},
									}),
							},
						},
					},
				}
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			logger := testhelper.NewLogger(t)
			hook := testhelper.AddLoggerHook(logger)
			setup := tc.setup(t, logger)

			require.NoError(t, setup.pool.logStats(ctx, "now"))

			logEntries := hook.AllEntries()
			require.Len(t, logEntries, 1)
			require.Equal(t, "pool dangling ref stats", logEntries[0].Message)
			require.Equal(t, setup.fields, logEntries[0].Data)
		})
	}
}

func testWithAndWithoutTransaction(t *testing.T, testFunc func(*testing.T, config.Cfg, LocalRepoFactory)) {
	t.Helper()

	t.Run("with transaction", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)

		logger := testhelper.SharedLogger(t)
		cmdFactory := gittest.NewCommandFactory(t, cfg)
		catfileCache := catfile.NewCache(cfg)
		t.Cleanup(catfileCache.Stop)

		dbMgr, err := keyvalue.NewDBManager(cfg.Storages, keyvalue.NewBadgerStore, helper.NewNullTickerFactory(), logger)
		require.NoError(t, err)
		defer dbMgr.Close()

		localRepoFactory := localrepo.NewFactory(logger, config.NewLocator(cfg), cmdFactory, catfileCache)
		partitionManager, err := storagemgr.NewPartitionManager(
			cfg.Storages,
			cmdFactory,
			localRepoFactory,
			logger,
			dbMgr,
			cfg.Prometheus,
			nil,
		)
		require.NoError(t, err)
		defer partitionManager.Close()

		testFunc(t, cfg, localRepoFactory.Build)
	})

	t.Run("without transaction", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		testFunc(t, cfg, nil)
	})
}
