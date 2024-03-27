package housekeeping

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestHeuristicalOptimizationStrategy_ShouldRepackObjects(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc           string
		strategy       HeuristicalOptimizationStrategy
		expectedNeeded bool
		expectedConfig config.RepackObjectsConfig
	}{
		{
			desc:     "empty repo does nothing",
			strategy: HeuristicalOptimizationStrategy{},
		},
		{
			desc: "missing bitmap",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						Count: 1,
						Bitmap: stats.BitmapInfo{
							Exists: false,
						},
					},
				},
			},
			expectedNeeded: true,
			expectedConfig: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyGeometric,
				WriteBitmap:         true,
				WriteMultiPackIndex: true,
			},
		},
		{
			desc: "missing bitmap with alternate",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						Count: 1,
						Bitmap: stats.BitmapInfo{
							Exists: false,
						},
					},
					Alternates: stats.AlternatesInfo{
						ObjectDirectories: []string{"something"},
					},
				},
			},
			// If we have no bitmap in the repository we'd normally want to fully repack
			// the repository. But because we have an alternates file we know that the
			// repository must not have a bitmap anyway, so we can skip the repack here.
			//
			// This changes though with multi-pack-indices, which allow for bitmaps to
			// exist in pooled repositories.
			expectedNeeded: true,
			expectedConfig: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyGeometric,
				WriteMultiPackIndex: true,
			},
		},
		{
			desc: "no repack needed without multi-pack-index",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						Count: 1,
						Bitmap: stats.BitmapInfo{
							Exists: true,
						},
					},
				},
			},
			expectedNeeded: true,
			expectedConfig: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyGeometric,
				WriteBitmap:         true,
				WriteMultiPackIndex: true,
			},
		},
		{
			desc: "no repack needed with multi-pack-index",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						Count: 1,
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists: true,
						},
					},
				},
			},
			expectedNeeded: false,
			expectedConfig: config.RepackObjectsConfig{},
		},
		{
			desc: "recently packed with tracked packfiles will not be repacked again",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						// We have multiple tracked packfiles, but did not
						// yet cross the 24 hour boundary. So we don't
						// expect a repack.
						Count:          2,
						LastFullRepack: time.Now().Add(-FullRepackCooldownPeriod + time.Hour),
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 2,
						},
					},
				},
			},
			expectedNeeded: false,
			expectedConfig: config.RepackObjectsConfig{},
		},
		{
			desc: "old tracked packfiles will be repacked",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						// We have multiple tracked packfiles and have
						// crossed the 24 hour boundary, so we should
						// perform a full repack.
						Count:          2,
						LastFullRepack: time.Now().Add(-FullRepackCooldownPeriod),
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 2,
						},
					},
				},
			},
			expectedNeeded: true,
			expectedConfig: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyFullWithCruft,
				WriteBitmap:         true,
				WriteMultiPackIndex: true,
			},
		},
		{
			desc: "old tracked packfiles with cruft pack will not be repacked",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						// We have multiple tracked packfiles and have
						// crossed the 24 hour boundary, so we should
						// perform a full repack.
						Count:          2,
						CruftCount:     1,
						LastFullRepack: time.Now().Add(-FullRepackCooldownPeriod),
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 2,
						},
					},
				},
			},
			expectedNeeded: false,
			expectedConfig: config.RepackObjectsConfig{},
		},
		{
			desc: "recent tracked packfiles in pool repository will be repacked",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					IsObjectPool: true,
					Packfiles: stats.PackfilesInfo{
						// Pool repositories follow the same schema as
						// normal repositories, but have a longer grace
						// period for the next repack.
						Count:          2,
						LastFullRepack: time.Now().Add(-FullRepackCooldownPeriod + time.Hour),
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 2,
						},
					},
				},
			},
			expectedNeeded: false,
			expectedConfig: config.RepackObjectsConfig{},
		},
		{
			desc: "old tracked packfiles in pool repository will be repacked",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					IsObjectPool: true,
					Packfiles: stats.PackfilesInfo{
						// Once we have crossed the grace period, pool
						// repositories should get a full repack in case
						// they have more than a single packfile.
						Count:          2,
						LastFullRepack: time.Now().Add(-FullRepackCooldownPeriod),
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 2,
						},
					},
				},
			},
			expectedNeeded: true,
			expectedConfig: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyFullWithUnreachable,
				WriteBitmap:         true,
				WriteMultiPackIndex: true,
			},
		},
		{
			desc: "few untracked packfiles will not get repacked",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						// We have 10 packfiles, of which 8 are tracked via
						// the multi-pack-index. This is "good enough", so
						// we don't expect a repack.
						Count:          10,
						Size:           10 * 1024 * 1024,
						LastFullRepack: time.Now(),
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 8,
						},
					},
				},
			},
			expectedNeeded: false,
			expectedConfig: config.RepackObjectsConfig{},
		},
		{
			desc: "many untracked packfiles will get repacked",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						// Once we have more than a certain number of
						// untracked packfiles we want to see a repack
						// though.
						Count:          10,
						Size:           10 * 1024 * 1024,
						LastFullRepack: time.Now(),
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 5,
						},
					},
				},
			},
			expectedNeeded: true,
			expectedConfig: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyGeometric,
				WriteBitmap:         true,
				WriteMultiPackIndex: true,
			},
		},
		{
			desc: "larger packfiles allow more untracked packfiles",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						// The number of allowed untracked packfiles scales
						// with the size of the repository.
						Count:          20,
						Size:           1000 * 1024 * 1024,
						LastFullRepack: time.Now(),
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 9,
						},
					},
				},
			},
			expectedNeeded: false,
			expectedConfig: config.RepackObjectsConfig{},
		},
		{
			desc: "larger packfiles with many untracked packfiles eventually repack",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						// For large repositories, the threshold of
						// untracked packfiles will eventually be reached.
						Count:          20,
						Size:           1000 * 1024 * 1024,
						LastFullRepack: time.Now(),
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 8,
						},
					},
				},
			},
			expectedNeeded: true,
			expectedConfig: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyGeometric,
				WriteBitmap:         true,
				WriteMultiPackIndex: true,
			},
		},
		{
			desc: "more tracked packfiles than exist will repack to update MIDX",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						// We have a single packfile, but 8 tracked
						// packfiles in the multi-pack-index. We shouldn't
						// ever get here, but it's nice to verify we don't
						// misbehave. Repacking is the best thing we can do
						// to fix the MIDX.
						Count:          1,
						Size:           10 * 1024,
						LastFullRepack: time.Now(),
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 8,
						},
					},
				},
			},
			expectedNeeded: true,
			expectedConfig: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyGeometric,
				WriteBitmap:         true,
				WriteMultiPackIndex: true,
			},
		},
		{
			desc: "geometric repack in object pool member with recent Git version",
			strategy: HeuristicalOptimizationStrategy{
				gitVersion: git.NewVersion(2, 40, 0, 1),
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						Count:          9,
						LastFullRepack: time.Now(),
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 1,
						},
					},
					Alternates: stats.AlternatesInfo{
						ObjectDirectories: []string{"object-pool"},
					},
				},
			},
			expectedNeeded: true,
			expectedConfig: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyGeometric,
				WriteBitmap:         false,
				WriteMultiPackIndex: true,
			},
		},
		{
			desc: "alternates modified after last full repack",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						Count: 1,
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 1,
						},
						LastFullRepack: time.Now().Add(-1 * time.Hour),
					},
					Alternates: stats.AlternatesInfo{
						LastModified: time.Now(),
					},
				},
			},
			expectedNeeded: true,
			expectedConfig: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyFullWithCruft,
				WriteBitmap:         true,
				WriteMultiPackIndex: true,
			},
		},
		{
			desc: "alternates modified before last full repack",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						Count: 1,
						MultiPackIndex: stats.MultiPackIndexInfo{
							Exists:        true,
							PackfileCount: 1,
						},
						LastFullRepack: time.Now(),
					},
					Alternates: stats.AlternatesInfo{
						LastModified: time.Now().Add(-1 * time.Hour),
					},
				},
			},
			expectedNeeded: false,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repackNeeded, repackCfg := tc.strategy.ShouldRepackObjects(ctx)
			require.Equal(t, tc.expectedNeeded, repackNeeded)
			require.Equal(t, tc.expectedConfig, repackCfg)
		})
	}

	for _, outerTC := range []struct {
		desc           string
		looseObjects   uint64
		expectedRepack bool
	}{
		{
			desc:           "no objects",
			looseObjects:   0,
			expectedRepack: false,
		},
		{
			desc:           "single object",
			looseObjects:   1,
			expectedRepack: false,
		},
		{
			desc:           "boundary",
			looseObjects:   1024,
			expectedRepack: false,
		},
		{
			desc:           "exceeding boundary should cause repack",
			looseObjects:   1025,
			expectedRepack: true,
		},
	} {
		for _, tc := range []struct {
			desc   string
			isPool bool
		}{
			{
				desc:   "normal repository",
				isPool: false,
			},
			{
				desc:   "object pool",
				isPool: true,
			},
		} {
			t.Run(tc.desc, func(t *testing.T) {
				strategy := HeuristicalOptimizationStrategy{
					info: stats.RepositoryInfo{
						IsObjectPool: tc.isPool,
						LooseObjects: stats.LooseObjectsInfo{
							Count: outerTC.looseObjects,
						},
						Packfiles: stats.PackfilesInfo{
							// We need to pretend that we have a bitmap,
							// otherwise we always do a full repack.
							Bitmap: stats.BitmapInfo{
								Exists: true,
							},
							MultiPackIndex: stats.MultiPackIndexInfo{
								Exists: true,
							},
						},
					},
				}

				repackNeeded, repackCfg := strategy.ShouldRepackObjects(ctx)
				require.Equal(t, outerTC.expectedRepack, repackNeeded)
				require.Equal(t, config.RepackObjectsConfig{
					Strategy: func() config.RepackObjectsStrategy {
						if repackNeeded {
							return config.RepackObjectsStrategyIncrementalWithUnreachable
						}
						return ""
					}(),
				}, repackCfg)
			})
		}
	}
}

func TestHeuristicalOptimizationStrategy_ShouldPruneObjects(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	expireBefore := time.Now()

	for _, tc := range []struct {
		desc                       string
		strategy                   HeuristicalOptimizationStrategy
		expectedShouldPruneObjects bool
		expectedPruneObjectsConfig PruneObjectsConfig
	}{
		{
			desc: "empty repository",
			strategy: HeuristicalOptimizationStrategy{
				expireBefore: expireBefore,
			},
			expectedShouldPruneObjects: false,
		},
		{
			desc: "only recent object",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					LooseObjects: stats.LooseObjectsInfo{
						Count: 10000,
					},
				},
				expireBefore: expireBefore,
			},
			expectedShouldPruneObjects: false,
		},
		{
			desc: "few stale objects",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					LooseObjects: stats.LooseObjectsInfo{
						StaleCount: 1000,
					},
				},
				expireBefore: expireBefore,
			},
			expectedShouldPruneObjects: false,
		},
		{
			desc: "too many stale objects",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					LooseObjects: stats.LooseObjectsInfo{
						StaleCount: 1025,
					},
				},
				expireBefore: expireBefore,
			},
			expectedShouldPruneObjects: true,
			expectedPruneObjectsConfig: PruneObjectsConfig{
				ExpireBefore: expireBefore,
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			t.Run("normal repository", func(t *testing.T) {
				shouldPrune, pruneCfg := tc.strategy.ShouldPruneObjects(ctx)
				require.Equal(t, tc.expectedShouldPruneObjects, shouldPrune)
				require.Equal(t, tc.expectedPruneObjectsConfig, pruneCfg)
			})

			t.Run("object pool", func(t *testing.T) {
				strategy := tc.strategy
				strategy.info.IsObjectPool = true

				shouldPrune, pruneCfg := tc.strategy.ShouldPruneObjects(ctx)
				require.Equal(t, tc.expectedShouldPruneObjects, shouldPrune)
				require.Equal(t, tc.expectedPruneObjectsConfig, pruneCfg)
			})
		})
	}
}

func TestHeuristicalOptimizationStrategy_ShouldRepackReferences(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	const kiloByte = 1024

	for _, tc := range []struct {
		packedRefsSize uint64
		requiredRefs   uint64
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
		t.Run("packed-refs with %d bytes", func(t *testing.T) {
			strategy := HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					References: stats.ReferencesInfo{
						ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
						PackedReferencesSize: tc.packedRefsSize,
						LooseReferencesCount: tc.requiredRefs - 1,
					},
				},
			}

			require.False(t, strategy.ShouldRepackReferences(ctx))

			strategy.info.References.LooseReferencesCount++

			require.True(t, strategy.ShouldRepackReferences(ctx))
		})
	}
}

func TestHeuristicalOptimizationStrategy_NeedsWriteCommitGraph(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc           string
		strategy       HeuristicalOptimizationStrategy
		expectedNeeded bool
		expectedCfg    config.WriteCommitGraphConfig
	}{
		{
			desc: "empty repository",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					References: stats.ReferencesInfo{
						ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
					},
				},
			},
			expectedNeeded: gittest.FilesOrReftables(false, true),
			expectedCfg:    gittest.FilesOrReftables(config.WriteCommitGraphConfig{}, config.WriteCommitGraphConfig{ReplaceChain: true}),
		},
		{
			desc: "repository with objects but no refs",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					References: stats.ReferencesInfo{
						ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
					},
					LooseObjects: stats.LooseObjectsInfo{
						Count: 9000,
					},
				},
			},
			expectedNeeded: gittest.FilesOrReftables(false, true),
			expectedCfg:    gittest.FilesOrReftables(config.WriteCommitGraphConfig{}, config.WriteCommitGraphConfig{ReplaceChain: true}),
		},
		{
			desc: "repository without bloom filters",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					References: stats.ReferencesInfo{
						ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
						LooseReferencesCount: 1,
					},
				},
			},
			expectedNeeded: true,
			expectedCfg: config.WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		},
		{
			desc: "repository without bloom filters with repack",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					References: stats.ReferencesInfo{
						ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
						LooseReferencesCount: 1,
					},
					LooseObjects: stats.LooseObjectsInfo{
						Count: 9000,
					},
				},
			},
			// When we have a valid commit-graph, but objects have been repacked, we
			// assume that there are new objects in the repository. So consequentially,
			// we should write the commit-graphs.
			expectedNeeded: true,
			expectedCfg: config.WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		},
		{
			desc: "repository with split commit-graph with bitmap without repack",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					References: stats.ReferencesInfo{
						ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
						LooseReferencesCount: 1,
					},
					CommitGraph: stats.CommitGraphInfo{
						CommitGraphChainLength: 1,
						HasBloomFilters:        true,
					},
				},
			},
			// If we have no generation data then we want to rewrite the commit-graph,
			// but only if the feature flag is enabled.
			expectedNeeded: true,
			expectedCfg: config.WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		},
		{
			desc: "repository with split commit-graph and generation data with bitmap without repack",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					References: stats.ReferencesInfo{
						ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
						LooseReferencesCount: 1,
					},
					CommitGraph: stats.CommitGraphInfo{
						CommitGraphChainLength: 1,
						HasBloomFilters:        true,
						HasGenerationData:      true,
					},
				},
			},
			// We use the information about whether we repacked objects as an indicator
			// whether something has changed in the repository. If it didn't, then we
			// assume no new objects exist and thus we don't rewrite the commit-graph.
			expectedNeeded: false,
		},
		{
			desc: "repository with monolithic commit-graph with bloom filters with repack",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					LooseObjects: stats.LooseObjectsInfo{
						Count: 9000,
					},
					References: stats.ReferencesInfo{
						ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
						LooseReferencesCount: 1,
					},
					CommitGraph: stats.CommitGraphInfo{
						HasBloomFilters: true,
					},
				},
			},
			// When we have a valid commit-graph, but objects have been repacked, we
			// assume that there are new objects in the repository. So consequentially,
			// we should write the commit-graphs.
			expectedNeeded: true,
			expectedCfg: config.WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		},
		{
			desc: "repository with monolithic commit-graph with bloom filters with pruned objects",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					LooseObjects: stats.LooseObjectsInfo{
						StaleCount: 9000,
					},
					References: stats.ReferencesInfo{
						ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
						LooseReferencesCount: 1,
					},
					CommitGraph: stats.CommitGraphInfo{
						HasBloomFilters: true,
					},
				},
			},
			// When we have a valid commit-graph, but objects have been repacked, we
			// assume that there are new objects in the repository. So consequentially,
			// we should write the commit-graphs.
			expectedNeeded: true,
			expectedCfg: config.WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		},
		{
			desc: "writing cruft packs with expiry rewrites commit graph chain",
			strategy: HeuristicalOptimizationStrategy{
				info: stats.RepositoryInfo{
					Packfiles: stats.PackfilesInfo{
						Count: 9000,
					},
					References: stats.ReferencesInfo{
						ReferenceBackendName: gittest.DefaultReferenceBackend.Name,
						LooseReferencesCount: 1,
					},
					CommitGraph: stats.CommitGraphInfo{
						CommitGraphChainLength: 1,
						HasBloomFilters:        true,
						HasGenerationData:      true,
					},
				},
				expireBefore: time.Now(),
			},
			// When we have a valid commit-graph, but objects are expired via cruft
			// packs, then some objects may be deleted and thus cause us to end up with
			// a stale commit-graph. We thus need to replace the whole chain.
			expectedNeeded: true,
			expectedCfg: config.WriteCommitGraphConfig{
				ReplaceChain: true,
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			needed, writeCommitGraphCfg, err := tc.strategy.ShouldWriteCommitGraph(ctx)
			require.NoError(t, err)
			require.Equal(t, tc.expectedNeeded, needed)
			require.Equal(t, tc.expectedCfg, writeCommitGraphCfg)
		})
	}
}

func TestEagerOptimizationStrategy(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	expireBefore := time.Now()

	for _, tc := range []struct {
		desc                     string
		strategy                 EagerOptimizationStrategy
		expectWriteBitmap        bool
		expectShouldPruneObjects bool
		pruneObjectsCfg          PruneObjectsConfig
	}{
		{
			desc: "no alternate",
			strategy: EagerOptimizationStrategy{
				expireBefore: expireBefore,
			},
			expectWriteBitmap:        true,
			expectShouldPruneObjects: true,
			pruneObjectsCfg: PruneObjectsConfig{
				ExpireBefore: expireBefore,
			},
		},
		{
			desc: "alternate",
			strategy: EagerOptimizationStrategy{
				info: stats.RepositoryInfo{
					Alternates: stats.AlternatesInfo{
						ObjectDirectories: []string{"path/to/alternate"},
					},
				},
				expireBefore: expireBefore,
			},
			expectWriteBitmap:        false,
			expectShouldPruneObjects: true,
			pruneObjectsCfg: PruneObjectsConfig{
				ExpireBefore: expireBefore,
			},
		},
		{
			desc: "object pool",
			strategy: EagerOptimizationStrategy{
				info: stats.RepositoryInfo{
					IsObjectPool: true,
				},
				expireBefore: expireBefore,
			},
			expectWriteBitmap:        true,
			expectShouldPruneObjects: false,
		},
		{
			desc: "object pool with alternate",
			strategy: EagerOptimizationStrategy{
				info: stats.RepositoryInfo{
					IsObjectPool: true,
					Alternates: stats.AlternatesInfo{
						ObjectDirectories: []string{"path/to/alternate"},
					},
				},
				expireBefore: expireBefore,
			},
			expectWriteBitmap:        false,
			expectShouldPruneObjects: false,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			var expectedExpireBefore time.Time
			if !tc.strategy.info.IsObjectPool {
				expectedExpireBefore = expireBefore
			}

			shouldRepackObjects, repackObjectsCfg := tc.strategy.ShouldRepackObjects(ctx)
			require.True(t, shouldRepackObjects)
			require.Equal(t, config.RepackObjectsConfig{
				Strategy: func() config.RepackObjectsStrategy {
					if !tc.strategy.info.IsObjectPool {
						return config.RepackObjectsStrategyFullWithCruft
					}
					return config.RepackObjectsStrategyFullWithUnreachable
				}(),
				WriteBitmap:         tc.expectWriteBitmap,
				WriteMultiPackIndex: true,
				CruftExpireBefore:   expectedExpireBefore,
			}, repackObjectsCfg)

			shouldWriteCommitGraph, writeCommitGraphCfg, err := tc.strategy.ShouldWriteCommitGraph(ctx)
			require.NoError(t, err)
			require.True(t, shouldWriteCommitGraph)
			require.Equal(t, config.WriteCommitGraphConfig{
				ReplaceChain: true,
			}, writeCommitGraphCfg)

			shouldPruneObjects, pruneObjectsCfg := tc.strategy.ShouldPruneObjects(ctx)
			require.Equal(t, tc.expectShouldPruneObjects, shouldPruneObjects)
			require.Equal(t, tc.pruneObjectsCfg, pruneObjectsCfg)

			require.True(t, tc.strategy.ShouldRepackReferences(ctx))
		})
	}
}
