package praefect

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	gitalyconfig "gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/service/transaction"
	"gitlab.com/gitlab-org/gitaly/v14/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/v14/internal/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc"
)

type erroringRepositoryService struct {
	gitalypb.RepositoryServiceServer
}

func (svc erroringRepositoryService) RepositoryExists(context.Context, *gitalypb.RepositoryExistsRequest) (*gitalypb.RepositoryExistsResponse, error) {
	return nil, errors.New("erroring repository exists")
}

func TestVerifier(t *testing.T) {
	t.Parallel()

	// replicas contains the replicas the test setup should create, keyed by
	// virtual storage -> relative path -> storage -> exists.
	type replicas map[string]map[string]map[string]struct {
		// exists determines whether the replica exists on the gitaly or not. If false,
		// the replica is deleted but the metadata record left in place.
		exists bool
		// lastVerified is duration that has passed since the last verification.
		lastVerified time.Duration
		// isLeased determines whether the replica has a lease acquired during the test.
		isLeased bool
	}

	const (
		neverVerified       = 0
		recentlyVerified    = time.Millisecond
		pendingVerification = 30 * 24 * time.Hour
	)

	// these are the gitalys setup by the test setup
	const (
		gitaly1 = "gitaly-0"
		gitaly2 = "gitaly-1"
		gitaly3 = "gitaly-2"
	)

	type step struct {
		expectedRemovals logRecord
		expectedErrors   map[string]map[string][]string
		healthyStorages  StaticHealthChecker
		expectedReplicas map[string]map[string][]string
	}

	for _, tc := range []struct {
		desc            string
		erroringGitalys map[string]bool
		replicas        replicas
		batchSize       int
		steps           []step
	}{
		{
			desc: "all replicas exist",
			replicas: replicas{
				"virtual-storage": {
					"repository-1": {
						gitaly1: {exists: true, lastVerified: neverVerified},
						gitaly2: {exists: true, lastVerified: recentlyVerified},
						gitaly3: {exists: true, lastVerified: pendingVerification},
					},
				},
			},
			steps: []step{
				{
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly1, gitaly2, gitaly3},
						},
					},
				},
			},
		},
		{
			desc: "recently verified replicas are not picked",
			replicas: replicas{
				"virtual-storage": {
					"repository-1": {
						gitaly1: {exists: false, lastVerified: recentlyVerified},
						gitaly2: {exists: false, lastVerified: recentlyVerified},
						gitaly3: {exists: false, lastVerified: recentlyVerified},
					},
				},
			},
			steps: []step{
				{
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly1, gitaly2, gitaly3},
						},
					},
				},
			},
		},
		{
			desc: "replicas on unhealthy storages are not picked",
			replicas: replicas{
				"virtual-storage": {
					"repository-1": {
						gitaly1: {exists: true, lastVerified: neverVerified},
						gitaly2: {exists: true, lastVerified: neverVerified},
						gitaly3: {exists: false, lastVerified: neverVerified},
					},
				},
			},
			steps: []step{
				{
					healthyStorages: StaticHealthChecker{"virtual-storage": {gitaly1, gitaly2}},
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly1, gitaly2, gitaly3},
						},
					},
				},
			},
		},
		{
			desc: "metadata not deleted for replicas which errored on verification",
			erroringGitalys: map[string]bool{
				gitaly3: true,
			},
			replicas: replicas{
				"virtual-storage": {
					"repository-1": {
						gitaly1: {exists: true, lastVerified: neverVerified},
						gitaly2: {exists: true, lastVerified: neverVerified},
						gitaly3: {exists: false, lastVerified: neverVerified},
					},
				},
			},
			steps: []step{
				{
					expectedErrors: map[string]map[string][]string{
						"virtual-storage": {"repository-1": {gitaly3}},
					},
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly1, gitaly2, gitaly3},
						},
					},
				},
			},
		},
		{
			desc: "replicas with leases acquired are not picked",
			replicas: replicas{
				"virtual-storage": {
					"repository-1": {
						gitaly1: {exists: true, lastVerified: neverVerified, isLeased: true},
						gitaly2: {exists: false, lastVerified: neverVerified, isLeased: true},
						gitaly3: {exists: false, lastVerified: pendingVerification, isLeased: true},
					},
				},
			},
			steps: []step{
				{
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly1, gitaly2, gitaly3},
						},
					},
				},
			},
		},
		{
			desc: "replicas missing have their metadata records removed",
			replicas: replicas{
				"virtual-storage": {
					"repository-1": {
						gitaly1: {exists: true, lastVerified: neverVerified},
						gitaly2: {exists: false, lastVerified: neverVerified},
						gitaly3: {exists: false, lastVerified: pendingVerification},
					},
				},
			},
			steps: []step{
				{
					expectedRemovals: logRecord{
						"virtual-storage": {
							"repository-1": {gitaly2, gitaly3},
						},
					},
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly1},
						},
					},
				},
			},
		},
		{
			desc: "verification time is updated when repository exists",
			replicas: replicas{
				"virtual-storage": {
					"repository-1": {
						gitaly1: {exists: true, lastVerified: neverVerified},
						gitaly2: {exists: false, lastVerified: neverVerified},
						gitaly3: {exists: false, lastVerified: pendingVerification},
					},
				},
			},
			batchSize: 1,
			steps: []step{
				{
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly1, gitaly2, gitaly3},
						},
					},
				},
				{
					expectedRemovals: logRecord{
						"virtual-storage": {
							"repository-1": {gitaly2},
						},
					},
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly1, gitaly3},
						},
					},
				},
				{
					expectedRemovals: logRecord{
						"virtual-storage": {
							"repository-1": {gitaly3},
						},
					},
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly1},
						},
					},
				},
			},
		},
		{
			desc: "all replicas are lost",
			replicas: replicas{
				"virtual-storage": {
					"repository-1": {
						// The verification should be prioritized never verified repos
						// are first and repos that were verified a longer time ago after them.
						gitaly1: {exists: false, lastVerified: neverVerified},
						gitaly2: {exists: false, lastVerified: pendingVerification + time.Hour},
						gitaly3: {exists: false, lastVerified: pendingVerification},
					},
				},
			},
			batchSize: 1,
			steps: []step{
				{
					expectedRemovals: logRecord{
						"virtual-storage": {
							"repository-1": {gitaly1},
						},
					},
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly2, gitaly3},
						},
					},
				},
				{
					expectedRemovals: logRecord{
						"virtual-storage": {
							"repository-1": {gitaly2},
						},
					},
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly3},
						},
					},
				},
				{
					expectedRemovals: logRecord{
						"virtual-storage": {
							"repository-1": {gitaly3},
						},
					},
					expectedReplicas: map[string]map[string][]string{},
				},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			conf := config.Config{
				VirtualStorages: []*config.VirtualStorage{
					{Name: "virtual-storage"},
				},
				Failover: config.Failover{ElectionStrategy: config.ElectionStrategyPerRepository},
			}

			for i := 0; i < 3; i++ {
				storageName := fmt.Sprintf("gitaly-%d", i)

				registerFunc := setup.RegisterAll
				if tc.erroringGitalys[storageName] {
					registerFunc = func(srv *grpc.Server, deps *service.Dependencies) {
						gitalypb.RegisterRepositoryServiceServer(srv, erroringRepositoryService{repository.NewServer(
							deps.GetCfg(),
							deps.GetRubyServer(),
							deps.GetLocator(),
							deps.GetTxManager(),
							deps.GetGitCmdFactory(),
							deps.GetCatfileCache(),
							deps.GetConnsPool(),
							deps.GetGit2goExecutor(),
							deps.GetHousekeepingManager(),
						)})
					}
				}

				cfg := testcfg.Build(t, testcfg.WithStorages(storageName))
				cfg.SocketPath = testserver.RunGitalyServer(t, cfg, nil, registerFunc, testserver.WithDisablePraefect())
				conf.VirtualStorages[0].Nodes = append(conf.VirtualStorages[0].Nodes, &config.Node{
					Storage: storageName,
					Address: cfg.SocketPath,
				})
			}

			db := testdb.New(t)
			discardLogger := testhelper.NewDiscardingLogEntry(t)
			sidechannelRegistry := sidechannel.NewRegistry()
			txManager := transactions.NewManager(config.Config{})
			nodeSet, err := DialNodes(
				ctx,
				conf.VirtualStorages,
				protoregistry.GitalyProtoPreregistered,
				nil,
				backchannel.NewClientHandshaker(
					discardLogger,
					NewBackchannelServerFactory(
						discardLogger,
						transaction.NewServer(txManager),
						sidechannelRegistry,
					),
				),
				sidechannelRegistry,
			)
			require.NoError(t, err)
			t.Cleanup(nodeSet.Close)

			tx := db.Begin(t)
			t.Cleanup(func() { tx.Rollback(t) })
			testdb.SetHealthyNodes(t, ctx, tx, map[string]map[string][]string{
				"praefect-0": conf.StorageNames(),
			})
			elector := nodes.NewPerRepositoryElector(tx)
			conns := nodeSet.Connections()
			rs := datastore.NewPostgresRepositoryStore(db, conf.StorageNames())

			conn, _, cleanup := runPraefectServer(t, ctx, conf, buildOptions{
				withRouter: NewPerRepositoryRouter(
					conns,
					elector,
					StaticHealthChecker(conf.StorageNames()),
					NewLockedRandom(rand.New(rand.NewSource(0))),
					rs,
					datastore.NewAssignmentStore(db, conf.StorageNames()),
					rs,
					conf.DefaultReplicationFactors(),
				),
				withRepoStore: rs,
				withTxMgr:     txManager,
			})
			t.Cleanup(cleanup)

			// Set up the test repositories.
			for virtualStorage, relativePaths := range tc.replicas {
				for relativePath, storages := range relativePaths {
					// Create the expected repository. This creates all of the replicas transactionally.
					gittest.CreateRepository(ctx, t,
						gitalyconfig.Cfg{Storages: []gitalyconfig.Storage{{Name: virtualStorage}}},
						gittest.CreateRepositoryConfig{ClientConn: conn, RelativePath: relativePath},
					)

					// Now remove the replicas that were created in the transaction but the test case
					// expects not to exist. We remove them directly from the Gitalys so the metadata
					// records are left in place.
					for storage, replica := range storages {
						// Set the last verification time to what the test expects it to be.
						if replica.lastVerified > 0 {
							_, err := db.ExecContext(ctx, `
								UPDATE storage_repositories
								SET verified_at = now() - $4 * '1 millisecond'::interval
								FROM repositories
								WHERE storage_repositories.repository_id = repositories.repository_id
								AND   repositories.virtual_storage = $1
								AND   repositories.relative_path = $2
								AND   storage = $3`,
								virtualStorage, relativePath, storage, replica.lastVerified.Milliseconds())
							require.NoError(t, err)
						}

						// Set a lease if the test expects the record to be leased.
						if replica.isLeased {
							_, err := db.ExecContext(ctx, `
								UPDATE storage_repositories
								SET verification_leased_until = now()
								FROM repositories
								WHERE storage_repositories.repository_id = repositories.repository_id
								AND   repositories.virtual_storage = $1
								AND   repositories.relative_path = $2
								AND   storage = $3`,
								virtualStorage, relativePath, storage)
							require.NoError(t, err)
						}

						if replica.exists {
							continue
						}

						_, err := gitalypb.NewRepositoryServiceClient(conns[virtualStorage][storage]).RemoveRepository(ctx,
							&gitalypb.RemoveRepositoryRequest{
								Repository: &gitalypb.Repository{
									StorageName:  storage,
									RelativePath: relativePath,
								},
							},
						)
						require.NoError(t, err)
					}
				}
			}

			// Create a repository and lock its records to assert the dequeuer does not wait on row locks.
			gittest.CreateRepository(ctx, t,
				gitalyconfig.Cfg{Storages: []gitalyconfig.Storage{{Name: "virtual-storage"}}},
				gittest.CreateRepositoryConfig{ClientConn: conn, RelativePath: "locked-repository"},
			)

			rowLockTx := db.Begin(t)
			defer rowLockTx.Rollback(t)

			var lockedRows int
			require.NoError(t, rowLockTx.QueryRowContext(ctx, `
				WITH locked_repository AS (
					SELECT repository_id
					FROM repositories
					WHERE repositories.virtual_storage = 'virtual-storage'
					AND repositories.relative_path = 'locked-repository'
					FOR UPDATE
				),

				locked_replicas AS (
					SELECT FROM storage_repositories
					JOIN locked_repository USING (repository_id)
					FOR UPDATE
				)

				SELECT count(*) FROM locked_replicas`,
			).Scan(&lockedRows))
			require.Equal(t, 3, lockedRows)

			for _, step := range tc.steps {
				logger, hook := test.NewNullLogger()

				healthyStorages := StaticHealthChecker{"virtual-storage": []string{gitaly1, gitaly2, gitaly3}}
				if step.healthyStorages != nil {
					healthyStorages = step.healthyStorages
				}

				verifier := NewMetadataVerifier(logger, db, conns, healthyStorages, 24*7*time.Hour)
				if tc.batchSize > 0 {
					verifier.batchSize = tc.batchSize
				}

				runCtx, cancelRun := context.WithCancel(ctx)
				err = verifier.Run(runCtx, helper.NewCountTicker(1, cancelRun))
				require.Equal(t, context.Canceled, err)

				// Ensure the removals and errors are correctly logged
				var actualRemovals logRecord
				actualErrors := map[string]map[string][]string{}
				for _, entry := range hook.Entries {
					switch entry.Message {
					case "removing metadata records of non-existent replicas":
						if len(step.expectedRemovals) == 0 {
							t.Fatalf("unexpected removals logged")
						}

						actualRemovals = entry.Data["replicas"].(logRecord)
					case "failed to verify replica's existence":
						if len(step.expectedErrors) == 0 {
							t.Fatalf("unexpected errors logged")
						}

						virtualStorage := entry.Data["virtual_storage"].(string)
						relativePath := entry.Data["relative_path"].(string)

						if actualErrors[virtualStorage] == nil {
							actualErrors[virtualStorage] = map[string][]string{}
						}

						actualErrors[virtualStorage][relativePath] = append(actualErrors[virtualStorage][relativePath], entry.Data["storage"].(string))
					default:
						t.Fatalf("unexpected log message")
					}
				}

				if len(step.expectedErrors) > 0 {
					require.Equal(t, step.expectedErrors, actualErrors)
				}

				if len(step.expectedRemovals) > 0 {
					require.Equal(t, step.expectedRemovals, actualRemovals)
				}

				// The repository record should always be left in place. Otherwise data loss go unnoticed
				// when all replica records are lost.
				exists, err := gitalypb.NewRepositoryServiceClient(conn).RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
					Repository: &gitalypb.Repository{
						StorageName:  "virtual-storage",
						RelativePath: "repository-1",
					},
				})
				require.NoError(t, err)
				require.True(t, exists.GetExists())

				// Ensure all the metadata still contains the expected replicas
				require.Equal(t, step.expectedReplicas, getAllReplicas(ctx, t, db))
			}
		})
	}
}

// getAllReplicas gets all replicas from the database except for the locked-repository which is created
// by the test suite to ensure non-blocking queries.
func getAllReplicas(ctx context.Context, t testing.TB, db glsql.Querier) map[string]map[string][]string {
	rows, err := db.QueryContext(ctx, `
		SELECT repositories.virtual_storage, repositories.relative_path, storage
		FROM repositories
		JOIN storage_repositories USING (repository_id)
		WHERE repositories.relative_path != 'locked-repository'
		ORDER BY virtual_storage, relative_path, storage
	`)
	require.NoError(t, err)
	defer rows.Close()

	results := map[string]map[string][]string{}
	for rows.Next() {
		var virtualStorage, relativePath, storage string
		require.NoError(t, rows.Scan(&virtualStorage, &relativePath, &storage))

		if results[virtualStorage] == nil {
			results[virtualStorage] = map[string][]string{}
		}

		results[virtualStorage][relativePath] = append(results[virtualStorage][relativePath], storage)
	}
	require.NoError(t, rows.Err())

	return results
}
