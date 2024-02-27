package housekeeping

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	gitalycfgprom "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

type errorInjectingCommandFactory struct {
	git.CommandFactory
	injectedErrors map[string]error
}

func (f errorInjectingCommandFactory) New(
	ctx context.Context,
	repo storage.Repository,
	cmd git.Command,
	opts ...git.CmdOpt,
) (*command.Command, error) {
	if injectedErr, ok := f.injectedErrors[cmd.Name]; ok {
		return nil, injectedErr
	}

	return f.CommandFactory.New(ctx, repo, cmd, opts...)
}

type blockingCommandFactory struct {
	git.CommandFactory
	block map[string]chan struct{}
}

func (f *blockingCommandFactory) New(
	ctx context.Context,
	repo storage.Repository,
	cmd git.Command,
	opts ...git.CmdOpt,
) (*command.Command, error) {
	if ch, ok := f.block[cmd.Name]; ok {
		ch <- struct{}{}
		<-ch
	}

	return f.CommandFactory.New(ctx, repo, cmd, opts...)
}

func TestRepackIfNeeded(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	t.Run("no repacking", func(t *testing.T) {
		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		// Create a loose object to verify it's not getting repacked.
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("a"))

		didRepack, repackObjectsCfg, err := repackIfNeeded(ctx, repo, mockOptimizationStrategy{
			shouldRepackObjects: false,
		})
		require.NoError(t, err)
		require.False(t, didRepack)
		require.Equal(t, RepackObjectsConfig{}, repackObjectsCfg)

		requireObjectsState(t, repo, objectsState{
			looseObjects: 2,
		})
	})

	t.Run("incremental repack", func(t *testing.T) {
		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		// Create an object and pack it into a packfile.
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("a"))
		gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad")
		// And a second object that is loose. The incremental repack should only pack the
		// loose object.
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("b"))

		didRepack, repackObjectsCfg, err := repackIfNeeded(ctx, repo, mockOptimizationStrategy{
			shouldRepackObjects: true,
			repackObjectsCfg: RepackObjectsConfig{
				Strategy: RepackObjectsStrategyIncrementalWithUnreachable,
			},
		})
		require.NoError(t, err)
		require.True(t, didRepack)
		require.Equal(t, RepackObjectsConfig{
			Strategy: RepackObjectsStrategyIncrementalWithUnreachable,
		}, repackObjectsCfg)

		requireObjectsState(t, repo, objectsState{
			packfiles: 2,
			hasBitmap: true,
		})
	})

	t.Run("full repack", func(t *testing.T) {
		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		// Create an object and pack it into a packfile.
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("a"), gittest.WithMessage("a"))
		gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad")
		// And a second object that is loose. The full repack should repack both the
		// packfiles and loose objects into a single packfile.
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("b"), gittest.WithMessage("b"))

		didRepack, repackObjectsCfg, err := repackIfNeeded(ctx, repo, mockOptimizationStrategy{
			shouldRepackObjects: true,
			repackObjectsCfg: RepackObjectsConfig{
				Strategy: RepackObjectsStrategyFullWithUnreachable,
			},
		})
		require.NoError(t, err)
		require.True(t, didRepack)
		require.Equal(t, RepackObjectsConfig{
			Strategy: RepackObjectsStrategyFullWithUnreachable,
		}, repackObjectsCfg)

		requireObjectsState(t, repo, objectsState{
			packfiles: 1,
		})
	})

	t.Run("cruft repack with recent unreachable object", func(t *testing.T) {
		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("reachable"), gittest.WithMessage("reachable"))
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("unreachable"))

		// The expiry time is before we have written the objects, so they should be packed
		// into a cruft pack.
		expiryTime := time.Now().Add(-1 * time.Hour)

		didRepack, repackObjectsCfg, err := repackIfNeeded(ctx, repo, mockOptimizationStrategy{
			shouldRepackObjects: true,
			repackObjectsCfg: RepackObjectsConfig{
				Strategy:          RepackObjectsStrategyFullWithCruft,
				CruftExpireBefore: expiryTime,
			},
		})
		require.NoError(t, err)
		require.True(t, didRepack)
		require.Equal(t, RepackObjectsConfig{
			Strategy:          RepackObjectsStrategyFullWithCruft,
			CruftExpireBefore: expiryTime,
		}, repackObjectsCfg)

		requireObjectsState(t, repo, objectsState{
			packfiles:  2,
			cruftPacks: 1,
		})
	})

	t.Run("cruft repack with expired cruft object", func(t *testing.T) {
		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("reachable"), gittest.WithMessage("reachable"))
		gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("unreachable"))
		gittest.Exec(t, cfg, "-C", repoPath, "repack", "--cruft", "-d")

		// The expiry time is after we have written the cruft pack, so the unreachable
		// object should get pruned.
		expiryTime := time.Now().Add(1 * time.Hour)

		didRepack, repackObjectsCfg, err := repackIfNeeded(ctx, repo, mockOptimizationStrategy{
			shouldRepackObjects: true,
			repackObjectsCfg: RepackObjectsConfig{
				Strategy:          RepackObjectsStrategyFullWithCruft,
				CruftExpireBefore: expiryTime,
			},
		})
		require.NoError(t, err)
		require.True(t, didRepack)
		require.Equal(t, RepackObjectsConfig{
			Strategy:          RepackObjectsStrategyFullWithCruft,
			CruftExpireBefore: expiryTime,
		}, repackObjectsCfg)

		requireObjectsState(t, repo, objectsState{
			packfiles:  1,
			cruftPacks: 0,
		})
	})

	t.Run("failed repack returns configuration", func(t *testing.T) {
		repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})

		gitCmdFactory := errorInjectingCommandFactory{
			CommandFactory: gittest.NewCommandFactory(t, cfg),
			injectedErrors: map[string]error{
				"repack": assert.AnError,
			},
		}

		repo := localrepo.New(testhelper.NewLogger(t), config.NewLocator(cfg), gitCmdFactory, nil, repoProto)

		expectedCfg := RepackObjectsConfig{
			Strategy:          RepackObjectsStrategyFullWithCruft,
			CruftExpireBefore: time.Now(),
		}

		didRepack, actualCfg, err := repackIfNeeded(ctx, repo, mockOptimizationStrategy{
			shouldRepackObjects: true,
			repackObjectsCfg:    expectedCfg,
		})
		require.Equal(t, fmt.Errorf("repack failed: %w", assert.AnError), err)
		require.False(t, didRepack)
		require.Equal(t, expectedCfg, actualCfg)
	})
}

func TestPackRefsIfNeeded(t *testing.T) {
	testhelper.SkipWithReftable(t, "pack-refs is only required for the files backend")

	t.Parallel()

	type setupData struct {
		errExpected          error
		refsShouldBePacked   bool
		shouldPackReferences func(context.Context) bool
	}

	for _, tc := range []struct {
		desc            string
		setup           func(t *testing.T, ctx context.Context, m *RepositoryManager, repoPath string, b *blockingCommandFactory) setupData
		repoStateExists bool
	}{
		{
			desc: "strategy doesn't pack references",
			setup: func(t *testing.T, ctx context.Context, m *RepositoryManager, repoPath string, b *blockingCommandFactory) setupData {
				return setupData{
					refsShouldBePacked:   false,
					shouldPackReferences: func(context.Context) bool { return false },
				}
			},
		},
		{
			desc: "strategy packs references",
			setup: func(t *testing.T, ctx context.Context, m *RepositoryManager, repoPath string, b *blockingCommandFactory) setupData {
				return setupData{
					refsShouldBePacked:   true,
					shouldPackReferences: func(context.Context) bool { return true },
				}
			},
		},
		{
			desc: "one inhibitor present",
			setup: func(t *testing.T, ctx context.Context, m *RepositoryManager, repoPath string, b *blockingCommandFactory) setupData {
				success, cleanup, err := m.repositoryStates.addPackRefsInhibitor(ctx, repoPath)
				require.True(t, success)
				require.NoError(t, err)

				t.Cleanup(cleanup)

				return setupData{
					refsShouldBePacked:   false,
					shouldPackReferences: func(context.Context) bool { return true },
				}
			},
			repoStateExists: true,
		},
		{
			desc: "few inhibitors present",
			setup: func(t *testing.T, ctx context.Context, m *RepositoryManager, repoPath string, b *blockingCommandFactory) setupData {
				for i := 0; i < 10; i++ {
					success, cleanup, err := m.repositoryStates.addPackRefsInhibitor(ctx, repoPath)
					require.True(t, success)
					require.NoError(t, err)

					t.Cleanup(cleanup)
				}

				return setupData{
					refsShouldBePacked:   false,
					shouldPackReferences: func(context.Context) bool { return true },
				}
			},
			repoStateExists: true,
		},
		{
			desc: "inhibitors finish before pack refs call",
			setup: func(t *testing.T, ctx context.Context, m *RepositoryManager, repoPath string, b *blockingCommandFactory) setupData {
				for i := 0; i < 10; i++ {
					success, cleanup, err := m.repositoryStates.addPackRefsInhibitor(ctx, repoPath)
					require.True(t, success)
					require.NoError(t, err)

					defer cleanup()
				}

				return setupData{
					refsShouldBePacked:   true,
					shouldPackReferences: func(context.Context) bool { return true },
				}
			},
		},
		{
			desc: "only some inhibitors finish before pack refs call",
			setup: func(t *testing.T, ctx context.Context, m *RepositoryManager, repoPath string, b *blockingCommandFactory) setupData {
				for i := 0; i < 10; i++ {
					success, cleanup, err := m.repositoryStates.addPackRefsInhibitor(ctx, repoPath)
					require.True(t, success)
					require.NoError(t, err)

					defer cleanup()
				}

				// This inhibitor doesn't finish before the git-pack-refs(1) call
				success, cleanup, err := m.repositoryStates.addPackRefsInhibitor(ctx, repoPath)
				require.True(t, success)
				require.NoError(t, err)

				t.Cleanup(cleanup)

				return setupData{
					refsShouldBePacked:   false,
					shouldPackReferences: func(context.Context) bool { return true },
				}
			},
			repoStateExists: true,
		},
		{
			desc: "inhibitor cancels being blocked",
			setup: func(t *testing.T, ctx context.Context, m *RepositoryManager, repoPath string, b *blockingCommandFactory) setupData {
				ch := make(chan struct{})

				go func() {
					// We wait till we reach git-pack-refs(1) via the blocking command factory.
					<-ch

					// But here, we cancel the ctx we're sending into inhibitPackingReferences
					// so that we don't set the state and exit without being blocked.
					ctx, cancel := context.WithCancel(ctx)
					cancel()

					success, _, err := m.repositoryStates.addPackRefsInhibitor(ctx, repoPath)
					if success {
						t.Errorf("expected addPackRefsInhibitor to return false")
					}
					if !errors.Is(err, context.Canceled) {
						t.Errorf("expected a context cancelled error")
					}

					// This is used to block git-pack-refs(1) so that our cancellation above
					// isn't beaten by packRefsIfNeeded actually finishing first (race condition).
					ch <- struct{}{}
				}()

				b.block["pack-refs"] = ch

				return setupData{
					refsShouldBePacked: true,
					shouldPackReferences: func(_ context.Context) bool {
						return true
					},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)
			cfg := testcfg.Build(t)
			logger := testhelper.NewLogger(t)

			gitCmdFactory := blockingCommandFactory{
				CommandFactory: gittest.NewCommandFactory(t, cfg),
				block:          make(map[string]chan struct{}),
			}

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})
			repo := localrepo.New(logger, config.NewLocator(cfg), &gitCmdFactory, nil, repoProto)

			// Write an empty commit such that we can create valid refs.
			gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

			packedRefsPath := filepath.Join(repoPath, "packed-refs")
			looseRefPath := filepath.Join(repoPath, "refs", "heads", "main")

			manager := NewManager(gitalycfgprom.Config{}, logger, nil)
			data := tc.setup(t, ctx, manager, repoPath, &gitCmdFactory)

			didRepack, err := manager.packRefsIfNeeded(ctx, repo, mockOptimizationStrategy{
				shouldRepackReferences: data.shouldPackReferences,
			})

			require.Equal(t, data.errExpected, err)

			if data.refsShouldBePacked {
				require.True(t, didRepack)
				require.FileExists(t, packedRefsPath)
				require.NoFileExists(t, looseRefPath)
			} else {
				require.False(t, didRepack)
				require.NoFileExists(t, packedRefsPath)
				require.FileExists(t, looseRefPath)
			}

			_, ok := manager.repositoryStates.values[repoPath]
			require.Equal(t, tc.repoStateExists, ok)
		})
	}
}

func TestOptimizeRepository(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	txManager := transaction.NewManager(cfg, testhelper.SharedLogger(t), backchannel.NewRegistry())

	earlierDate := time.Date(2022, 12, 1, 0, 0, 0, 0, time.Local)
	laterDate := time.Date(2022, 12, 1, 12, 0, 0, 0, time.Local)

	linkRepoToPool := func(t *testing.T, repoPath, poolPath string, date time.Time) {
		t.Helper()

		alternatesPath := filepath.Join(repoPath, "objects", "info", "alternates")

		require.NoError(t, os.WriteFile(
			alternatesPath,
			[]byte(filepath.Join(poolPath, "objects")),
			perm.PrivateFile,
		))
		require.NoError(t, os.Chtimes(alternatesPath, date, date))
	}

	readPackfiles := func(t *testing.T, repoPath string) []string {
		packPaths, err := filepath.Glob(filepath.Join(repoPath, "objects", "pack", "pack-*.pack"))
		require.NoError(t, err)

		packs := make([]string, 0, len(packPaths))
		for _, packPath := range packPaths {
			packs = append(packs, filepath.Base(packPath))
		}
		return packs
	}

	type metric struct {
		name, status string
		count        int
	}

	type setupData struct {
		repo                   *localrepo.Repo
		options                []OptimizeRepositoryOption
		expectedErr            error
		expectedMetrics        []metric
		expectedMetricsForPool []metric
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T, relativePath string) setupData
	}{
		{
			desc: "empty repository does nothing",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})

				metrics := []metric{
					{name: "total", status: "success", count: 1},
				}

				// We always create commit graphs with reftables.
				if testhelper.IsReftableEnabled() {
					metrics = append(metrics, metric{name: "written_commit_graph_full", status: "success", count: 1})
				}

				return setupData{
					repo:            localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: metrics,
				}
			},
		},
		{
			desc: "repository without bitmap repacks objects",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "written_bitmap", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "repository without commit-graph writes commit-graph",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-d", "--write-bitmap-index")
				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "written_bitmap", status: "success", count: 1},
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "repository with commit-graph without generation data writes commit-graph",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-d", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-c", "commitGraph.generationVersion=1", "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")
				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "written_bitmap", status: "success", count: 1},
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "repository without multi-pack-index performs incremental repack",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-d", "-b")
				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "written_bitmap", status: "success", count: 1},
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "repository with multiple packfiles packs only for object pool",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})

				// Create two packfiles by creating two objects and then packing
				// twice. Note that the second git-repack(1) is incremental so that
				// we don't remove the first packfile.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("first"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "--write-bitmap-index")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("second"), gittest.WithMessage("second"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack")

				gittest.Exec(t, cfg, "-c", "commitGraph.generationVersion=2", "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")

				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_full_with_cruft", status: "success", count: 1},
						{name: "written_bitmap", status: "success", count: 1},
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
					expectedMetricsForPool: []metric{
						{name: "packed_objects_full_with_unreachable", status: "success", count: 1},
						{name: "written_bitmap", status: "success", count: 1},
						{name: "written_commit_graph_incremental", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "well-packed repository does not optimize",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-d", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-c", "commitGraph.generationVersion=2", "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")
				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "written_bitmap", status: "success", count: 1},
						{name: "written_commit_graph_incremental", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "well-packed repository with multi-pack-index does not optimize",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-d", "--write-bitmap-index", "--write-midx")
				gittest.Exec(t, cfg, "-c", "commitGraph.generationVersion=2", "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")
				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "recent loose objects don't get pruned",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-d", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-c", "commitGraph.generationVersion=2", "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")

				// The repack won't repack the following objects because they're
				// broken, and thus we'll retry to prune them afterwards.
				require.NoError(t, os.MkdirAll(filepath.Join(repoPath, "objects", "17"), perm.SharedDir))

				// We set the object's mtime to be almost two weeks ago. Given that
				// our timeout is at exactly two weeks this shouldn't caused them to
				// get pruned.
				almostTwoWeeksAgo := time.Now().Add(stats.StaleObjectsGracePeriod).Add(time.Minute)

				for i := 0; i < looseObjectLimit+1; i++ {
					blobPath := filepath.Join(repoPath, "objects", "17", fmt.Sprintf("%d", i))
					require.NoError(t, os.WriteFile(blobPath, nil, perm.SharedFile))
					require.NoError(t, os.Chtimes(blobPath, almostTwoWeeksAgo, almostTwoWeeksAgo))
				}

				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "written_bitmap", status: "success", count: 1},
						{name: "written_commit_graph_incremental", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "old loose objects get pruned",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-d", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-c", "commitGraph.generationVersion=2", "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")

				// The repack won't repack the following objects because they're
				// broken, and thus we'll retry to prune them afterwards.
				require.NoError(t, os.MkdirAll(filepath.Join(repoPath, "objects", "17"), perm.SharedDir))

				moreThanTwoWeeksAgo := time.Now().Add(stats.StaleObjectsGracePeriod).Add(-time.Minute)

				for i := 0; i < looseObjectLimit+1; i++ {
					blobPath := filepath.Join(repoPath, "objects", "17", fmt.Sprintf("%d", i))
					require.NoError(t, os.WriteFile(blobPath, nil, perm.SharedFile))
					require.NoError(t, os.Chtimes(blobPath, moreThanTwoWeeksAgo, moreThanTwoWeeksAgo))
				}

				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "pruned_objects", status: "success", count: 1},
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "written_bitmap", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
					// Object pools never prune objects.
					expectedMetricsForPool: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "written_bitmap", status: "success", count: 1},
						{name: "written_commit_graph_incremental", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "loose refs get packed",
			setup: func(t *testing.T, relativePath string) setupData {
				testhelper.SkipWithReftable(t, `tests are specific to files backend`)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})

				for i := 0; i < 16; i++ {
					gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(fmt.Sprintf("branch-%d", i)))
				}

				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "--write-bitmap-index")
				gittest.Exec(t, cfg, "-c", "commitGraph.generationVersion=2", "-C", repoPath, "commit-graph", "write", "--split", "--changed-paths")

				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "packed_refs", status: "success", count: 1},
						{name: "written_commit_graph_incremental", status: "success", count: 1},
						{name: "written_bitmap", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "repository connected to empty object pool",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("some-branch"))

				_, poolPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           gittest.NewObjectPoolName(t),
				})

				require.NoError(t, stats.UpdateFullRepackTimestamp(repoPath, laterDate))
				linkRepoToPool(t, repoPath, poolPath, earlierDate)

				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "repository with all objects deduplicated via pool",
			setup: func(t *testing.T, relativePath string) setupData {
				_, poolPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           gittest.NewObjectPoolName(t),
				})
				commitID := gittest.WriteCommit(t, cfg, poolPath)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})

				require.NoError(t, stats.UpdateFullRepackTimestamp(repoPath, laterDate))
				linkRepoToPool(t, repoPath, poolPath, earlierDate)

				gittest.WriteRef(t, cfg, repoPath, "refs/heads/some-branch", commitID)

				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "repository with some deduplicated objects",
			setup: func(t *testing.T, relativePath string) setupData {
				_, poolPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           gittest.NewObjectPoolName(t),
				})
				commitID := gittest.WriteCommit(t, cfg, poolPath)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				require.NoError(t, stats.UpdateFullRepackTimestamp(repoPath, laterDate))
				linkRepoToPool(t, repoPath, poolPath, earlierDate)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(commitID), gittest.WithBranch("some-branch"))

				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "recently linked repository gets a full repack",
			setup: func(t *testing.T, relativePath string) setupData {
				testhelper.SkipWithReftable(t, `tests are specific to files backend`)

				_, poolPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           gittest.NewObjectPoolName(t),
				})

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath)

				// Pretend that the last full repack has happened before creating
				// the gitalternates file. This should cause a full repack in order
				// to deduplicate all objects.
				require.NoError(t, stats.UpdateFullRepackTimestamp(repoPath, earlierDate))
				linkRepoToPool(t, repoPath, poolPath, laterDate)

				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_full_with_cruft", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
					expectedMetricsForPool: []metric{
						{
							name:   "packed_objects_full_with_unreachable",
							status: "success",
							count:  1,
						},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "repository with some deduplicated objects and eager strategy",
			setup: func(t *testing.T, relativePath string) setupData {
				_, poolPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           gittest.NewObjectPoolName(t),
				})
				commitID := gittest.WriteCommit(t, cfg, poolPath)

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				require.NoError(t, stats.UpdateFullRepackTimestamp(repoPath, laterDate))
				linkRepoToPool(t, repoPath, poolPath, earlierDate)
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(commitID), gittest.WithBranch("some-branch"))

				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					options: []OptimizeRepositoryOption{
						WithOptimizationStrategyConstructor(func(repoInfo stats.RepositoryInfo) OptimizationStrategy {
							return NewEagerOptimizationStrategy(repoInfo)
						}),
					},
					expectedMetrics: []metric{
						{name: "packed_refs", status: "success", count: 1},
						{name: "pruned_objects", status: "success", count: 1},
						{name: "packed_objects_full_with_cruft", status: "success", count: 1},
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
					expectedMetricsForPool: []metric{
						{name: "packed_refs", status: "success", count: 1},
						{name: "packed_objects_full_with_unreachable", status: "success", count: 1},
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "repository with same packfile in pool",
			setup: func(t *testing.T, relativePath string) setupData {
				_, poolPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           gittest.NewObjectPoolName(t),
				})
				gittest.WriteCommit(t, cfg, poolPath, gittest.WithBranch("some-branch"))
				gittest.Exec(t, cfg, "-C", poolPath, "repack", "-Ad")

				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("some-branch"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad")

				repoPackfiles := readPackfiles(t, repoPath)
				require.Len(t, repoPackfiles, 1)

				// Assert that the packfiles in both the repository and the object
				// pool are actually the same. This is likely to happen e.g. when
				// the object pool has just been created and the repository was
				// linked to it and has caused bugs with geometric repacking in the
				// past.
				require.Equal(t, repoPackfiles, readPackfiles(t, poolPath))

				require.NoError(t, stats.UpdateFullRepackTimestamp(repoPath, laterDate))
				linkRepoToPool(t, repoPath, poolPath, earlierDate)

				return setupData{
					repo: localrepo.NewTestRepo(t, cfg, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "success", count: 1},
						{name: "written_commit_graph_full", status: "success", count: 1},
						{name: "written_multi_pack_index", status: "success", count: 1},
						{name: "total", status: "success", count: 1},
					},
				}
			},
		},
		{
			desc: "failing repack",
			setup: func(t *testing.T, relativePath string) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           relativePath,
				})
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))

				gitCmdFactory := errorInjectingCommandFactory{
					CommandFactory: gittest.NewCommandFactory(t, cfg),
					injectedErrors: map[string]error{
						"repack": assert.AnError,
					},
				}

				return setupData{
					repo: localrepo.New(testhelper.NewLogger(t), config.NewLocator(cfg), gitCmdFactory, nil, repo),
					expectedMetrics: []metric{
						{name: "packed_objects_geometric", status: "failure", count: 1},
						{name: "written_bitmap", status: "failure", count: 1},
						{name: "written_multi_pack_index", status: "failure", count: 1},
						{name: "total", status: "failure", count: 1},
					},
					expectedErr: fmt.Errorf("could not repack: %w", fmt.Errorf("repack failed: %w", assert.AnError)),
				}
			},
		},
	} {
		tc := tc

		testRepoAndPool(t, tc.desc, func(t *testing.T, relativePath string) {
			t.Parallel()

			setup := tc.setup(t, relativePath)

			manager := NewManager(cfg.Prometheus, testhelper.SharedLogger(t), txManager)

			err := manager.OptimizeRepository(ctx, setup.repo, setup.options...)
			require.Equal(t, setup.expectedErr, err)

			expectedMetrics := setup.expectedMetrics
			if storage.IsPoolRepository(setup.repo) && setup.expectedMetricsForPool != nil {
				expectedMetrics = setup.expectedMetricsForPool
			}

			var buf bytes.Buffer
			_, err = buf.WriteString("# HELP gitaly_housekeeping_tasks_total Total number of housekeeping tasks performed in the repository\n")
			require.NoError(t, err)
			_, err = buf.WriteString("# TYPE gitaly_housekeeping_tasks_total counter\n")
			require.NoError(t, err)

			for _, metric := range expectedMetrics {
				_, err := buf.WriteString(fmt.Sprintf(
					"gitaly_housekeeping_tasks_total{housekeeping_task=%q, status=%q} %d\n",
					metric.name, metric.status, metric.count,
				))
				require.NoError(t, err)
			}

			require.NoError(t, testutil.CollectAndCompare(
				manager.tasksTotal, &buf, "gitaly_housekeeping_tasks_total",
			))

			path, err := setup.repo.Path()
			require.NoError(t, err)
			// The state of the repo should be cleared after running housekeeping.
			require.NotContains(t, manager.repositoryStates.values, path)
		})
	}
}

func TestOptimizeRepository_ConcurrencyLimit(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	t.Run("subsequent calls get skipped", func(t *testing.T) {
		reqReceivedCh, ch := make(chan struct{}), make(chan struct{})

		repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		manager := NewManager(gitalycfgprom.Config{}, testhelper.NewLogger(t), nil)
		manager.optimizeFunc = func(context.Context, *RepositoryManager, log.Logger, *localrepo.Repo, OptimizationStrategy) error {
			reqReceivedCh <- struct{}{}
			ch <- struct{}{}

			return nil
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

	// We want to confirm that even if a state exists, the housekeeping shall run as
	// long as the state doesn't state that there is another housekeeping running
	// i.e. `isRunning` is set to false.
	t.Run("there is no other housekeeping running but state exists", func(t *testing.T) {
		ch := make(chan struct{})

		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		manager := NewManager(gitalycfgprom.Config{}, testhelper.SharedLogger(t), nil)
		manager.optimizeFunc = func(context.Context, *RepositoryManager, log.Logger, *localrepo.Repo, OptimizationStrategy) error {
			// This should only happen if housekeeping is running successfully.
			// So by sending data on this channel we can notify the test that this
			// function ran successfully.
			ch <- struct{}{}

			return nil
		}

		// We're not acquiring a lock here, because there is no other goroutines running
		// We set the state, but make sure that isRunning is explicitly set to false. This states
		// that there is no housekeeping running currently.
		manager.repositoryStates.values[repoPath] = &refCountedState{
			state: &repositoryState{
				isRunning: false,
			},
		}

		go func() {
			require.NoError(t, manager.OptimizeRepository(ctx, repo))
		}()

		// Only if optimizeFunc is run, we shall receive data here, this acts as test that
		// housekeeping ran successfully.
		<-ch
	})

	t.Run("there is a housekeeping running state", func(t *testing.T) {
		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		manager := NewManager(gitalycfgprom.Config{}, testhelper.SharedLogger(t), nil)
		manager.optimizeFunc = func(context.Context, *RepositoryManager, log.Logger, *localrepo.Repo, OptimizationStrategy) error {
			require.FailNow(t, "housekeeping run should have been skipped")
			return nil
		}

		// we create a state before calling the OptimizeRepository function.
		ok, cleanup := manager.repositoryStates.tryRunningHousekeeping(repoPath)
		require.True(t, ok)
		// check that the state actually exists.
		require.Contains(t, manager.repositoryStates.values, repoPath)

		require.NoError(t, manager.OptimizeRepository(ctx, repo))

		// After running the cleanup, the state should be removed.
		cleanup()
		require.NotContains(t, manager.repositoryStates.values, repoPath)
	})

	t.Run("multiple repositories concurrently", func(t *testing.T) {
		reqReceivedCh, ch := make(chan struct{}), make(chan struct{})

		repoProtoFirst, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repoFirst := localrepo.NewTestRepo(t, cfg, repoProtoFirst)
		repoProtoSecond, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repoSecond := localrepo.NewTestRepo(t, cfg, repoProtoSecond)

		reposOptimized := make(map[string]struct{})

		manager := NewManager(gitalycfgprom.Config{}, testhelper.SharedLogger(t), nil)
		manager.optimizeFunc = func(_ context.Context, _ *RepositoryManager, _ log.Logger, repo *localrepo.Repo, _ OptimizationStrategy) error {
			reposOptimized[repo.GetRelativePath()] = struct{}{}

			if repo.GetRelativePath() == repoFirst.GetRelativePath() {
				reqReceivedCh <- struct{}{}
				ch <- struct{}{}
			}

			return nil
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
		repoProto, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)
		var optimizations int

		manager := NewManager(gitalycfgprom.Config{}, testhelper.SharedLogger(t), nil)
		manager.optimizeFunc = func(context.Context, *RepositoryManager, log.Logger, *localrepo.Repo, OptimizationStrategy) error {
			optimizations++

			if optimizations == 1 {
				reqReceivedCh <- struct{}{}
				ch <- struct{}{}
			}

			return nil
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
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	objectPath := func(oid git.ObjectID) string {
		return filepath.Join(repoPath, "objects", oid.String()[0:2], oid.String()[2:])
	}

	// Write two blobs, one recent blob and one blob that is older than two weeks and that would
	// thus get pruned.
	recentBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("recent"))
	staleBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("stale"))
	twoWeeksAgo := time.Now().Add(-1 * 2 * 7 * 24 * time.Hour)
	require.NoError(t, os.Chtimes(objectPath(staleBlobID), twoWeeksAgo, twoWeeksAgo))

	// We shouldn't prune when the strategy determines there aren't enough old objects.
	didPrune, err := pruneIfNeeded(ctx, repo, mockOptimizationStrategy{
		shouldPruneObjects: false,
		pruneObjectsCfg: PruneObjectsConfig{
			ExpireBefore: twoWeeksAgo,
		},
	})
	require.NoError(t, err)
	require.False(t, didPrune)

	// Consequentially, the objects shouldn't have been pruned.
	require.FileExists(t, objectPath(recentBlobID))
	require.FileExists(t, objectPath(staleBlobID))

	// But we naturally should prune if told so.
	didPrune, err = pruneIfNeeded(ctx, repo, mockOptimizationStrategy{
		shouldPruneObjects: true,
		pruneObjectsCfg: PruneObjectsConfig{
			ExpireBefore: twoWeeksAgo,
		},
	})
	require.NoError(t, err)
	require.True(t, didPrune)

	// But we should only prune the stale blob, never the recent one.
	require.FileExists(t, objectPath(recentBlobID))
	require.NoFileExists(t, objectPath(staleBlobID))
}

func TestWriteCommitGraphIfNeeded(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	t.Run("strategy does not update commit-graph", func(t *testing.T) {
		t.Parallel()

		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

		written, cfg, err := writeCommitGraphIfNeeded(ctx, repo, mockOptimizationStrategy{
			shouldWriteCommitGraph: false,
		})
		require.NoError(t, err)
		require.False(t, written)
		require.Equal(t, WriteCommitGraphConfig{}, cfg)

		require.NoFileExists(t, filepath.Join(repoPath, "objects", "info", "commit-graph"))
		require.NoDirExists(t, filepath.Join(repoPath, "objects", "info", "commit-graphs"))
	})

	t.Run("strategy does update commit-graph", func(t *testing.T) {
		t.Parallel()

		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

		written, cfg, err := writeCommitGraphIfNeeded(ctx, repo, mockOptimizationStrategy{
			shouldWriteCommitGraph: true,
		})
		require.NoError(t, err)
		require.True(t, written)
		require.Equal(t, WriteCommitGraphConfig{}, cfg)

		require.NoFileExists(t, filepath.Join(repoPath, "objects", "info", "commit-graph"))
		require.DirExists(t, filepath.Join(repoPath, "objects", "info", "commit-graphs"))
	})

	t.Run("commit-graph with pruned objects", func(t *testing.T) {
		t.Parallel()

		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
		})
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

		// Write the commit-graph incrementally.
		didWrite, writeCommitGraphCfg, err := writeCommitGraphIfNeeded(ctx, repo, mockOptimizationStrategy{
			shouldWriteCommitGraph: true,
		})
		require.NoError(t, err)
		require.True(t, didWrite)
		require.Equal(t, WriteCommitGraphConfig{}, writeCommitGraphCfg)

		// We should still observe the failure failure.
		stderr.Reset()
		verifyCmd = gittest.NewCommand(t, cfg, "-C", repoPath, "commit-graph", "verify")
		verifyCmd.Stderr = &stderr
		require.EqualError(t, verifyCmd.Run(), "exit status 1")
		require.Equal(t, stderr.String(), fmt.Sprintf("error: Could not read %[1]s\nfailed to parse commit %[1]s from object database for commit-graph\n", unreachableCommitID))

		// Write the commit-graph a second time, but this time we ask to rewrite the
		// commit-graph completely.
		didWrite, writeCommitGraphCfg, err = writeCommitGraphIfNeeded(ctx, repo, mockOptimizationStrategy{
			shouldWriteCommitGraph: true,
			writeCommitGraphCfg: WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		})
		require.NoError(t, err)
		require.True(t, didWrite)
		require.Equal(t, WriteCommitGraphConfig{
			ReplaceChain: true,
		}, writeCommitGraphCfg)

		// The commit-graph should now have been fixed.
		gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "verify")
	})
}
