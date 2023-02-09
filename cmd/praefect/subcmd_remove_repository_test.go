//go:build !gitaly_test_sha256

package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	gitalycfg "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/tick"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore/glsql"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestRemoveRepository_FlagSet(t *testing.T) {
	t.Parallel()
	cmd := &removeRepository{}
	fs := cmd.FlagSet()
	require.NoError(t, fs.Parse([]string{"--virtual-storage", "vs", "--repository", "repo"}))
	require.Equal(t, "vs", cmd.virtualStorage)
	require.Equal(t, "repo", cmd.relativePath)
}

func TestRemoveRepository_Exec_invalidArgs(t *testing.T) {
	t.Parallel()
	t.Run("not all flag values processed", func(t *testing.T) {
		cmd := removeRepository{}
		flagSet := flag.NewFlagSet("cmd", flag.PanicOnError)
		require.NoError(t, flagSet.Parse([]string{"stub"}))
		err := cmd.Exec(flagSet, config.Config{})
		require.EqualError(t, err, "cmd doesn't accept positional arguments")
	})

	t.Run("virtual-storage is not set", func(t *testing.T) {
		cmd := removeRepository{}
		err := cmd.Exec(flag.NewFlagSet("", flag.PanicOnError), config.Config{})
		require.EqualError(t, err, `"virtual-storage" is a required parameter`)
	})

	t.Run("repository is not set", func(t *testing.T) {
		cmd := removeRepository{virtualStorage: "stub"}
		err := cmd.Exec(flag.NewFlagSet("", flag.PanicOnError), config.Config{})
		require.EqualError(t, err, `"repository" is a required parameter`)
	})

	t.Run("db connection error", func(t *testing.T) {
		cmd := removeRepository{virtualStorage: "stub", relativePath: "stub"}
		cfg := config.Config{DB: config.DB{Host: "stub", SSLMode: "disable"}}
		err := cmd.Exec(flag.NewFlagSet("", flag.PanicOnError), cfg)
		require.Error(t, err)
		require.Contains(t, err.Error(), "connect to database: send ping: failed to connect to ")
	})
}

func TestRemoveRepository_Exec(t *testing.T) {
	t.Parallel()
	g1Cfg := testcfg.Build(t, testcfg.WithStorages("gitaly-1"))
	g2Cfg := testcfg.Build(t, testcfg.WithStorages("gitaly-2"))

	g1Addr := testserver.RunGitalyServer(t, g1Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
	g2Srv := testserver.StartGitalyServer(t, g2Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())

	db := testdb.New(t)

	conf := config.Config{
		SocketPath: testhelper.GetTemporaryGitalySocketFileName(t),
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "praefect",
				Nodes: []*config.Node{
					{Storage: g1Cfg.Storages[0].Name, Address: g1Addr},
					{Storage: g2Cfg.Storages[0].Name, Address: g2Srv.Address()},
				},
			},
		},
		DB: testdb.GetConfig(t, db.Name),
		Failover: config.Failover{
			Enabled:          true,
			ElectionStrategy: config.ElectionStrategyPerRepository,
		},
	}

	praefectServer := testserver.StartPraefect(t, conf)

	cc, err := client.Dial(praefectServer.Address(), nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, cc.Close()) }()
	repoClient := gitalypb.NewRepositoryServiceClient(cc)
	ctx := testhelper.Context(t)

	praefectStorage := conf.VirtualStorages[0].Name

	repositoryExists := func(tb testing.TB, repo *gitalypb.Repository) bool {
		response, err := gitalypb.NewRepositoryServiceClient(cc).RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
			Repository: repo,
		})
		require.NoError(tb, err)
		return response.GetExists()
	}

	t.Run("dry run", func(t *testing.T) {
		var out bytes.Buffer
		repo := createRepo(t, ctx, repoClient, praefectStorage, t.Name())
		replicaPath := gittest.GetReplicaPath(t, ctx, gitalycfg.Cfg{SocketPath: praefectServer.Address()}, repo)

		cmd := &removeRepository{
			logger:         testhelper.NewDiscardingLogger(t),
			virtualStorage: repo.StorageName,
			relativePath:   repo.RelativePath,
			dialTimeout:    time.Second,
			apply:          false,
			dbOnly:         false,
			w:              &writer{w: &out},
		}
		require.NoError(t, cmd.Exec(flag.NewFlagSet("", flag.PanicOnError), conf))

		require.DirExists(t, filepath.Join(g1Cfg.Storages[0].Path, replicaPath))
		require.DirExists(t, filepath.Join(g2Cfg.Storages[0].Path, replicaPath))
		assert.Contains(t, out.String(), "Repository found in the database.\n")
		assert.Contains(t, out.String(), "Re-run the command with -apply to remove repositories from the database and disk or -apply and -db-only to remove from database only.")
		require.True(t, repositoryExists(t, repo))
	})

	t.Run("ok", func(t *testing.T) {
		var out bytes.Buffer
		repo := createRepo(t, ctx, repoClient, praefectStorage, t.Name())
		replicaPath := gittest.GetReplicaPath(t, ctx, gitalycfg.Cfg{SocketPath: praefectServer.Address()}, repo)
		require.DirExists(t, filepath.Join(g1Cfg.Storages[0].Path, replicaPath))
		require.DirExists(t, filepath.Join(g2Cfg.Storages[0].Path, replicaPath))

		cmd := &removeRepository{
			logger:         testhelper.NewDiscardingLogger(t),
			virtualStorage: repo.StorageName,
			relativePath:   repo.RelativePath,
			dialTimeout:    time.Second,
			apply:          true,
			dbOnly:         false,
			w:              &writer{w: &out},
		}
		require.NoError(t, cmd.Exec(flag.NewFlagSet("", flag.PanicOnError), conf))

		require.NoDirExists(t, filepath.Join(g1Cfg.Storages[0].Path, replicaPath))
		require.NoDirExists(t, filepath.Join(g2Cfg.Storages[0].Path, replicaPath))
		assert.Contains(t, out.String(), "Repository found in the database.\n")
		assert.Contains(t, out.String(), fmt.Sprintf("Attempting to remove %s from the database, and delete it from all gitaly nodes...\n", repo.RelativePath))
		assert.Contains(t, out.String(), "Repository removal completed.")
		require.False(t, repositoryExists(t, repo))
	})

	t.Run("db only", func(t *testing.T) {
		var out bytes.Buffer
		repo := createRepo(t, ctx, repoClient, praefectStorage, t.Name())
		replicaPath := gittest.GetReplicaPath(t, ctx, gitalycfg.Cfg{SocketPath: praefectServer.Address()}, repo)

		cmd := &removeRepository{
			logger:         testhelper.NewDiscardingLogger(t),
			virtualStorage: repo.StorageName,
			relativePath:   repo.RelativePath,
			dialTimeout:    time.Second,
			apply:          true,
			dbOnly:         true,
			w:              &writer{w: &out},
		}
		require.NoError(t, cmd.Exec(flag.NewFlagSet("", flag.PanicOnError), conf))

		// Repo is no longer present in the Praefect DB.
		require.False(t, repositoryExists(t, repo))

		// Repo is still present on-disk on the Gitaly nodes.
		require.True(t, storage.IsGitDirectory(filepath.Join(g1Cfg.Storages[0].Path, replicaPath)))
		require.True(t, storage.IsGitDirectory(filepath.Join(g2Cfg.Storages[0].Path, replicaPath)))

		assert.Contains(t, out.String(), "Repository found in the database.\n")
		assert.Contains(t, out.String(), fmt.Sprintf("Attempting to remove %s from the database...\n", repo.RelativePath))
		assert.Contains(t, out.String(), "Repository removal from database completed.")
	})

	t.Run("repository doesnt exist on one gitaly", func(t *testing.T) {
		var out bytes.Buffer
		repo := createRepo(t, ctx, repoClient, praefectStorage, t.Name())
		replicaPath := gittest.GetReplicaPath(t, ctx, gitalycfg.Cfg{SocketPath: praefectServer.Address()}, repo)

		require.DirExists(t, filepath.Join(g2Cfg.Storages[0].Path, replicaPath))
		require.DirExists(t, filepath.Join(g1Cfg.Storages[0].Path, replicaPath))
		require.NoError(t, os.RemoveAll(filepath.Join(g2Cfg.Storages[0].Path, replicaPath)))

		cmd := &removeRepository{
			logger:         testhelper.NewDiscardingLogger(t),
			virtualStorage: repo.StorageName,
			relativePath:   repo.RelativePath,
			dialTimeout:    time.Second,
			apply:          true,
			dbOnly:         false,
			w:              &writer{w: &out},
		}
		require.NoError(t, cmd.Exec(flag.NewFlagSet("", flag.PanicOnError), conf))

		require.NoDirExists(t, filepath.Join(g1Cfg.Storages[0].Path, replicaPath))
		require.NoDirExists(t, filepath.Join(g2Cfg.Storages[0].Path, replicaPath))
		assert.Contains(t, out.String(), "Repository found in the database.\n")
		assert.Contains(t, out.String(), fmt.Sprintf("Attempting to remove %s from the database, and delete it from all gitaly nodes...\n", repo.RelativePath))
		assert.Contains(t, out.String(), "Repository removal completed.")
		require.False(t, repositoryExists(t, repo))
	})

	t.Run("no info about repository on praefect", func(t *testing.T) {
		var out bytes.Buffer
		repo := createRepo(t, ctx, repoClient, praefectStorage, t.Name())
		replicaPath := gittest.GetReplicaPath(t, ctx, gitalycfg.Cfg{SocketPath: praefectServer.Address()}, repo)

		repoStore := datastore.NewPostgresRepositoryStore(db.DB, nil)
		_, _, err = repoStore.DeleteRepository(ctx, repo.StorageName, repo.RelativePath)
		require.NoError(t, err)

		cmd := &removeRepository{
			logger:         testhelper.NewDiscardingLogger(t),
			virtualStorage: praefectStorage,
			relativePath:   repo.RelativePath,
			dialTimeout:    time.Second,
			apply:          true,
			dbOnly:         false,
			w:              &writer{w: &out},
		}
		require.Error(t, cmd.Exec(flag.NewFlagSet("", flag.PanicOnError), conf), "repository is not being tracked in Praefect")
		require.DirExists(t, filepath.Join(g1Cfg.Storages[0].Path, replicaPath))
		require.DirExists(t, filepath.Join(g2Cfg.Storages[0].Path, replicaPath))
		require.False(t, repositoryExists(t, repo))
	})

	t.Run("one of gitalies is out of service", func(t *testing.T) {
		var out bytes.Buffer
		repo := createRepo(t, ctx, repoClient, praefectStorage, t.Name())
		g2Srv.Shutdown()

		replicaPath := gittest.GetReplicaPath(t, ctx, gitalycfg.Cfg{SocketPath: praefectServer.Address()}, repo)
		require.DirExists(t, filepath.Join(g1Cfg.Storages[0].Path, replicaPath))
		require.DirExists(t, filepath.Join(g2Cfg.Storages[0].Path, replicaPath))

		cmd := &removeRepository{
			logger:         logrus.NewEntry(testhelper.NewDiscardingLogger(t)),
			virtualStorage: praefectStorage,
			relativePath:   repo.RelativePath,
			dialTimeout:    100 * time.Millisecond,
			apply:          true,
			dbOnly:         false,
			w:              &writer{w: &out},
		}

		require.NoError(t, cmd.Exec(flag.NewFlagSet("", flag.PanicOnError), conf))
		assert.Contains(t, out.String(), "Repository removal completed.")

		require.NoDirExists(t, filepath.Join(g1Cfg.Storages[0].Path, replicaPath))
		require.DirExists(t, filepath.Join(g2Cfg.Storages[0].Path, replicaPath))
		require.False(t, repositoryExists(t, repo))
	})
}

func TestRemoveRepository_removeReplicationEvents(t *testing.T) {
	t.Parallel()
	const (
		virtualStorage = "praefect"
		relativePath   = "relative_path/to/repo.git"
	)

	ctx := testhelper.Context(t)
	db := testdb.New(t)

	queue := datastore.NewPostgresReplicationEventQueue(db)

	// Create an event that is "in-progress" to verify that it is not removed by the command.
	inProgressEvent, err := queue.Enqueue(ctx, datastore.ReplicationEvent{
		Job: datastore.ReplicationJob{
			Change:            datastore.CreateRepo,
			VirtualStorage:    virtualStorage,
			TargetNodeStorage: "gitaly-2",
			RelativePath:      relativePath,
		},
	})
	require.NoError(t, err)
	// Dequeue the event to move it into "in_progress" state.
	dequeuedEvents, err := queue.Dequeue(ctx, virtualStorage, "gitaly-2", 10)
	require.NoError(t, err)
	require.Len(t, dequeuedEvents, 1)
	require.Equal(t, inProgressEvent.ID, dequeuedEvents[0].ID)
	require.Equal(t, datastore.JobStateInProgress, dequeuedEvents[0].State)

	// Create a second event that is "ready" to verify that it is getting removed by the
	// command.
	_, err = queue.Enqueue(ctx, datastore.ReplicationEvent{
		Job: datastore.ReplicationJob{
			Change:            datastore.UpdateRepo,
			VirtualStorage:    virtualStorage,
			TargetNodeStorage: "gitaly-3",
			SourceNodeStorage: "gitaly-1",
			RelativePath:      relativePath,
		},
	})
	require.NoError(t, err)

	// And create a third event that is in "failed" state, which should also get cleaned up.
	failedEvent, err := queue.Enqueue(ctx, datastore.ReplicationEvent{
		Job: datastore.ReplicationJob{
			Change:            datastore.UpdateRepo,
			VirtualStorage:    virtualStorage,
			TargetNodeStorage: "gitaly-4",
			SourceNodeStorage: "gitaly-0",
			RelativePath:      relativePath,
		},
	})
	require.NoError(t, err)
	// Dequeue the job to move it into "in-progress".
	dequeuedEvents, err = queue.Dequeue(ctx, virtualStorage, "gitaly-4", 10)
	require.NoError(t, err)
	require.Len(t, dequeuedEvents, 1)
	require.Equal(t, failedEvent.ID, dequeuedEvents[0].ID)
	require.Equal(t, datastore.JobStateInProgress, dequeuedEvents[0].State)
	// And then acknowledge it to move it into "failed" state.
	acknowledgedJobIDs, err := queue.Acknowledge(ctx, datastore.JobStateFailed, []uint64{failedEvent.ID})
	require.NoError(t, err)
	require.Equal(t, []uint64{failedEvent.ID}, acknowledgedJobIDs)

	resetCh := make(chan struct{})
	ticker := tick.NewManualTicker()
	ticker.ResetFunc = func() {
		resetCh <- struct{}{}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()

		// Wait for the system-under-test to execute the `Reset()` function of the ticker
		// for the first time. This means that the logic-under-test has executed once and
		// that the processing loop is blocked until we call `Tick()`.
		<-resetCh

		// Verify that the database now only contains a single job, which is the "in_progress" one.
		var jobIDs glsql.Uint64Provider
		row := db.QueryRowContext(ctx, `SELECT id FROM replication_queue`)
		assert.NoError(t, row.Scan(jobIDs.To()...))
		assert.Equal(t, []uint64{inProgressEvent.ID}, jobIDs.Values())

		// Now we acknowledge the "in_progress" job so that it will also get pruned. This
		// will also stop the processing loop as there are no more jobs left.
		acknowledgedJobIDs, err = queue.Acknowledge(ctx, datastore.JobStateCompleted, []uint64{inProgressEvent.ID})
		assert.NoError(t, err)
		assert.Equal(t, []uint64{inProgressEvent.ID}, acknowledgedJobIDs)

		// Trigger the ticker so that the processing loop becomes unblocked. This should
		// cause us to prune the now-acknowledged job.
		//
		// Note that we explicitly don't close the reset channel or try to receive another
		// message on it. This is done to ensure that we have now deterministically removed
		// the replication event and that the loop indeed has terminated as expected without
		// calling `Reset()` on the ticker again.
		ticker.Tick()
	}()

	cmd := &removeRepository{virtualStorage: virtualStorage, relativePath: relativePath}
	require.NoError(t, cmd.removeReplicationEvents(ctx, testhelper.NewDiscardingLogger(t), db.DB, ticker))

	wg.Wait()

	// And now we can finally assert that the replication queue is empty.
	var notExists bool
	row := db.QueryRow(`SELECT NOT EXISTS(SELECT FROM replication_queue)`)
	require.NoError(t, row.Scan(&notExists))
	require.True(t, notExists)
}
