//go:build !gitaly_test_sha256

package praefect

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	gitalyconfig "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/tick"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/transactions"
	"gitlab.com/gitlab-org/gitaly/v15/internal/sidechannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
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
		expectedReplicas map[string]map[string][]string
	}

	for _, tc := range []struct {
		desc                 string
		dontPerformDeletions bool
		erroringGitalys      map[string]bool
		replicas             replicas
		healthyStorages      StaticHealthChecker
		batchSize            int
		steps                []step
		dequeuedJobsTotal    map[string]map[string]int
		completedJobsTotal   map[string]map[string]map[string]int
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
			dequeuedJobsTotal: map[string]map[string]int{
				"virtual-storage": {
					gitaly1: 1,
					gitaly3: 1,
				},
			},
			completedJobsTotal: map[string]map[string]map[string]int{
				"virtual-storage": {
					gitaly1: {"valid": 1},
					gitaly3: {"valid": 1},
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
			healthyStorages: StaticHealthChecker{"virtual-storage": {gitaly1, gitaly2}},
			steps: []step{
				{
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly1, gitaly2, gitaly3},
						},
					},
				},
			},
			dequeuedJobsTotal: map[string]map[string]int{
				"virtual-storage": {
					gitaly1: 1,
					gitaly2: 1,
				},
			},
			completedJobsTotal: map[string]map[string]map[string]int{
				"virtual-storage": {
					gitaly1: {"valid": 1},
					gitaly2: {"valid": 1},
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
			dequeuedJobsTotal: map[string]map[string]int{
				"virtual-storage": {
					gitaly1: 1,
					gitaly2: 1,
					gitaly3: 1,
				},
			},
			completedJobsTotal: map[string]map[string]map[string]int{
				"virtual-storage": {
					gitaly1: {"valid": 1},
					gitaly2: {"valid": 1},
					gitaly3: {"error": 1},
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
			dequeuedJobsTotal: map[string]map[string]int{
				"virtual-storage": {
					gitaly1: 1,
					gitaly2: 1,
					gitaly3: 1,
				},
			},
			completedJobsTotal: map[string]map[string]map[string]int{
				"virtual-storage": {
					gitaly1: {"valid": 1},
					gitaly2: {"invalid": 1},
					gitaly3: {"invalid": 1},
				},
			},
		},
		{
			desc:                 "metadata is not deleted if deletions are disabled",
			dontPerformDeletions: true,
			batchSize:            2,
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
							"repository-1": {gitaly2},
						},
					},
					expectedReplicas: map[string]map[string][]string{
						"virtual-storage": {
							"repository-1": {gitaly1, gitaly2, gitaly3},
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
							"repository-1": {gitaly1, gitaly2, gitaly3},
						},
					},
				},
			},
			dequeuedJobsTotal: map[string]map[string]int{
				"virtual-storage": {
					gitaly1: 1,
					gitaly2: 1,
					gitaly3: 1,
				},
			},
			completedJobsTotal: map[string]map[string]map[string]int{
				"virtual-storage": {
					gitaly1: {"valid": 1},
					gitaly2: {"invalid": 1},
					gitaly3: {"invalid": 1},
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
			dequeuedJobsTotal: map[string]map[string]int{
				"virtual-storage": {
					gitaly1: 1,
					gitaly2: 1,
					gitaly3: 1,
				},
			},
			completedJobsTotal: map[string]map[string]map[string]int{
				"virtual-storage": {
					gitaly1: {"valid": 1},
					gitaly2: {"invalid": 1},
					gitaly3: {"invalid": 1},
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
			dequeuedJobsTotal: map[string]map[string]int{
				"virtual-storage": {
					gitaly1: 1,
					gitaly2: 1,
					gitaly3: 1,
				},
			},
			completedJobsTotal: map[string]map[string]map[string]int{
				"virtual-storage": {
					gitaly1: {"invalid": 1},
					gitaly2: {"invalid": 1},
					gitaly3: {"invalid": 1},
				},
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

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
					backchannel.DefaultConfiguration(),
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

			conn, _, cleanup := RunPraefectServer(t, ctx, conf, BuildOptions{
				WithRouter: NewPerRepositoryRouter(
					conns,
					elector,
					StaticHealthChecker(conf.StorageNames()),
					NewLockedRandom(rand.New(rand.NewSource(0))),
					rs,
					datastore.NewAssignmentStore(db, conf.StorageNames()),
					rs,
					conf.DefaultReplicationFactors(),
				),
				WithRepoStore: rs,
				WithTxMgr:     txManager,
			})
			t.Cleanup(cleanup)

			// Set up the test repositories.
			for virtualStorage, relativePaths := range tc.replicas {
				for relativePath, storages := range relativePaths {
					// Create the expected repository. This creates all of the replicas transactionally.
					repo, _ := gittest.CreateRepository(t, ctx,
						gitalyconfig.Cfg{Storages: []gitalyconfig.Storage{{Name: virtualStorage}}},
						gittest.CreateRepositoryConfig{ClientConn: conn, RelativePath: relativePath},
					)

					replicaPath := gittest.GetReplicaPath(t, ctx, gitalyconfig.Cfg{}, repo, gittest.GetReplicaPathConfig{ClientConn: conn})

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
									RelativePath: replicaPath,
								},
							},
						)
						require.NoError(t, err)
					}
				}
			}

			// Create a repository and lock its records to assert the dequeuer does not wait on row locks.
			gittest.CreateRepository(t, ctx,
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

			logger, hook := test.NewNullLogger()

			healthyStorages := StaticHealthChecker{"virtual-storage": []string{gitaly1, gitaly2, gitaly3}}
			if tc.healthyStorages != nil {
				healthyStorages = tc.healthyStorages
			}

			verifier := NewMetadataVerifier(logger, db, conns, healthyStorages, 24*7*time.Hour, !tc.dontPerformDeletions)
			if tc.batchSize > 0 {
				verifier.batchSize = tc.batchSize
			}

			for _, step := range tc.steps {
				hook.Reset()

				runCtx, cancelRun := context.WithCancel(ctx)
				err = verifier.Run(runCtx, tick.NewCountTicker(1, cancelRun))
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
				require.Equal(t, step.expectedReplicas, getAllReplicas(t, ctx, db))
			}

			require.NoError(t, testutil.CollectAndCompare(verifier, strings.NewReader(fmt.Sprintf(`
# HELP gitaly_praefect_stale_verification_leases_released_total Number of stale verification leases released.
# TYPE gitaly_praefect_stale_verification_leases_released_total counter
gitaly_praefect_stale_verification_leases_released_total 0
# HELP gitaly_praefect_verification_jobs_completed_total Number of verification jobs completed and their result
# TYPE gitaly_praefect_verification_jobs_completed_total counter
gitaly_praefect_verification_jobs_completed_total{result="error",storage="gitaly-0",virtual_storage="virtual-storage"} %d
gitaly_praefect_verification_jobs_completed_total{result="error",storage="gitaly-1",virtual_storage="virtual-storage"} %d
gitaly_praefect_verification_jobs_completed_total{result="error",storage="gitaly-2",virtual_storage="virtual-storage"} %d
gitaly_praefect_verification_jobs_completed_total{result="invalid",storage="gitaly-0",virtual_storage="virtual-storage"} %d
gitaly_praefect_verification_jobs_completed_total{result="invalid",storage="gitaly-1",virtual_storage="virtual-storage"} %d
gitaly_praefect_verification_jobs_completed_total{result="invalid",storage="gitaly-2",virtual_storage="virtual-storage"} %d
gitaly_praefect_verification_jobs_completed_total{result="valid",storage="gitaly-0",virtual_storage="virtual-storage"} %d
gitaly_praefect_verification_jobs_completed_total{result="valid",storage="gitaly-1",virtual_storage="virtual-storage"} %d
gitaly_praefect_verification_jobs_completed_total{result="valid",storage="gitaly-2",virtual_storage="virtual-storage"} %d
# HELP gitaly_praefect_verification_jobs_dequeued_total Number of verification jobs dequeud.
# TYPE gitaly_praefect_verification_jobs_dequeued_total counter
gitaly_praefect_verification_jobs_dequeued_total{storage="gitaly-0",virtual_storage="virtual-storage"} %d
gitaly_praefect_verification_jobs_dequeued_total{storage="gitaly-1",virtual_storage="virtual-storage"} %d
gitaly_praefect_verification_jobs_dequeued_total{storage="gitaly-2",virtual_storage="virtual-storage"} %d
			`,
				tc.completedJobsTotal["virtual-storage"][gitaly1][resultError],
				tc.completedJobsTotal["virtual-storage"][gitaly2][resultError],
				tc.completedJobsTotal["virtual-storage"][gitaly3][resultError],
				tc.completedJobsTotal["virtual-storage"][gitaly1][resultInvalid],
				tc.completedJobsTotal["virtual-storage"][gitaly2][resultInvalid],
				tc.completedJobsTotal["virtual-storage"][gitaly3][resultInvalid],
				tc.completedJobsTotal["virtual-storage"][gitaly1][resultValid],
				tc.completedJobsTotal["virtual-storage"][gitaly2][resultValid],
				tc.completedJobsTotal["virtual-storage"][gitaly3][resultValid],
				tc.dequeuedJobsTotal["virtual-storage"][gitaly1],
				tc.dequeuedJobsTotal["virtual-storage"][gitaly2],
				tc.dequeuedJobsTotal["virtual-storage"][gitaly3],
			))))
		})
	}
}

// getAllReplicas gets all replicas from the database except for the locked-repository which is created
// by the test suite to ensure non-blocking queries.
func getAllReplicas(tb testing.TB, ctx context.Context, db glsql.Querier) map[string]map[string][]string {
	rows, err := db.QueryContext(ctx, `
		SELECT repositories.virtual_storage, repositories.relative_path, storage
		FROM repositories
		JOIN storage_repositories USING (repository_id)
		WHERE repositories.relative_path != 'locked-repository'
		ORDER BY virtual_storage, relative_path, storage
	`)
	require.NoError(tb, err)
	defer rows.Close()

	results := map[string]map[string][]string{}
	for rows.Next() {
		var virtualStorage, relativePath, storage string
		require.NoError(tb, rows.Scan(&virtualStorage, &relativePath, &storage))

		if results[virtualStorage] == nil {
			results[virtualStorage] = map[string][]string{}
		}

		results[virtualStorage][relativePath] = append(results[virtualStorage][relativePath], storage)
	}
	require.NoError(tb, rows.Err())

	return results
}

func TestVerifier_runExpiredLeaseReleaser(t *testing.T) {
	ctx := testhelper.Context(t)

	db := testdb.New(t)

	rs := datastore.NewPostgresRepositoryStore(db, nil)
	require.NoError(t,
		rs.CreateRepository(ctx, 1, "virtual-storage-1", "relative-path-1", "replica-path-1", "no-lease", []string{
			"valid-lease", "expired-lease-1", "expired-lease-2", "expired-lease-3", "locked-lease",
		}, nil, true, true),
	)

	// lock one lease record to ensure the worker doesn't block on it
	result, err := db.ExecContext(ctx, `
		UPDATE storage_repositories
		SET verification_leased_until = now() - interval '1 week'
		WHERE storage = 'locked-lease'
	`)
	require.NoError(t, err)
	locked, err := result.RowsAffected()
	require.NoError(t, err)
	require.Equal(t, int64(1), locked)

	lockingTx := db.Begin(t)
	defer lockingTx.Rollback(t)
	_, err = lockingTx.ExecContext(ctx, "SELECT FROM storage_repositories WHERE storage = 'locked-lease' FOR UPDATE")
	require.NoError(t, err)

	tx := db.Begin(t)
	defer tx.Rollback(t)

	logger, hook := test.NewNullLogger()
	verifier := NewMetadataVerifier(logrus.NewEntry(logger), tx, nil, nil, 0, true)
	// set batch size lower than the number of locked leases to ensure the batching works
	verifier.batchSize = 2

	_, err = tx.ExecContext(ctx, `
			UPDATE storage_repositories
			SET verification_leased_until = lease_time
			FROM ( VALUES
				( 'valid-lease', now() - $1 * interval '1 microsecond' ),
				( 'expired-lease-1', now() - $1 * interval '1 microsecond' - interval '1 microsecond'),
				( 'expired-lease-2', now() - $1 * interval '2 microsecond'),
				( 'expired-lease-3', now() - $1 * interval '2 microsecond')
			) AS fixture (storage, lease_time)
			WHERE storage_repositories.storage = fixture.storage
		`, verifier.leaseDuration.Microseconds())
	require.NoError(t, err)

	runCtx, cancelRun := context.WithCancel(ctx)
	ticker := tick.NewCountTicker(1, cancelRun)

	err = verifier.RunExpiredLeaseReleaser(runCtx, ticker)
	require.Equal(t, context.Canceled, err)

	// actualReleased contains the released leases from the logs. It's keyed
	// as virtual storage -> relative path -> storage.
	actualReleased := map[string]map[string]map[string]struct{}{}
	require.Equal(t, 2, len(hook.AllEntries()))
	for i := range hook.Entries {
		require.Equal(t, "released stale verification leases", hook.Entries[i].Message, hook.Entries[i].Data[logrus.ErrorKey])
		for virtualStorage, relativePaths := range hook.Entries[i].Data["leases_released"].(map[string]map[string][]string) {
			for relativePath, storages := range relativePaths {
				for _, storage := range storages {
					if actualReleased[virtualStorage] == nil {
						actualReleased[virtualStorage] = map[string]map[string]struct{}{}
					}

					if actualReleased[virtualStorage][relativePath] == nil {
						actualReleased[virtualStorage][relativePath] = map[string]struct{}{}
					}

					actualReleased[virtualStorage][relativePath][storage] = struct{}{}
				}
			}
		}
	}

	require.Equal(t, map[string]map[string]map[string]struct{}{
		"virtual-storage-1": {"relative-path-1": {"expired-lease-1": {}, "expired-lease-2": {}, "expired-lease-3": {}}},
	}, actualReleased)
	require.NoError(t, testutil.CollectAndCompare(verifier, strings.NewReader(`
# HELP gitaly_praefect_stale_verification_leases_released_total Number of stale verification leases released.
# TYPE gitaly_praefect_stale_verification_leases_released_total counter
gitaly_praefect_stale_verification_leases_released_total 3
	`)))
}
