package storagemgr_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestMiddleware_partitioning_hint(t *testing.T) {
	testhelper.SkipWithPraefect(t, `
Partitioning hints are currently only sent by Praefect. We don't support sending partitioning hints
through Praefect yet as we don't have a use case yet. Skip the test if Praefect running in front
of the Gitaly.
	`)

	if !testhelper.IsWALEnabled() {
		t.Skip("This is testing the partitioning behavior specifically. No point running the test without transactions.")
	}

	t.Parallel()

	for _, tc := range []struct {
		desc       string
		createFork func(t *testing.T, ctx context.Context, cfg config.Cfg, alternate *gitalypb.Repository) *gitalypb.Repository
	}{
		{
			desc: "partitioning hint provided",
			createFork: func(t *testing.T, ctx context.Context, cfg config.Cfg, alternate *gitalypb.Repository) *gitalypb.Repository {
				fork, _ := gittest.CreateRepository(t,
					metadata.IncomingToOutgoing(storagectx.SetPartitioningHintToIncomingContext(ctx, alternate.RelativePath)),
					cfg,
				)
				return fork
			},
		},
		{
			desc: "implicit CreateFork hint",
			createFork: func(t *testing.T, ctx context.Context, cfg config.Cfg, alternate *gitalypb.Repository) *gitalypb.Repository {
				cc, err := client.Dial(ctx, cfg.ListenAddr)
				require.NoError(t, err)
				defer testhelper.MustClose(t, cc)

				fork := &gitalypb.Repository{
					StorageName:  alternate.StorageName,
					RelativePath: gittest.NewRepositoryName(t),
				}

				_, err = gitalypb.NewRepositoryServiceClient(cc).CreateFork(
					testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg)),
					&gitalypb.CreateForkRequest{
						Repository:       fork,
						SourceRepository: alternate,
					})
				require.NoError(t, err)

				return fork
			},
		},
		{
			desc: "explicit partitioning hint with implicit CreateFork hint",
			createFork: func(t *testing.T, ctx context.Context, cfg config.Cfg, alternate *gitalypb.Repository) *gitalypb.Repository {
				cc, err := client.Dial(ctx, cfg.ListenAddr)
				require.NoError(t, err)
				defer testhelper.MustClose(t, cc)

				fork := &gitalypb.Repository{
					StorageName:  alternate.StorageName,
					RelativePath: gittest.NewRepositoryName(t),
				}

				sourceRepository, _ := gittest.CreateRepository(t, ctx, cfg)

				_, err = gitalypb.NewRepositoryServiceClient(cc).CreateFork(
					testhelper.MergeOutgoingMetadata(
						metadata.IncomingToOutgoing(storagectx.SetPartitioningHintToIncomingContext(ctx, alternate.RelativePath)),
						testcfg.GitalyServersMetadataFromCfg(t, cfg),
					),
					&gitalypb.CreateForkRequest{
						Repository: fork,
						// We're using different source repository than the object pool we'll link to at the end of the test.
						// The linking would fail at the end if the repository was partitioned implicitly with the source repository
						// instead of the the explicitly hinted repository.
						SourceRepository: sourceRepository,
					})
				require.NoError(t, err)

				return fork
			},
		},
		{
			desc: "explicit partitioning hint with an additional repository fails",
			createFork: func(t *testing.T, ctx context.Context, cfg config.Cfg, alternate *gitalypb.Repository) *gitalypb.Repository {
				cc, err := client.Dial(ctx, cfg.ListenAddr)
				require.NoError(t, err)
				defer testhelper.MustClose(t, cc)

				_, err = gitalypb.NewObjectPoolServiceClient(cc).CreateObjectPool(
					testhelper.MergeOutgoingMetadata(
						metadata.IncomingToOutgoing(storagectx.SetPartitioningHintToIncomingContext(ctx, alternate.RelativePath)),
						testcfg.GitalyServersMetadataFromCfg(t, cfg),
					),
					&gitalypb.CreateObjectPoolRequest{
						ObjectPool: &gitalypb.ObjectPool{
							Repository: &gitalypb.Repository{
								StorageName:  alternate.StorageName,
								RelativePath: gittest.NewObjectPoolName(t),
							},
						},
						Origin: alternate,
					})
				testhelper.RequireGrpcError(t, storagemgr.ErrPartitioningHintAndAdditionalRepoProvided, err)

				return nil
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			cfg := testcfg.Build(t)
			cfg.ListenAddr = testserver.RunGitalyServer(t, cfg, setup.RegisterAll)

			testcfg.BuildGitalySSH(t, cfg)

			ctx := testhelper.Context(t)
			alternate, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				RelativePath: gittest.NewObjectPoolName(t),
			})

			cc, err := client.Dial(ctx, cfg.ListenAddr)
			require.NoError(t, err)
			defer testhelper.MustClose(t, cc)

			fork := tc.createFork(t, testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg)), cfg, alternate)
			if fork == nil {
				// End the test if we we're testing error handling in createFork.
				return
			}

			// If the repositories are in the same partition, it should be possible to link them.
			_, err = gitalypb.NewObjectPoolServiceClient(cc).LinkRepositoryToObjectPool(ctx, &gitalypb.LinkRepositoryToObjectPoolRequest{
				Repository: fork,
				ObjectPool: &gitalypb.ObjectPool{Repository: alternate},
			})
			require.NoError(t, err)
		})
	}
}
