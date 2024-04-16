package objectpool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	housekeepingmgr "gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/manager"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/objectpool"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestCreate(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	logger := testhelper.NewLogger(t)
	cfg, repo, repoPath, _, client := setup(t, ctx)
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	txManager := transaction.NewManager(cfg, logger, nil)
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	poolProto := &gitalypb.ObjectPool{
		Repository: &gitalypb.Repository{
			StorageName:  cfg.Storages[0].Name,
			RelativePath: gittest.NewObjectPoolName(t),
		},
	}

	_, err := client.CreateObjectPool(ctx, &gitalypb.CreateObjectPoolRequest{
		ObjectPool: poolProto,
		Origin:     repo,
	})
	require.NoError(t, err)

	pool, err := objectpool.FromProto(
		logger,
		config.NewLocator(cfg),
		gittest.NewCommandFactory(t, cfg),
		catfileCache,
		txManager,
		housekeepingmgr.New(cfg.Prometheus, logger, txManager, nil),
		&gitalypb.ObjectPool{
			Repository: &gitalypb.Repository{
				StorageName:  cfg.Storages[0].Name,
				RelativePath: gittest.GetReplicaPath(t, ctx, cfg, poolProto.GetRepository()),
			},
		},
	)
	require.NoError(t, err)
	poolPath := gittest.RepositoryPath(t, pool)

	// Assert that the now-created object pool exists and is valid.
	require.True(t, pool.IsValid())
	require.NoDirExists(t, filepath.Join(poolPath, "hooks"))
	gittest.RequireObjectExists(t, cfg, poolPath, commitID)

	// Making the same request twice should result in an error.
	_, err = client.CreateObjectPool(ctx, &gitalypb.CreateObjectPoolRequest{
		ObjectPool: poolProto,
		Origin:     repo,
	})
	require.Error(t, err)
	require.True(t, pool.IsValid())
}

func TestCreate_emptySource(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repoProto, _, _, client := setup(t, ctx)

	objectPoolProto := &gitalypb.ObjectPool{
		Repository: &gitalypb.Repository{
			StorageName:  repoProto.StorageName,
			RelativePath: gittest.NewObjectPoolName(t),
		},
	}

	response, err := client.CreateObjectPool(ctx, &gitalypb.CreateObjectPoolRequest{
		ObjectPool: objectPoolProto,
		Origin:     repoProto,
	})
	require.NoError(t, err)
	testhelper.ProtoEqual(t, &gitalypb.CreateObjectPoolResponse{}, response)

	objectPoolRepo := localrepo.NewTestRepo(t, cfg, objectPoolProto.Repository)

	// Assert that the created object pool is indeed empty.
	info, err := stats.RepositoryInfoForRepository(ctx, objectPoolRepo)
	require.NoError(t, err)
	info.Packfiles.LastFullRepack = time.Time{}
	require.Equal(t, stats.RepositoryInfo{
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
	}, info)

	// And furthermore assert that the object hash of the new object pool matches what we
	// expect.
	objectHash, err := objectPoolRepo.ObjectHash(ctx)
	require.NoError(t, err)
	require.Equal(t, gittest.DefaultObjectHash.Format, objectHash.Format)
}

func TestCreate_unsuccessful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, _, _, client := setup(t, ctx, testserver.WithDisablePraefect())

	// Precreate a stale lock for a valid object pool path so that we can verify that the lock
	// gets honored as expected.
	lockedRelativePath := gittest.NewObjectPoolName(t)
	lockedFullPath := filepath.Join(cfg.Storages[0].Path, lockedRelativePath+".lock")
	require.NoError(t, os.MkdirAll(filepath.Dir(lockedFullPath), perm.SharedDir))
	require.NoError(t, os.WriteFile(lockedFullPath, nil, perm.SharedFile))

	// Create a preexisting object pool.
	preexistingPool := &gitalypb.ObjectPool{
		Repository: &gitalypb.Repository{
			StorageName:  cfg.Storages[0].Name,
			RelativePath: gittest.NewObjectPoolName(t),
		},
	}
	_, err := client.CreateObjectPool(ctx, &gitalypb.CreateObjectPoolRequest{
		ObjectPool: preexistingPool,
		Origin:     repo,
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		desc        string
		request     *gitalypb.CreateObjectPoolRequest
		expectedErr error
		skipWithWAL string
	}{
		{
			desc: "no origin repository",
			request: &gitalypb.CreateObjectPoolRequest{
				ObjectPool: &gitalypb.ObjectPool{
					Repository: &gitalypb.Repository{
						StorageName:  cfg.Storages[0].Name,
						RelativePath: gittest.NewObjectPoolName(t),
					},
				},
			},
			expectedErr: errMissingOriginRepository,
		},
		{
			desc: "no object pool",
			request: &gitalypb.CreateObjectPoolRequest{
				Origin: repo,
			},
			expectedErr: func() error {
				if testhelper.IsWALEnabled() {
					// The transaction middleware is erroring out and returns the generic
					// repository not set error.
					return structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet)
				}

				return errMissingPool
			}(),
		},
		{
			desc: "outside pools directory",
			request: &gitalypb.CreateObjectPoolRequest{
				Origin: repo,
				ObjectPool: &gitalypb.ObjectPool{
					Repository: &gitalypb.Repository{
						StorageName:  cfg.Storages[0].Name,
						RelativePath: "outside-pools",
					},
				},
			},
			expectedErr: errInvalidPoolDir,
		},
		{
			desc: "path must be lowercase",
			request: &gitalypb.CreateObjectPoolRequest{
				Origin: repo,
				ObjectPool: &gitalypb.ObjectPool{
					Repository: &gitalypb.Repository{
						StorageName:  cfg.Storages[0].Name,
						RelativePath: strings.ToUpper(gittest.NewObjectPoolName(t)),
					},
				},
			},
			expectedErr: errInvalidPoolDir,
		},
		{
			desc: "subdirectories must match first four pool digits",
			request: &gitalypb.CreateObjectPoolRequest{
				Origin: repo,
				ObjectPool: &gitalypb.ObjectPool{
					Repository: &gitalypb.Repository{
						StorageName:  cfg.Storages[0].Name,
						RelativePath: "@pools/aa/bb/ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff.git",
					},
				},
			},
			expectedErr: errInvalidPoolDir,
		},
		{
			desc: "pool path traversal fails",
			request: &gitalypb.CreateObjectPoolRequest{
				Origin: repo,
				ObjectPool: &gitalypb.ObjectPool{
					Repository: &gitalypb.Repository{
						StorageName:  cfg.Storages[0].Name,
						RelativePath: gittest.NewObjectPoolName(t) + "/..",
					},
				},
			},
			expectedErr: errInvalidPoolDir,
		},
		{
			desc: "pool is locked",
			request: &gitalypb.CreateObjectPoolRequest{
				Origin: repo,
				ObjectPool: &gitalypb.ObjectPool{
					Repository: &gitalypb.Repository{
						StorageName:  cfg.Storages[0].Name,
						RelativePath: lockedRelativePath,
					},
				},
			},
			expectedErr: structerr.NewInternal("creating object pool: locking repository: file already locked"),
			skipWithWAL: `Transactions are isolated and won't see each other's locks.`,
		},
		{
			desc: "pool exists",
			request: &gitalypb.CreateObjectPoolRequest{
				Origin:     repo,
				ObjectPool: preexistingPool,
			},
			expectedErr: structerr.NewFailedPrecondition("creating object pool: repository exists already"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if tc.skipWithWAL != "" {
				testhelper.SkipWithWAL(t, tc.skipWithWAL)
			}

			_, err := client.CreateObjectPool(ctx, tc.request)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}

func TestCreate_atomic(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	gitCmdFactory := gittest.NewInterceptingCommandFactory(t, ctx, cfg, func(execEnv git.ExecutionEnvironment) string {
		return fmt.Sprintf(`#!/usr/bin/env bash
		if [[ ! "$@" =~ "clone" ]]; then
			exec %[1]q "$@"
		fi

		# If we are cloning then this must be the object pool that we try to create. We
		# execute the command, but then afterwards we pretend to fail. We should ultimately
		# see that the pool does not exist.
		%[1]q "$@" 2>/dev/null

		exit 123
		`, execEnv.BinaryPath)
	})

	cfg, repo, _, _, client := setupWithConfig(t, ctx, cfg, testserver.WithGitCommandFactory(gitCmdFactory))

	objectPool := &gitalypb.ObjectPool{
		Repository: &gitalypb.Repository{
			StorageName:  cfg.Storages[0].Name,
			RelativePath: gittest.NewObjectPoolName(t),
		},
	}

	_, err := client.CreateObjectPool(ctx, &gitalypb.CreateObjectPoolRequest{
		ObjectPool: objectPool,
		Origin:     repo,
	})
	testhelper.RequireGrpcError(t, structerr.NewInternal("creating object pool: cloning to pool: exit status 123, stderr: %q", ""), err)
	require.NoDirExists(t, filepath.Join(cfg.Storages[0].Path, objectPool.Repository.RelativePath))
}
