package praefect

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestReplicateRepositoryHandler(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	// Standalone Gitaly node for hosting source repository being replicated from.
	const sourceStorage = "gitaly-source"
	sourceCfg := testcfg.Build(t, testcfg.WithStorages(sourceStorage))
	sourceAddr := testserver.RunGitalyServer(t, sourceCfg, setup.RegisterAll, testserver.WithDisablePraefect())
	sourceCfg.SocketPath = sourceAddr

	// Gitaly node deployed behind Praefect for hosting target repository being replicated to.
	const targetStorage = "gitaly-target"
	targetCfg := testcfg.Build(t, testcfg.WithStorages(targetStorage))
	testcfg.BuildGitalyHooks(t, targetCfg)
	testcfg.BuildGitalySSH(t, targetCfg)
	targetAddr := testserver.RunGitalyServer(t, targetCfg, setup.RegisterAll, testserver.WithDisablePraefect())
	targetCfg.SocketPath = targetAddr

	// Praefect proxy which ReplicateRepository RPC routes through.
	const virtualStorage = "virtual-storage"
	praefectCfg := config.Config{
		SocketPath:  testhelper.GetTemporaryGitalySocketFileName(t),
		DB:          testdb.GetConfig(t, testdb.New(t).Name),
		Failover:    config.Failover{ElectionStrategy: config.ElectionStrategyPerRepository},
		Replication: config.DefaultReplicationConfig(),
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: virtualStorage,
				Nodes: []*config.Node{
					{Storage: targetStorage, Address: targetAddr},
				},
			},
		},
	}
	praefectServer := testserver.StartPraefect(t, praefectCfg)

	clientConn, err := grpc.DialContext(ctx, praefectServer.Address(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() {
		testhelper.MustClose(t, clientConn)
	})

	repoClient := gitalypb.NewRepositoryServiceClient(clientConn)
	objectPoolClient := gitalypb.NewObjectPoolServiceClient(clientConn)

	type setupData struct {
		source             *gitalypb.Repository
		target             *gitalypb.Repository
		expectedObjectPool *gitalypb.ObjectPool
		expectedErr        error
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "source repository is not linked to object pool",
			setup: func(t *testing.T) setupData {
				// If the source repository is not linked to an object pool, there is no need to
				// perform object pool replication.
				repoProto, _ := gittest.CreateRepository(t, ctx, sourceCfg)

				return setupData{
					source: repoProto,
					target: &gitalypb.Repository{
						StorageName:  virtualStorage,
						RelativePath: gittest.NewRepositoryName(t),
					},
					expectedObjectPool: nil,
				}
			},
		},
		{
			desc: "source repository is linked to object pool",
			setup: func(t *testing.T) setupData {
				// If the source repository is linked to an object pool and the target storage is
				// Praefect, the ReplicateRepository RPC handler should intercept and disable object
				// pool replication.
				sourceProto, _ := gittest.CreateRepository(t, ctx, sourceCfg)
				gittest.CreateObjectPool(t, ctx, sourceCfg, sourceProto, gittest.CreateObjectPoolConfig{
					LinkRepositoryToObjectPool: true,
				})

				target := &gitalypb.Repository{
					StorageName:  virtualStorage,
					RelativePath: gittest.NewRepositoryName(t),
				}

				return setupData{
					source:             sourceProto,
					target:             target,
					expectedObjectPool: nil,
				}
			},
		},
		{
			desc: "repository not set",
			setup: func(t *testing.T) setupData {
				return setupData{
					target:      nil,
					expectedErr: structerr.NewInvalidArgument("repository not set"),
				}
			},
		},
		{
			desc: "storage not set",
			setup: func(t *testing.T) setupData {
				return setupData{
					target: &gitalypb.Repository{
						StorageName:  "",
						RelativePath: gittest.NewRepositoryName(t),
					},
					expectedErr: structerr.NewInvalidArgument("storage name not set"),
				}
			},
		},
		{
			desc: "relative path not set",
			setup: func(t *testing.T) setupData {
				return setupData{
					target: &gitalypb.Repository{
						StorageName:  virtualStorage,
						RelativePath: "",
					},
					expectedErr: structerr.NewInvalidArgument("repository path not set"),
				}
			},
		},
		{
			desc: "storage and relative path not set",
			setup: func(t *testing.T) setupData {
				return setupData{
					target: &gitalypb.Repository{
						StorageName:  "",
						RelativePath: "",
					},
					expectedErr: structerr.NewInvalidArgument("repository not set"),
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			ctx := testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, sourceCfg))
			_, err := repoClient.ReplicateRepository(ctx, &gitalypb.ReplicateRepositoryRequest{
				Repository: setup.target,
				Source:     setup.source,
				// Enable object pool replication to validate if Praefect is able to rewrite the
				// message to disable it.
				ReplicateObjectDeduplicationNetworkMembership: true,
			})
			testhelper.RequireGrpcError(t, setup.expectedErr, err)
			if setup.expectedErr != nil {
				return
			}

			// Check if Praefect disabled object pool replication.
			resp, err := objectPoolClient.GetObjectPool(ctx, &gitalypb.GetObjectPoolRequest{Repository: setup.target})
			require.NoError(t, err)
			require.Equal(t, setup.expectedObjectPool, resp.GetObjectPool())
		})
	}
}
