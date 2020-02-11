package praefect

import (
	"io/ioutil"
	"log"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	gitaly_config "gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/git/objectpool"
	gitaly_log "gitlab.com/gitlab-org/gitaly/internal/log"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/conn"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	serverPkg "gitlab.com/gitlab-org/gitaly/internal/server"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/promtest"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestProcessReplicationJob(t *testing.T) {
	srv, srvSocketPath := runFullGitalyServer(t)
	defer srv.Stop()

	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	backupStorageName := "backup"

	backupDir, err := ioutil.TempDir(testhelper.GitlabTestStoragePath(), backupStorageName)
	require.NoError(t, err)

	defer func() {
		os.RemoveAll(backupDir)
	}()

	oldStorages := gitaly_config.Config.Storages
	defer func() {
		gitaly_config.Config.Storages = oldStorages
	}()

	gitaly_config.Config.Storages = append(gitaly_config.Config.Storages, gitaly_config.Storage{
		Name: backupStorageName,
		Path: backupDir,
	},
		gitaly_config.Storage{
			Name: "default",
			Path: testhelper.GitlabTestStoragePath(),
		},
	)

	config := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			&config.VirtualStorage{
				Name: "default",
				Nodes: []*models.Node{
					&models.Node{
						Storage:        "default",
						Address:        srvSocketPath,
						Token:          gitaly_config.Config.Auth.Token,
						DefaultPrimary: true,
					},
					&models.Node{
						Storage: backupStorageName,
						Address: srvSocketPath,
						Token:   gitaly_config.Config.Auth.Token,
					},
				},
			},
		},
	}

	ds := datastore.NewInMemory(config)

	// create object pool on the source
	objectPoolPath := testhelper.NewTestObjectPoolName(t)
	pool, err := objectpool.NewObjectPool(testRepo.GetStorageName(), objectPoolPath)
	require.NoError(t, err)

	poolCtx, cancel := testhelper.Context()
	defer cancel()

	require.NoError(t, pool.Create(poolCtx, testRepo))
	require.NoError(t, pool.Link(poolCtx, testRepo))

	// replicate object pool repository to target node
	targetObjectPoolRepo := *pool.ToProto().GetRepository()
	targetObjectPoolRepo.StorageName = "backup"

	ctx, cancel := testhelper.Context()
	defer cancel()

	injectedCtx := metadata.NewOutgoingContext(ctx, testhelper.GitalyServersMetadata(t, srvSocketPath))

	repoClient, con := newRepositoryClient(t, srvSocketPath)
	defer con.Close()

	_, err = repoClient.ReplicateRepository(injectedCtx, &gitalypb.ReplicateRepositoryRequest{
		Repository: &targetObjectPoolRepo,
		Source:     pool.ToProto().GetRepository(),
	})
	require.NoError(t, err)

	primary, err := ds.GetPrimary(config.VirtualStorages[0].Name)
	require.NoError(t, err)
	secondaries, err := ds.GetSecondaries(config.VirtualStorages[0].Name)
	require.NoError(t, err)

	_, err = ds.CreateReplicaReplJobs(testRepo.GetRelativePath(), primary, secondaries, datastore.UpdateRepo)
	require.NoError(t, err)

	jobs, err := ds.GetJobs(datastore.JobStateReady|datastore.JobStatePending, backupStorageName, 1)
	require.NoError(t, err)
	require.Len(t, jobs, 1)

	commitID := testhelper.CreateCommit(t, testRepoPath, "master", &testhelper.CreateCommitOpts{
		Message: "a commit",
	})

	var replicator defaultReplicator
	replicator.log = gitaly_log.Default()

	clientCC := conn.NewClientConnections()
	require.NoError(t, clientCC.RegisterNode("default", srvSocketPath, gitaly_config.Config.Auth.Token))
	require.NoError(t, clientCC.RegisterNode("backup", srvSocketPath, gitaly_config.Config.Auth.Token))

	var mockReplicationGauge promtest.MockGauge
	var mockReplicationHistogram promtest.MockHistogram

	replMgr := NewReplMgr("", gitaly_log.Default(), ds, clientCC, WithLatencyMetric(&mockReplicationHistogram), WithQueueMetric(&mockReplicationGauge))
	replMgr.replicator = replicator

	replMgr.processReplJob(ctx, jobs[0])

	relativeRepoPath, err := filepath.Rel(testhelper.GitlabTestStoragePath(), testRepoPath)
	require.NoError(t, err)
	replicatedPath := filepath.Join(backupDir, relativeRepoPath)

	testhelper.MustRunCommand(t, nil, "git", "-C", replicatedPath, "cat-file", "-e", commitID)
	testhelper.MustRunCommand(t, nil, "git", "-C", replicatedPath, "gc")
	require.Less(t, testhelper.GetGitPackfileDirSize(t, replicatedPath), int64(100), "expect a small pack directory")

	require.Equal(t, 1, mockReplicationGauge.IncsCalled())
	require.Equal(t, 1, mockReplicationGauge.DecsCalled())
	require.Len(t, mockReplicationHistogram.Values, 1)
}

func TestConfirmReplication(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	srv, srvSocketPath := runFullGitalyServer(t)
	defer srv.Stop()

	testRepoA, testRepoAPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	testRepoB, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentials(testhelper.RepositoryAuthToken)),
	}
	conn, err := grpc.Dial(srvSocketPath, connOpts...)
	require.NoError(t, err)

	var replicator defaultReplicator
	replicator.log = gitaly_log.Default()

	equal, err := replicator.confirmChecksums(ctx, gitalypb.NewRepositoryServiceClient(conn), gitalypb.NewRepositoryServiceClient(conn), testRepoA, testRepoB)
	require.NoError(t, err)
	require.True(t, equal)

	testhelper.CreateCommit(t, testRepoAPath, "master", &testhelper.CreateCommitOpts{
		Message: "a commit",
	})

	equal, err = replicator.confirmChecksums(ctx, gitalypb.NewRepositoryServiceClient(conn), gitalypb.NewRepositoryServiceClient(conn), testRepoA, testRepoB)
	require.NoError(t, err)
	require.False(t, equal)
}

func TestProcessBacklog(t *testing.T) {
	srv, srvSocketPath := runFullGitalyServer(t)
	defer srv.Stop()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	backupStorageName := "backup"

	backupDir, err := ioutil.TempDir(testhelper.GitlabTestStoragePath(), backupStorageName)
	require.NoError(t, err)

	defer os.RemoveAll(backupDir)

	oldStorages := gitaly_config.Config.Storages
	defer func() {
		gitaly_config.Config.Storages = oldStorages
	}()

	gitaly_config.Config.Storages = append(gitaly_config.Config.Storages, gitaly_config.Storage{
		Name: backupStorageName,
		Path: backupDir,
	},
		gitaly_config.Storage{
			Name: "default",
			Path: testhelper.GitlabTestStoragePath(),
		},
	)

	primary := models.Node{
		Storage:        "default",
		Address:        srvSocketPath,
		Token:          gitaly_config.Config.Auth.Token,
		DefaultPrimary: true,
	}

	secondary := models.Node{
		Storage: backupStorageName,
		Address: srvSocketPath,
		Token:   gitaly_config.Config.Auth.Token,
	}

	config := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "default",
				Nodes: []*models.Node{
					&primary,
					&secondary,
				},
			},
		},
	}

	ds := datastore.NewInMemory(config)
	ids, err := ds.CreateReplicaReplJobs(testRepo.GetRelativePath(), primary, []models.Node{secondary}, datastore.UpdateRepo)
	require.NoError(t, err)
	require.Len(t, ids, 1)

	require.NoError(t, ds.UpdateReplJobState(ids[0], datastore.JobStateReady))

	clientCC := conn.NewClientConnections()
	require.NoError(t, clientCC.RegisterNode("default", srvSocketPath, gitaly_config.Config.Auth.Token))

	replMgr := NewReplMgr(backupStorageName, gitaly_log.Default(), ds, clientCC)
	ctx, cancel := testhelper.Context()
	defer cancel()

	go replMgr.ProcessBacklog(ctx, noopBackoffFunc)

	timeLimit := time.NewTimer(5 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)

	// the job will fail to process because the client connection for "backup" is not registered. It should fail maxAttempts times
	// and get cancelled.
TestJobGetsCancelled:
	for {
		select {
		case <-ticker.C:
			replJobs, err := ds.GetJobs(datastore.JobStateDead, "backup", 10)
			require.NoError(t, err)
			if len(replJobs) == 1 {
				//success
				break TestJobGetsCancelled
			}
		case <-timeLimit.C:
			t.Fatal("time limit expired for job to complete")
		}
	}

	require.NoError(t, clientCC.RegisterNode("backup", srvSocketPath, gitaly_config.Config.Auth.Token))
	ids, err = ds.CreateReplicaReplJobs(testRepo.GetRelativePath(), primary, []models.Node{secondary}, datastore.UpdateRepo)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	require.NoError(t, ds.UpdateReplJobState(ids[0], datastore.JobStateReady))
	timeLimit.Reset(5 * time.Second)

	// Once the node is registered, and we try the job again it should succeed
TestJobSucceeds:
	for {
		select {
		case <-ticker.C:
			replJobs, err := ds.GetJobs(datastore.JobStateFailed|datastore.JobStateInProgress|datastore.JobStateReady, "backup", 10)
			require.NoError(t, err)
			if len(replJobs) == 0 {
				//success
				break TestJobSucceeds
			}
		case <-timeLimit.C:
			t.Error("time limit expired for job to complete")
		}
	}
}

func TestBackoff(t *testing.T) {
	start := 1 * time.Microsecond
	max := 6 * time.Microsecond
	expectedBackoffs := []time.Duration{
		1 * time.Microsecond,
		2 * time.Microsecond,
		4 * time.Microsecond,
		6 * time.Microsecond,
		6 * time.Microsecond,
		6 * time.Microsecond,
	}
	b, reset := ExpBackoffFunc(start, max)()
	for _, expectedBackoff := range expectedBackoffs {
		require.Equal(t, expectedBackoff, b())
	}

	reset()
	require.Equal(t, start, b())
}

func runFullGitalyServer(t *testing.T) (*grpc.Server, string) {
	server := serverPkg.NewInsecure(RubyServer)
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()

	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}
	//listen on internal socket
	internalListener, err := net.Listen("unix", gitaly_config.GitalyInternalSocketPath())
	require.NoError(t, err)

	go server.Serve(listener)
	go server.Serve(internalListener)

	return server, "unix://" + serverSocketPath
}

func newRepositoryClient(t *testing.T, serverSocketPath string) (gitalypb.RepositoryServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentials(testhelper.RepositoryAuthToken)),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewRepositoryServiceClient(conn), conn
}

var RubyServer = &rubyserver.Server{}

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	gitaly_config.Config.Auth.Token = testhelper.RepositoryAuthToken

	var err error
	gitaly_config.Config.GitlabShell.Dir, err = filepath.Abs("testdata/gitlab-shell")
	if err != nil {
		log.Fatal(err)
	}

	testhelper.ConfigureGitalySSH()

	if err := RubyServer.Start(); err != nil {
		log.Fatal(err)
	}
	defer RubyServer.Stop()

	return m.Run()
}
