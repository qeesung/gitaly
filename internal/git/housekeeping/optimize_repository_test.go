//go:build !gitaly_test_sha256

package housekeeping

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestNeedsRepacking(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)

	for _, tc := range []struct {
		desc           string
		setup          func(t *testing.T, relativePath string) *gitalypb.Repository
		expectedErr    error
		expectedNeeded bool
		expectedConfig RepackObjectsConfig
	}{
		{
			desc: "empty repo does nothing",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repoProto, _ := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
					WithRelativePath: relativePath,
				})
				return repoProto
			},
		},
		{
			desc: "missing bitmap",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repoProto, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
					RelativePath: relativePath,
				})
				return repoProto
			},
			expectedNeeded: true,
			expectedConfig: RepackObjectsConfig{
				FullRepack:  true,
				WriteBitmap: true,
			},
		},
		{
			desc: "missing bitmap with alternate",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repoProto, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
					RelativePath: relativePath,
				})

				// Create the alternates file. If it exists, then we shouldn't try
				// to generate a bitmap.
				require.NoError(t, os.WriteFile(filepath.Join(repoPath, "objects", "info", "alternates"), nil, 0o755))

				return repoProto
			},
			// If we have no bitmap in the repository we'd normally want to fully repack
			// the repository. But because we have an alternates file we know that the
			// repository must not have a bitmap anyway, so we can skip the repack here.
			expectedNeeded: false,
		},
		{
			desc: "missing commit-graph",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repoProto, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
					RelativePath: relativePath,
				})

				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad", "--write-bitmap-index")

				return repoProto
			},
			// The commit-graph used to influence whether we repacked a repository or
			// not. That was due to historic reasons only, though, and ultimately does
			// not make any sense.
			expectedNeeded: false,
		},
		{
			desc: "commit-graph without bloom filters",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repoProto, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
					RelativePath: relativePath,
				})

				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write")

				return repoProto
			},
			// The commit-graph used to influence whether we repacked a repository or
			// not. That was due to historic reasons only, though, and ultimately does
			// not make any sense.
			expectedNeeded: false,
		},
		{
			desc: "no repack needed",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repoProto, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
					RelativePath: relativePath,
				})

				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--changed-paths", "--split")

				return repoProto
			},
			expectedNeeded: false,
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			testRepoAndPool(t, tc.desc, func(t *testing.T, relativePath string) {
				repoProto := tc.setup(t, relativePath)
				repo := localrepo.NewTestRepo(t, cfg, repoProto)

				repackNeeded, repackCfg, err := needsRepacking(repo)
				require.Equal(t, tc.expectedErr, err)
				require.Equal(t, tc.expectedNeeded, repackNeeded)
				require.Equal(t, tc.expectedConfig, repackCfg)
			})
		})
	}

	const megaByte = 1024 * 1024

	for _, tc := range []struct {
		packfileSize             int64
		requiredPackfiles        int
		requiredPackfilesForPool int
	}{
		{
			packfileSize:             1,
			requiredPackfiles:        5,
			requiredPackfilesForPool: 2,
		},
		{
			packfileSize:             5 * megaByte,
			requiredPackfiles:        6,
			requiredPackfilesForPool: 2,
		},
		{
			packfileSize:             10 * megaByte,
			requiredPackfiles:        8,
			requiredPackfilesForPool: 2,
		},
		{
			packfileSize:             50 * megaByte,
			requiredPackfiles:        14,
			requiredPackfilesForPool: 2,
		},
		{
			packfileSize:             100 * megaByte,
			requiredPackfiles:        17,
			requiredPackfilesForPool: 2,
		},
		{
			packfileSize:             500 * megaByte,
			requiredPackfiles:        23,
			requiredPackfilesForPool: 2,
		},
		{
			packfileSize:             1001 * megaByte,
			requiredPackfiles:        26,
			requiredPackfilesForPool: 3,
		},
		// Let's not go any further than this, we're thrashing the temporary directory.
	} {
		testRepoAndPool(t, fmt.Sprintf("packfile with %d bytes", tc.packfileSize), func(t *testing.T, relativePath string) {
			repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
				WithRelativePath: relativePath,
			})
			repo := localrepo.NewTestRepo(t, cfg, repoProto)
			packDir := filepath.Join(repoPath, "objects", "pack")

			// Emulate the existence of a bitmap and a commit-graph with bloom filters.
			// We explicitly don't want to generate them via Git commands as they would
			// require us to already have objects in the repository, and we want to be
			// in full control over all objects and packfiles in the repo.
			require.NoError(t, os.WriteFile(filepath.Join(packDir, "something.bitmap"), nil, 0o644))
			commitGraphChainPath := filepath.Join(repoPath, stats.CommitGraphChainRelPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(commitGraphChainPath), 0o755))
			require.NoError(t, os.WriteFile(commitGraphChainPath, nil, 0o644))

			// We first create a single big packfile which is used to determine the
			// boundary of when we repack.
			bigPackPath := filepath.Join(packDir, "big.pack")
			require.NoError(t, os.WriteFile(bigPackPath, nil, 0o644))
			require.NoError(t, os.Truncate(bigPackPath, tc.packfileSize))

			requiredPackfiles := tc.requiredPackfiles
			if IsPoolRepository(repoProto) {
				requiredPackfiles = tc.requiredPackfilesForPool
			}

			// And then we create one less packfile than we need to hit the boundary.
			// This is done to assert that we indeed don't repack before hitting the
			// boundary.
			for i := 0; i < requiredPackfiles-2; i++ {
				additionalPackfile, err := os.Create(filepath.Join(packDir, fmt.Sprintf("%d.pack", i)))
				require.NoError(t, err)
				testhelper.MustClose(t, additionalPackfile)
			}

			repackNeeded, _, err := needsRepacking(repo)
			require.NoError(t, err)
			require.False(t, repackNeeded)

			// Now we create the additional packfile that causes us to hit the boundary.
			// We should thus see that we want to repack now.
			lastPackfile, err := os.Create(filepath.Join(packDir, "last.pack"))
			require.NoError(t, err)
			testhelper.MustClose(t, lastPackfile)

			repackNeeded, repackCfg, err := needsRepacking(repo)
			require.NoError(t, err)
			require.True(t, repackNeeded)
			require.Equal(t, RepackObjectsConfig{
				FullRepack:  true,
				WriteBitmap: true,
			}, repackCfg)
		})
	}

	for _, tc := range []struct {
		desc           string
		looseObjects   []string
		expectedRepack bool
	}{
		{
			desc:           "no objects",
			looseObjects:   nil,
			expectedRepack: false,
		},
		{
			desc: "object not in 17 shard",
			looseObjects: []string{
				filepath.Join("ab/12345"),
			},
			expectedRepack: false,
		},
		{
			desc: "object in 17 shard",
			looseObjects: []string{
				filepath.Join("17/12345"),
			},
			expectedRepack: false,
		},
		{
			desc: "objects in different shards",
			looseObjects: []string{
				filepath.Join("ab/12345"),
				filepath.Join("cd/12345"),
				filepath.Join("12/12345"),
				filepath.Join("17/12345"),
			},
			expectedRepack: false,
		},
		{
			desc: "boundary",
			looseObjects: []string{
				filepath.Join("17/1"),
				filepath.Join("17/2"),
				filepath.Join("17/3"),
				filepath.Join("17/4"),
			},
			expectedRepack: false,
		},
		{
			desc: "exceeding boundary should cause repack",
			looseObjects: []string{
				filepath.Join("17/1"),
				filepath.Join("17/2"),
				filepath.Join("17/3"),
				filepath.Join("17/4"),
				filepath.Join("17/5"),
			},
			expectedRepack: true,
		},
	} {
		testRepoAndPool(t, tc.desc, func(t *testing.T, relativePath string) {
			repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
				WithRelativePath: relativePath,
			})
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			// Emulate the existence of a bitmap and a commit-graph with bloom filters.
			// We explicitly don't want to generate them via Git commands as they would
			// require us to already have objects in the repository, and we want to be
			// in full control over all objects and packfiles in the repo.
			require.NoError(t, os.WriteFile(filepath.Join(repoPath, "objects", "pack", "something.bitmap"), nil, 0o644))
			commitGraphChainPath := filepath.Join(repoPath, stats.CommitGraphChainRelPath)
			require.NoError(t, os.MkdirAll(filepath.Dir(commitGraphChainPath), 0o755))
			require.NoError(t, os.WriteFile(commitGraphChainPath, nil, 0o644))

			for _, looseObjectPath := range tc.looseObjects {
				looseObjectPath := filepath.Join(repoPath, "objects", looseObjectPath)
				require.NoError(t, os.MkdirAll(filepath.Dir(looseObjectPath), 0o755))

				looseObjectFile, err := os.Create(looseObjectPath)
				require.NoError(t, err)
				testhelper.MustClose(t, looseObjectFile)
			}

			repackNeeded, repackCfg, err := needsRepacking(repo)
			require.NoError(t, err)
			require.Equal(t, tc.expectedRepack, repackNeeded)
			if tc.expectedRepack {
				require.Equal(t, RepackObjectsConfig{
					FullRepack:  false,
					WriteBitmap: false,
				}, repackCfg)
			}
		})
	}
}

func TestPackRefsIfNeeded(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	const kiloByte = 1024

	for _, tc := range []struct {
		packedRefsSize int64
		requiredRefs   int
	}{
		{
			packedRefsSize: 1,
			requiredRefs:   16,
		},
		{
			packedRefsSize: 1 * kiloByte,
			requiredRefs:   16,
		},
		{
			packedRefsSize: 10 * kiloByte,
			requiredRefs:   33,
		},
		{
			packedRefsSize: 100 * kiloByte,
			requiredRefs:   49,
		},
		{
			packedRefsSize: 1000 * kiloByte,
			requiredRefs:   66,
		},
		{
			packedRefsSize: 10000 * kiloByte,
			requiredRefs:   82,
		},
		{
			packedRefsSize: 100000 * kiloByte,
			requiredRefs:   99,
		},
	} {
		testRepoAndPool(t, fmt.Sprintf("packed-refs with %d bytes", tc.packedRefsSize), func(t *testing.T, relativePath string) {
			repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
				WithRelativePath: relativePath,
			})
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			// Write an empty commit such that we can create valid refs.
			commitID := gittest.WriteCommit(t, cfg, repoPath)
			looseRefContent := []byte(commitID.String() + "\n")

			// We first create a single big packfile which is used to determine the
			// boundary of when we repack. We need to write a valid packed-refs file or
			// otherwise git-pack-refs(1) would choke later on, so we just write the
			// file such that every line is a separate ref of exactly 128 bytes in
			// length (a divisor of 1024), referring to the commit we created above.
			packedRefs, err := os.OpenFile(filepath.Join(repoPath, "packed-refs"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			require.NoError(t, err)
			defer testhelper.MustClose(t, packedRefs)
			for i := int64(0); i < tc.packedRefsSize/128; i++ {
				packedRefLine := fmt.Sprintf("%s refs/something/this-line-is-padded-to-exactly-128-bytes-%030d\n", commitID.String(), i)
				require.Len(t, packedRefLine, 128)
				_, err := packedRefs.WriteString(packedRefLine)
				require.NoError(t, err)
			}
			require.NoError(t, packedRefs.Sync())

			// And then we create one less loose ref than we need to hit the boundary.
			// This is done to assert that we indeed don't repack before hitting the
			// boundary.
			for i := 0; i < tc.requiredRefs-1; i++ {
				looseRefPath := filepath.Join(repoPath, "refs", "heads", fmt.Sprintf("branch-%d", i))
				require.NoError(t, os.WriteFile(looseRefPath, looseRefContent, 0o644))
			}

			didRepack, err := packRefsIfNeeded(ctx, repo)
			require.NoError(t, err)
			require.False(t, didRepack)

			// Now we create the additional loose ref that causes us to hit the
			// boundary. We should thus see that we want to repack now.
			looseRefPath := filepath.Join(repoPath, "refs", "heads", "last-branch")
			require.NoError(t, os.WriteFile(looseRefPath, looseRefContent, 0o644))

			didRepack, err = packRefsIfNeeded(ctx, repo)
			require.NoError(t, err)
			require.True(t, didRepack)
		})
	}
}

func TestEstimateLooseObjectCount(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	t.Run("empty repository", func(t *testing.T) {
		looseObjects, err := estimateLooseObjectCount(repo, time.Now())
		require.NoError(t, err)
		require.Zero(t, looseObjects)
	})

	t.Run("object in different shard", func(t *testing.T) {
		differentShard := filepath.Join(repoPath, "objects", "a0")
		require.NoError(t, os.MkdirAll(differentShard, 0o755))

		object, err := os.Create(filepath.Join(differentShard, "123456"))
		require.NoError(t, err)
		testhelper.MustClose(t, object)

		looseObjects, err := estimateLooseObjectCount(repo, time.Now())
		require.NoError(t, err)
		require.Zero(t, looseObjects)
	})

	t.Run("object in estimation shard", func(t *testing.T) {
		estimationShard := filepath.Join(repoPath, "objects", "17")
		require.NoError(t, os.MkdirAll(estimationShard, 0o755))

		object, err := os.Create(filepath.Join(estimationShard, "123456"))
		require.NoError(t, err)
		testhelper.MustClose(t, object)

		looseObjects, err := estimateLooseObjectCount(repo, time.Now())
		require.NoError(t, err)
		require.Equal(t, int64(256), looseObjects)

		// Create a second object in there.
		object, err = os.Create(filepath.Join(estimationShard, "654321"))
		require.NoError(t, err)
		testhelper.MustClose(t, object)

		looseObjects, err = estimateLooseObjectCount(repo, time.Now())
		require.NoError(t, err)
		require.Equal(t, int64(512), looseObjects)
	})

	t.Run("object in estimation shard with grace period", func(t *testing.T) {
		estimationShard := filepath.Join(repoPath, "objects", "17")
		require.NoError(t, os.MkdirAll(estimationShard, 0o755))

		objectPaths := []string{
			filepath.Join(estimationShard, "123456"),
			filepath.Join(estimationShard, "654321"),
		}

		cutoffDate := time.Now()
		afterCutoffDate := cutoffDate.Add(1 * time.Minute)
		beforeCutoffDate := cutoffDate.Add(-1 * time.Minute)

		for _, objectPath := range objectPaths {
			require.NoError(t, os.WriteFile(objectPath, nil, 0o644))
			require.NoError(t, os.Chtimes(objectPath, afterCutoffDate, afterCutoffDate))
		}

		// Objects are recent, so with the cutoff-date they shouldn't be counted.
		looseObjects, err := estimateLooseObjectCount(repo, cutoffDate)
		require.NoError(t, err)
		require.Equal(t, int64(0), looseObjects)

		for i, objectPath := range objectPaths {
			// Modify the object's mtime should cause it to be counted.
			require.NoError(t, os.Chtimes(objectPath, beforeCutoffDate, beforeCutoffDate))

			looseObjects, err = estimateLooseObjectCount(repo, cutoffDate)
			require.NoError(t, err)
			require.Equal(t, int64((i+1)*256), looseObjects)
		}
	})
}

func TestOptimizeRepository(t *testing.T) {
	cfg := testcfg.Build(t)
	txManager := transaction.NewManager(cfg, backchannel.NewRegistry())

	for _, tc := range []struct {
		desc                   string
		setup                  func(t *testing.T, relativePath string) *gitalypb.Repository
		expectedErr            error
		expectedMetrics        string
		expectedMetricsForPool string
	}{
		{
			desc: "empty repository does nothing",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repo, _ := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
					WithRelativePath: relativePath,
				})
				return repo
			},
			expectedMetrics: `# HELP gitaly_housekeeping_tasks_total Total number of housekeeping tasks performed in the repository
# TYPE gitaly_housekeeping_tasks_total counter
gitaly_housekeeping_tasks_total{housekeeping_task="total", status="success"} 1
`,
		},
		{
			desc: "repository without bitmap repacks objects",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repo, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
					RelativePath: relativePath,
				})
				return repo
			},
			expectedMetrics: `# HELP gitaly_housekeeping_tasks_total Total number of housekeeping tasks performed in the repository
# TYPE gitaly_housekeeping_tasks_total counter
gitaly_housekeeping_tasks_total{housekeeping_task="packed_objects_full", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="written_commit_graph_incremental", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="written_bitmap", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="total", status="success"} 1
`,
		},
		{
			desc: "repository without commit-graph writes commit-graph",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repo, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
					RelativePath: relativePath,
				})
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-d", "--write-bitmap-index")
				return repo
			},
			expectedMetrics: `# HELP gitaly_housekeeping_tasks_total Total number of housekeeping tasks performed in the repository
# TYPE gitaly_housekeeping_tasks_total counter
gitaly_housekeeping_tasks_total{housekeeping_task="written_commit_graph_full", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="total", status="success"} 1
`,
		},
		{
			desc: "repository with multiple packfiles packs only for object pool",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repo, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
					RelativePath: relativePath,
				})

				// Note: git-repack(1) without "-d" will _not_ delete the old
				// packfile and thus end up with two packfiles.
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")

				return repo
			},
			expectedMetrics: `# HELP gitaly_housekeeping_tasks_total Total number of housekeeping tasks performed in the repository
# TYPE gitaly_housekeeping_tasks_total counter
gitaly_housekeeping_tasks_total{housekeeping_task="total", status="success"} 1
`,
			expectedMetricsForPool: `# HELP gitaly_housekeeping_tasks_total Total number of housekeeping tasks performed in the repository
# TYPE gitaly_housekeeping_tasks_total counter
gitaly_housekeeping_tasks_total{housekeeping_task="packed_objects_full", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="written_bitmap", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="written_commit_graph_incremental", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="total", status="success"} 1
`,
		},
		{
			desc: "well-packed repository does not optimize",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repo, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
					RelativePath: relativePath,
				})
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-d", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")
				return repo
			},
			expectedMetrics: `# HELP gitaly_housekeeping_tasks_total Total number of housekeeping tasks performed in the repository
# TYPE gitaly_housekeeping_tasks_total counter
gitaly_housekeeping_tasks_total{housekeeping_task="total", status="success"} 1
`,
		},
		{
			desc: "recent loose objects don't get pruned",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repo, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
					RelativePath: relativePath,
				})
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-d", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")

				// The repack won't repack the following objects because they're
				// broken, and thus we'll retry to prune them afterwards.
				require.NoError(t, os.MkdirAll(filepath.Join(repoPath, "objects", "17"), 0o755))

				// We set the object's mtime to be almost two weeks ago. Given that
				// our timeout is at exactly two weeks this shouldn't caused them to
				// get pruned.
				almostTwoWeeksAgo := time.Now().AddDate(0, 0, -14).Add(time.Minute)

				for i := 0; i < 10; i++ {
					blobPath := filepath.Join(repoPath, "objects", "17", fmt.Sprintf("%d", i))
					require.NoError(t, os.WriteFile(blobPath, nil, 0o644))
					require.NoError(t, os.Chtimes(blobPath, almostTwoWeeksAgo, almostTwoWeeksAgo))
				}

				return repo
			},
			expectedMetrics: `# HELP gitaly_housekeeping_tasks_total Total number of housekeeping tasks performed in the repository
# TYPE gitaly_housekeeping_tasks_total counter
gitaly_housekeeping_tasks_total{housekeeping_task="packed_objects_incremental", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="written_commit_graph_incremental", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="total", status="success"} 1
`,
		},
		{
			desc: "old loose objects get pruned",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repo, repoPath := gittest.CloneRepo(t, cfg, cfg.Storages[0], gittest.CloneRepoOpts{
					RelativePath: relativePath,
				})
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-d", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")

				// The repack won't repack the following objects because they're
				// broken, and thus we'll retry to prune them afterwards.
				require.NoError(t, os.MkdirAll(filepath.Join(repoPath, "objects", "17"), 0o755))

				moreThanTwoWeeksAgo := time.Now().AddDate(0, 0, -14).Add(-time.Minute)

				for i := 0; i < 10; i++ {
					blobPath := filepath.Join(repoPath, "objects", "17", fmt.Sprintf("%d", i))
					require.NoError(t, os.WriteFile(blobPath, nil, 0o644))
					require.NoError(t, os.Chtimes(blobPath, moreThanTwoWeeksAgo, moreThanTwoWeeksAgo))
				}

				return repo
			},
			expectedMetrics: `# HELP gitaly_housekeeping_tasks_total Total number of housekeeping tasks performed in the repository
# TYPE gitaly_housekeeping_tasks_total counter
gitaly_housekeeping_tasks_total{housekeeping_task="packed_objects_incremental", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="written_commit_graph_full", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="pruned_objects",status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="total", status="success"} 1
`,
			// Object pools never prune objects.
			expectedMetricsForPool: `# HELP gitaly_housekeeping_tasks_total Total number of housekeeping tasks performed in the repository
# TYPE gitaly_housekeeping_tasks_total counter
gitaly_housekeeping_tasks_total{housekeeping_task="packed_objects_incremental", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="written_commit_graph_incremental", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="total", status="success"} 1
`,
		},
		{
			desc: "loose refs get packed",
			setup: func(t *testing.T, relativePath string) *gitalypb.Repository {
				repo, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
					WithRelativePath: relativePath,
				})

				for i := 0; i < 16; i++ {
					gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(fmt.Sprintf("branch-%d", i)))
				}

				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")

				return repo
			},
			expectedMetrics: `# HELP gitaly_housekeeping_tasks_total Total number of housekeeping tasks performed in the repository
# TYPE gitaly_housekeeping_tasks_total counter
gitaly_housekeeping_tasks_total{housekeeping_task="packed_refs", status="success"} 1
gitaly_housekeeping_tasks_total{housekeeping_task="total", status="success"} 1
`,
		},
	} {
		testRepoAndPool(t, tc.desc, func(t *testing.T, relativePath string) {
			ctx := testhelper.Context(t)

			repoProto := tc.setup(t, relativePath)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			manager := NewManager(cfg.Prometheus, txManager)

			err := manager.OptimizeRepository(ctx, repo)
			require.Equal(t, tc.expectedErr, err)

			expectedMetrics := tc.expectedMetrics
			if IsPoolRepository(repoProto) && tc.expectedMetricsForPool != "" {
				expectedMetrics = tc.expectedMetricsForPool
			}

			require.NoError(t, testutil.CollectAndCompare(
				manager.tasksTotal,
				bytes.NewBufferString(expectedMetrics),
				"gitaly_housekeeping_tasks_total",
			))
		})
	}
}

func TestOptimizeRepository_ConcurrencyLimit(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	t.Run("subsequent calls get skipped", func(t *testing.T) {
		reqReceivedCh, ch := make(chan struct{}), make(chan struct{})

		repoProto, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		manager := &RepositoryManager{
			optimizeFunc: func(_ context.Context, _ *RepositoryManager, _ *localrepo.Repo) error {
				reqReceivedCh <- struct{}{}
				ch <- struct{}{}

				return nil
			},
		}

		go func() {
			require.NoError(t, manager.OptimizeRepository(ctx, repo))
		}()

		<-reqReceivedCh
		// When repository optimizations are performed for a specific repository already,
		// then any subsequent calls to the same repository should just return immediately
		// without doing any optimizations at all.
		require.NoError(t, manager.OptimizeRepository(ctx, repo))

		<-ch
	})

	t.Run("multiple repositories concurrently", func(t *testing.T) {
		reqReceivedCh, ch := make(chan struct{}), make(chan struct{})

		repoProtoFirst, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])
		repoFirst := localrepo.NewTestRepo(t, cfg, repoProtoFirst)
		repoProtoSecond, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])
		repoSecond := localrepo.NewTestRepo(t, cfg, repoProtoSecond)

		reposOptimized := make(map[string]struct{})

		manager := &RepositoryManager{
			optimizeFunc: func(_ context.Context, _ *RepositoryManager, repo *localrepo.Repo) error {
				reposOptimized[repo.GetRelativePath()] = struct{}{}

				if repo.GitRepo.GetRelativePath() == repoFirst.GetRelativePath() {
					reqReceivedCh <- struct{}{}
					ch <- struct{}{}
				}

				return nil
			},
		}

		// We block in the first call so that we can assert that a second call
		// to a different repository performs the optimization regardless without blocking.
		go func() {
			require.NoError(t, manager.OptimizeRepository(ctx, repoFirst))
		}()

		<-reqReceivedCh

		// Because this optimizes a different repository this call shouldn't block.
		require.NoError(t, manager.OptimizeRepository(ctx, repoSecond))

		<-ch

		assert.Contains(t, reposOptimized, repoFirst.GetRelativePath())
		assert.Contains(t, reposOptimized, repoSecond.GetRelativePath())
	})

	t.Run("serialized optimizations", func(t *testing.T) {
		reqReceivedCh, ch := make(chan struct{}), make(chan struct{})
		repoProto, _ := gittest.CloneRepo(t, cfg, cfg.Storages[0])
		repo := localrepo.NewTestRepo(t, cfg, repoProto)
		var optimizations int

		manager := &RepositoryManager{
			optimizeFunc: func(_ context.Context, _ *RepositoryManager, _ *localrepo.Repo) error {
				optimizations++

				if optimizations == 1 {
					reqReceivedCh <- struct{}{}
					ch <- struct{}{}
				}

				return nil
			},
		}

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			require.NoError(t, manager.OptimizeRepository(ctx, repo))
		}()

		<-reqReceivedCh

		// Because we already have a concurrent call which optimizes the repository we expect
		// that all subsequent calls which try to optimize the same repository return immediately.
		// Furthermore, we expect to see only a single call to the optimizing function because we
		// don't want to optimize the same repository concurrently.
		require.NoError(t, manager.OptimizeRepository(ctx, repo))
		require.NoError(t, manager.OptimizeRepository(ctx, repo))
		require.NoError(t, manager.OptimizeRepository(ctx, repo))
		assert.Equal(t, 1, optimizations)

		<-ch
		wg.Wait()

		// When performing optimizations sequentially though the repository
		// should be unlocked after every call, and consequentially we should
		// also see multiple calls to the optimizing function.
		require.NoError(t, manager.OptimizeRepository(ctx, repo))
		require.NoError(t, manager.OptimizeRepository(ctx, repo))
		require.NoError(t, manager.OptimizeRepository(ctx, repo))
		assert.Equal(t, 4, optimizations)
	})
}

func TestPruneIfNeeded(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	testRepoAndPool(t, "empty repo does not prune", func(t *testing.T, relativePath string) {
		repoProto, _ := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
			WithRelativePath: relativePath,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		didPrune, err := pruneIfNeeded(ctx, repo)
		require.NoError(t, err)
		require.False(t, didPrune)
	})

	testRepoAndPool(t, "repo with single object does not prune", func(t *testing.T, relativePath string) {
		repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
			WithRelativePath: relativePath,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		gittest.WriteBlob(t, cfg, repoPath, []byte("something"))

		didPrune, err := pruneIfNeeded(ctx, repo)
		require.NoError(t, err)
		require.False(t, didPrune)
	})

	testRepoAndPool(t, "repo with single 17-prefixed objects does not prune", func(t *testing.T, relativePath string) {
		repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
			WithRelativePath: relativePath,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("32"))
		require.True(t, strings.HasPrefix(blobID.String(), "17"))

		didPrune, err := pruneIfNeeded(ctx, repo)
		require.NoError(t, err)
		require.False(t, didPrune)
	})

	testRepoAndPool(t, "repo with four 17-prefixed objects does not prune", func(t *testing.T, relativePath string) {
		repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
			WithRelativePath: relativePath,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		for _, contents := range []string{"32", "119", "334", "782"} {
			blobID := gittest.WriteBlob(t, cfg, repoPath, []byte(contents))
			require.True(t, strings.HasPrefix(blobID.String(), "17"))
		}

		didPrune, err := pruneIfNeeded(ctx, repo)
		require.NoError(t, err)
		require.False(t, didPrune)
	})

	testRepoAndPool(t, "repo with five 17-prefixed objects does prune after grace period", func(t *testing.T, relativePath string) {
		repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0], gittest.InitRepoOpts{
			WithRelativePath: relativePath,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		objectPath := func(oid git.ObjectID) string {
			return filepath.Join(repoPath, "objects", oid.String()[0:2], oid.String()[2:])
		}

		// Contents with 17-prefix were brute forced with git-hash-object(1).
		var blobs []git.ObjectID
		for _, contents := range []string{"32", "119", "334", "782", "907"} {
			blobID := gittest.WriteBlob(t, cfg, repoPath, []byte(contents))
			require.True(t, strings.HasPrefix(blobID.String(), "17"))
			blobs = append(blobs, blobID)
		}

		// We also write one blob that stays recent to verify it doesn't get pruned.
		recentBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("922"))
		require.True(t, strings.HasPrefix(recentBlob.String(), "17"))

		// We shouldn't want to prune anything yet because there is no object older than two
		// weeks.
		didPrune, err := pruneIfNeeded(ctx, repo)
		require.NoError(t, err)
		require.False(t, didPrune)

		// Consequentially, the objects shouldn't have been pruned.
		for _, blob := range blobs {
			require.FileExists(t, objectPath(blob))
		}
		require.FileExists(t, objectPath(recentBlob))

		// Now we modify the object's times to be older than two weeks.
		twoWeeksAgo := time.Now().Add(-1 * 2 * 7 * 24 * time.Hour)
		for _, blob := range blobs {
			require.NoError(t, os.Chtimes(objectPath(blob), twoWeeksAgo, twoWeeksAgo))
		}

		// Because we didn't prune objects before due to the grace period, the still exist
		// and thus we would still want to prune here.
		didPrune, err = pruneIfNeeded(ctx, repo)
		require.NoError(t, err)

		if IsPoolRepository(repoProto) {
			// Object pools mustn't ever prune objects.
			require.False(t, didPrune)
			for _, blob := range append(blobs, recentBlob) {
				require.FileExists(t, objectPath(blob))
			}
		} else {
			require.True(t, didPrune)

			// But this time the objects shouldn't exist anymore because they were older than
			// the grace period.
			for _, blob := range blobs {
				require.NoFileExists(t, objectPath(blob))
			}

			// The recent blob should continue to exist though.
			require.FileExists(t, objectPath(recentBlob))
		}
	})
}

func TestWriteCommitGraphIfNeeded(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	for _, tc := range []struct {
		desc                string
		setup               func(t *testing.T) (*gitalypb.Repository, string)
		didRepack           bool
		didPrune            bool
		expectedWrite       bool
		expectedCfg         WriteCommitGraphConfig
		expectedCommitGraph bool
	}{
		{
			desc: "empty repository",
			setup: func(t *testing.T) (*gitalypb.Repository, string) {
				return gittest.InitRepo(t, cfg, cfg.Storages[0])
			},
			didRepack:     true,
			didPrune:      true,
			expectedWrite: false,
		},
		{
			desc: "repository with objects but no refs",
			setup: func(t *testing.T) (*gitalypb.Repository, string) {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				gittest.WriteBlob(t, cfg, repoPath, []byte("something"))
				return repoProto, repoPath
			},
			didRepack:     true,
			didPrune:      true,
			expectedWrite: false,
		},
		{
			desc: "repository without commit-graph",
			setup: func(t *testing.T) (*gitalypb.Repository, string) {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				return repoProto, repoPath
			},
			expectedWrite: true,
			expectedCfg: WriteCommitGraphConfig{
				ReplaceChain: true,
			},
			expectedCommitGraph: true,
		},
		{
			desc: "repository with old-style unsplit commit-graph",
			setup: func(t *testing.T) (*gitalypb.Repository, string) {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

				// Write a non-split commit-graph with bloom filters. We should
				// always rewrite the commit-graphs when we're not using a split
				// commit-graph. We make sure to add bloom filters via
				// `--changed-paths` given that it would otherwise cause us to
				// rewrite the graph regardless of whether the graph is split or not
				// if they were missing.
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--changed-paths")

				return repoProto, repoPath
			},
			expectedWrite: true,
			expectedCfg: WriteCommitGraphConfig{
				ReplaceChain: true,
			},
			expectedCommitGraph: true,
		},
		{
			desc: "repository with split commit-graph without bitmap",
			setup: func(t *testing.T) (*gitalypb.Repository, string) {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

				// Generate a split commit-graph, but don't enable computation of
				// changed paths. This should trigger a rewrite so that we can
				// recompute all graphs and generate the changed paths.
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--split")

				return repoProto, repoPath
			},
			expectedWrite: true,
			expectedCfg: WriteCommitGraphConfig{
				ReplaceChain: true,
			},
			expectedCommitGraph: true,
		},
		{
			desc: "repository with split commit-graph with bitmap without repack",
			setup: func(t *testing.T) (*gitalypb.Repository, string) {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

				// Write a split commit-graph with bitmaps. This is the state we
				// want to be in.
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--split", "--changed-paths")

				return repoProto, repoPath
			},
			// We use the information about whether we repacked objects as an indicator
			// whether something has changed in the repository. If it didn't, then we
			// assume no new objects exist and thus we don't rewrite the commit-graph.
			didRepack:           false,
			expectedWrite:       false,
			expectedCommitGraph: true,
		},
		{
			desc: "repository with split commit-graph with bitmap with repack",
			setup: func(t *testing.T) (*gitalypb.Repository, string) {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

				// Write a split commit-graph with bitmaps. This is the state we
				// want to be in, so there is no write required if we didn't also
				// repack objects.
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--split", "--changed-paths")

				return repoProto, repoPath
			},
			// When we have a valid commit-graph, but objects have been repacked, we
			// assume that there are new objects in the repository. So consequentially,
			// we should write the commit-graphs.
			didRepack:           true,
			expectedWrite:       true,
			expectedCommitGraph: true,
		},
		{
			desc: "repository with split commit-graph with bitmap with pruned objects",
			setup: func(t *testing.T) (*gitalypb.Repository, string) {
				repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

				// Write a split commit-graph with bitmaps. This is the state we
				// want to be in, so there is no write required if we didn't also
				// repack objects.
				gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--split", "--changed-paths")

				return repoProto, repoPath
			},
			// When we have a valid commit-graph, but objects have been repacked, we
			// assume that there are new objects in the repository. So consequentially,
			// we should write the commit-graphs.
			didPrune:      true,
			expectedWrite: true,
			expectedCfg: WriteCommitGraphConfig{
				ReplaceChain: true,
			},
			expectedCommitGraph: true,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, repoPath := tc.setup(t)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			didWrite, writeCommitGraphCfg, err := writeCommitGraphIfNeeded(ctx, repo, tc.didRepack, tc.didPrune)
			require.NoError(t, err)
			require.Equal(t, tc.expectedWrite, didWrite)
			require.Equal(t, tc.expectedCfg, writeCommitGraphCfg)

			commitGraphPath := filepath.Join(repoPath, "objects", "info", "commit-graphs", "commit-graph-chain")
			if tc.expectedCommitGraph {
				require.FileExists(t, commitGraphPath)
			} else {
				require.NoFileExists(t, commitGraphPath)
			}
			gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "verify")
		})
	}

	t.Run("commit-graph with pruned objects", func(t *testing.T) {
		repoProto, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		// Write a first commit-graph that contains the root commit, only.
		rootCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
		gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--split", "--changed-paths")

		// Write a second, incremental commit-graph that contains a commit we're about to
		// make unreachable and then prune.
		unreachableCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(rootCommitID), gittest.WithBranch("main"))
		gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--reachable", "--split=no-merge", "--changed-paths")

		// Reset the "main" branch back to the initial root commit ID and prune the now
		// unreachable second commit.
		gittest.Exec(t, cfg, "-C", repoPath, "update-ref", "refs/heads/main", rootCommitID.String())
		gittest.Exec(t, cfg, "-C", repoPath, "prune", "--expire", "now")

		// The commit-graph chain now refers to the pruned commit, and git-commit-graph(1)
		// should complain about that.
		var stderr bytes.Buffer
		verifyCmd := gittest.NewCommand(t, cfg, "-C", repoPath, "commit-graph", "verify")
		verifyCmd.Stderr = &stderr
		require.EqualError(t, verifyCmd.Run(), "exit status 1")
		require.Equal(t, stderr.String(), fmt.Sprintf("error: Could not read %[1]s\nfailed to parse commit %[1]s from object database for commit-graph\n", unreachableCommitID))

		// Write the commit-graph and pretend that objects have been rewritten, but not
		// pruned.
		didWrite, writeCommitGraphCfg, err := writeCommitGraphIfNeeded(ctx, repo, true, false)
		require.NoError(t, err)
		require.True(t, didWrite)
		require.Equal(t, WriteCommitGraphConfig{}, writeCommitGraphCfg)

		// When pretending that no objects have been pruned we still observe the same
		// failure.
		stderr.Reset()
		verifyCmd = gittest.NewCommand(t, cfg, "-C", repoPath, "commit-graph", "verify")
		verifyCmd.Stderr = &stderr
		require.EqualError(t, verifyCmd.Run(), "exit status 1")
		require.Equal(t, stderr.String(), fmt.Sprintf("error: Could not read %[1]s\nfailed to parse commit %[1]s from object database for commit-graph\n", unreachableCommitID))

		// Write the commit-graph a second time, but this time we pretend we have just
		// pruned objects. This should cause the commit-graph to be rewritten.
		didWrite, writeCommitGraphCfg, err = writeCommitGraphIfNeeded(ctx, repo, false, true)
		require.NoError(t, err)
		require.True(t, didWrite)
		require.Equal(t, WriteCommitGraphConfig{
			ReplaceChain: true,
		}, writeCommitGraphCfg)

		// The commit-graph should now have been fixed.
		gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "verify")
	})
}
