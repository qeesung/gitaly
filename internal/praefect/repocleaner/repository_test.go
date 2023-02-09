//go:build !gitaly_test_sha256

package repocleaner

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/tick"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
)

func TestRunner_Run(t *testing.T) {
	t.Parallel()

	const (
		repo1RelPath = "repo-1.git"
		repo2RelPath = "repo-2.git"
		repo3RelPath = "repo-3.git"

		storage1 = "gitaly-1"
		storage2 = "gitaly-2"
		storage3 = "gitaly-3"

		virtualStorage = "praefect"
	)

	g1Cfg := testcfg.Build(t, testcfg.WithStorages(storage1))
	g2Cfg := testcfg.Build(t, testcfg.WithStorages(storage2))
	g3Cfg := testcfg.Build(t, testcfg.WithStorages(storage3))

	g1Addr := testserver.RunGitalyServer(t, g1Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
	g2Addr := testserver.RunGitalyServer(t, g2Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
	g3Addr := testserver.RunGitalyServer(t, g3Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())

	db := testdb.New(t)
	dbConf := testdb.GetConfig(t, db.Name)

	conf := config.Config{
		SocketPath: testhelper.GetTemporaryGitalySocketFileName(t),
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: virtualStorage,
				Nodes: []*config.Node{
					{Storage: g1Cfg.Storages[0].Name, Address: g1Addr},
					{Storage: g2Cfg.Storages[0].Name, Address: g2Addr},
					{Storage: g3Cfg.Storages[0].Name, Address: g3Addr},
				},
			},
		},
		DB: dbConf,
	}
	cfg := Cfg{
		RunInterval:         time.Duration(1),
		LivenessInterval:    time.Duration(1),
		RepositoriesInBatch: 2,
	}

	ctx, cancel := context.WithCancel(testhelper.Context(t))

	// each gitaly has an extra repo-4.git repository
	gittest.CreateRepository(t, ctx, g1Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           repo1RelPath,
		Seed:                   gittest.SeedGitLabTest,
	})
	gittest.CreateRepository(t, ctx, g1Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           repo2RelPath,
		Seed:                   gittest.SeedGitLabTest,
	})
	gittest.CreateRepository(t, ctx, g1Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           repo3RelPath,
		Seed:                   gittest.SeedGitLabTest,
	})
	_, repo4Path := gittest.CreateRepository(t, ctx, g1Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           "repo-4.git",
		Seed:                   gittest.SeedGitLabTest,
	})
	require.NoError(t, os.Chtimes(repo4Path, time.Now().Add(-25*time.Hour), time.Now().Add(-25*time.Hour)))

	gittest.CreateRepository(t, ctx, g2Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           repo1RelPath,
		Seed:                   gittest.SeedGitLabTest,
	})
	gittest.CreateRepository(t, ctx, g2Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           repo2RelPath,
		Seed:                   gittest.SeedGitLabTest,
	})
	_, repo4Path = gittest.CreateRepository(t, ctx, g2Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           "repo-4.git",
		Seed:                   gittest.SeedGitLabTest,
	})
	require.NoError(t, os.Chtimes(repo4Path, time.Now().Add(-25*time.Hour), time.Now().Add(-25*time.Hour)))

	gittest.CreateRepository(t, ctx, g3Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           repo1RelPath,
		Seed:                   gittest.SeedGitLabTest,
	})
	gittest.CreateRepository(t, ctx, g3Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           repo2RelPath,
		Seed:                   gittest.SeedGitLabTest,
	})
	gittest.CreateRepository(t, ctx, g3Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           repo3RelPath,
		Seed:                   gittest.SeedGitLabTest,
	})
	_, repo4Path = gittest.CreateRepository(t, ctx, g3Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           "repo-4.git",
		Seed:                   gittest.SeedGitLabTest,
	})

	require.NoError(t, os.Chtimes(repo4Path, time.Now().Add(-25*time.Hour), time.Now().Add(-25*time.Hour)))

	repoStore := datastore.NewPostgresRepositoryStore(db.DB, nil)
	for i, set := range []struct {
		relativePath string
		primary      string
		secondaries  []string
	}{
		{
			relativePath: repo1RelPath,
			primary:      storage1,
			secondaries:  []string{storage3},
		},
		{
			relativePath: repo2RelPath,
			primary:      storage1,
			secondaries:  []string{storage2, storage3},
		},
		{
			relativePath: repo3RelPath,
			primary:      storage1,
			secondaries:  []string{storage2, storage3},
		},
	} {
		require.NoError(t, repoStore.CreateRepository(ctx, int64(i), conf.VirtualStorages[0].Name, set.relativePath, set.relativePath, set.primary, set.secondaries, nil, false, false))
	}

	logger, loggerHook := test.NewNullLogger()
	logger.SetLevel(logrus.DebugLevel)

	entry := logger.WithContext(ctx)
	clientHandshaker := backchannel.NewClientHandshaker(entry, praefect.NewBackchannelServerFactory(entry, transaction.NewServer(nil), nil), backchannel.DefaultConfiguration())
	nodeSet, err := praefect.DialNodes(ctx, conf.VirtualStorages, protoregistry.GitalyProtoPreregistered, nil, clientHandshaker, nil)
	require.NoError(t, err)
	defer nodeSet.Close()

	storageCleanup := datastore.NewStorageCleanup(db.DB)

	var iteration int32
	runner := NewRunner(cfg, logger, praefect.StaticHealthChecker{virtualStorage: []string{storage1, storage2, storage3}}, nodeSet.Connections(), storageCleanup, storageCleanup, actionStub{
		PerformMethod: func(ctx context.Context, argVirtualStoage, argStorage string, notExisting []string) error {
			// There should be three iterations, as each storage has
			// one repository that is unused by praefect.
			atomic.AddInt32(&iteration, 1)

			i := atomic.LoadInt32(&iteration)
			assert.Equal(t, virtualStorage, argVirtualStoage)
			assert.Equal(t, []string{"repo-4.git"}, notExisting)

			if i == 3 {
				// Terminates the loop.
				defer cancel()
			}
			return nil
		},
	})

	ticker := tick.NewManualTicker()
	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.Equal(t, context.Canceled, runner.Run(ctx, ticker))
	}()
	// We have 3 storages that is why it requires 3 runs to cover them all.
	// As the first run happens automatically we need to make 2 ticks for 2 additional runs.
	for i := 0; i < len(conf.VirtualStorages[0].Nodes)-1; i++ {
		ticker.Tick()
	}
	waitReceive(t, done)

	require.GreaterOrEqual(t, len(loggerHook.AllEntries()), 2)
	require.Equal(
		t,
		map[string]interface{}{"Data": logrus.Fields{"component": "repocleaner.repository_existence"}, "Message": "started"},
		map[string]interface{}{"Data": loggerHook.AllEntries()[0].Data, "Message": loggerHook.AllEntries()[0].Message},
	)
	require.Equal(
		t,
		map[string]interface{}{"Data": logrus.Fields{"component": "repocleaner.repository_existence"}, "Message": "completed"},
		map[string]interface{}{"Data": loggerHook.LastEntry().Data, "Message": loggerHook.LastEntry().Message},
	)
}

func TestRunner_Run_noAvailableStorages(t *testing.T) {
	t.Parallel()

	const (
		repo1RelPath   = "repo-1.git"
		storage1       = "gitaly-1"
		virtualStorage = "praefect"
	)

	g1Cfg := testcfg.Build(t, testcfg.WithStorages(storage1))
	g1Addr := testserver.RunGitalyServer(t, g1Cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())

	db := testdb.New(t)
	dbConf := testdb.GetConfig(t, db.Name)

	conf := config.Config{
		SocketPath: testhelper.GetTemporaryGitalySocketFileName(t),
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: virtualStorage,
				Nodes: []*config.Node{
					{Storage: g1Cfg.Storages[0].Name, Address: g1Addr},
				},
			},
		},
		DB: dbConf,
	}
	cfg := Cfg{
		RunInterval:         time.Minute,
		LivenessInterval:    time.Second,
		RepositoriesInBatch: 2,
	}

	ctx, cancel := context.WithCancel(testhelper.Context(t))

	_, repoPath := gittest.CreateRepository(t, ctx, g1Cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		RelativePath:           repo1RelPath,
		Seed:                   gittest.SeedGitLabTest,
	})
	require.NoError(t, os.Chtimes(repoPath, time.Now().Add(-25*time.Hour), time.Now().Add(-25*time.Hour)))

	repoStore := datastore.NewPostgresRepositoryStore(db.DB, nil)
	for i, set := range []struct {
		relativePath string
		primary      string
	}{
		{
			relativePath: repo1RelPath,
			primary:      storage1,
		},
	} {
		require.NoError(t, repoStore.CreateRepository(ctx, int64(i), conf.VirtualStorages[0].Name, set.relativePath, set.relativePath, set.primary, nil, nil, false, false))
	}

	logger := testhelper.NewDiscardingLogger(t)
	entry := logger.WithContext(ctx)
	clientHandshaker := backchannel.NewClientHandshaker(entry, praefect.NewBackchannelServerFactory(entry, transaction.NewServer(nil), nil), backchannel.DefaultConfiguration())
	nodeSet, err := praefect.DialNodes(ctx, conf.VirtualStorages, protoregistry.GitalyProtoPreregistered, nil, clientHandshaker, nil)
	require.NoError(t, err)
	defer nodeSet.Close()

	storageCleanup := datastore.NewStorageCleanup(db.DB)
	startSecond := make(chan struct{})
	releaseFirst := make(chan struct{})
	runner := NewRunner(cfg, logger, praefect.StaticHealthChecker{virtualStorage: []string{storage1}}, nodeSet.Connections(), storageCleanup, storageCleanup, actionStub{
		PerformMethod: func(ctx context.Context, virtualStorage, storage string, notExisting []string) error {
			assert.Empty(t, notExisting)
			// Block execution here until second instance completes its execution.
			// It allows us to be sure the picked storage can't be picked once again by
			// another instance as well as that it works without problems if there is
			// nothing to pick up to process.
			close(startSecond)
			<-releaseFirst
			return nil
		},
	})

	go func() {
		// Continue execution of the first runner after the second completes.
		defer close(releaseFirst)

		logger, loggerHook := test.NewNullLogger()
		logger.SetLevel(logrus.DebugLevel)

		runner := NewRunner(cfg, logger, praefect.StaticHealthChecker{virtualStorage: []string{storage1}}, nodeSet.Connections(), storageCleanup, storageCleanup, actionStub{
			PerformMethod: func(ctx context.Context, virtualStorage, storage string, notExisting []string) error {
				assert.FailNow(t, "should not be triggered as there is no available storages to acquire")
				return nil
			},
		})
		ctx, cancel := context.WithCancel(testhelper.Context(t))
		ticker := tick.NewManualTicker()

		done := make(chan struct{})
		go func() {
			defer close(done)
			<-startSecond
			assert.Equal(t, context.Canceled, runner.Run(ctx, ticker))
		}()
		// It fills the buffer with the value and is a non-blocking call.
		ticker.Tick()
		// It blocks until buffered value is consumed, that mean the initial
		// run is completed and next one is started.
		ticker.Tick()
		cancel()
		<-done

		entries := loggerHook.AllEntries()
		require.Greater(t, len(entries), 2)
		require.Equal(
			t,
			map[string]interface{}{"Data": logrus.Fields{"component": "repocleaner.repository_existence"}, "Message": "no storages to verify"},
			map[string]interface{}{"Data": loggerHook.AllEntries()[1].Data, "Message": loggerHook.AllEntries()[1].Message},
		)
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		assert.Equal(t, context.Canceled, runner.Run(ctx, tick.NewManualTicker()))
	}()
	// Once the second runner completes we can proceed with execution of the first.
	waitReceive(t, releaseFirst)
	// Terminate the first runner.
	cancel()
	waitReceive(t, done)
}

type actionStub struct {
	PerformMethod func(ctx context.Context, virtualStorage, storage string, existence []string) error
}

func (as actionStub) Perform(ctx context.Context, virtualStorage, storage string, existence []string) error {
	if as.PerformMethod != nil {
		return as.PerformMethod(ctx, virtualStorage, storage, existence)
	}
	return nil
}

func waitReceive(tb testing.TB, waitChan <-chan struct{}) {
	tb.Helper()
	select {
	case <-waitChan:
	case <-time.After(time.Minute):
		require.FailNow(tb, "waiting for too long")
	}
}
