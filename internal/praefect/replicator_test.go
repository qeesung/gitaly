package praefect

import (
	"context"
	"io/ioutil"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	gitaly_config "gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	gitaly_log "gitlab.com/gitlab-org/gitaly/internal/log"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
	"gitlab.com/gitlab-org/gitaly/internal/rubyserver"
	serverPkg "gitlab.com/gitlab-org/gitaly/internal/server"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestReplicate(t *testing.T) {
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

	ctx, cancel := testhelper.Context()
	defer cancel()

	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentials(testhelper.RepositoryAuthToken)),
	}
	conn, err := grpc.Dial(srvSocketPath, connOpts...)
	require.NoError(t, err)

	commitID := testhelper.CreateCommit(t, testRepoPath, "master", &testhelper.CreateCommitOpts{
		Message: "a commit",
	})

	ctx, err = helper.InjectGitalyServers(ctx, "default", srvSocketPath, testhelper.RepositoryAuthToken)
	require.NoError(t, err)

	var replicator defaultReplicator
	replicator.log = gitaly_log.Default()

	require.NoError(t, replicator.Replicate(
		ctx,
		ReplJob{
			Repository: models.Repository{
				RelativePath: testRepo.GetRelativePath(),
			},
			SourceNode: models.Node{
				Storage: "default",
			},
			TargetNode: models.Node{
				Storage: backupStorageName,
			},
		},
		conn,
		conn,
	))

	replicatedPath := filepath.Join(backupDir, filepath.Base(testRepoPath))
	testhelper.MustRunCommand(t, nil, "git", "-C", replicatedPath, "cat-file", "-e", commitID)
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

type noopReplicator struct {
	replicationLatency time.Duration
}

func (n *noopReplicator) Replicate(ctx context.Context, job ReplJob, source, target *grpc.ClientConn) error {
	time.Sleep(n.replicationLatency)
	return nil
}

func TestReplicationMetrics(t *testing.T) {
	mockReplicationLatency := &testhelper.MockHistogram{}
	mockReplicationGauge := &testhelper.MockGauge{}

	recordReplicationLatency = func(d float64) {
		mockReplicationLatency.Observe(d)
	}

	incReplicationJobsInFlight = func() {
		mockReplicationGauge.Inc()
	}

	decReplicationJobsInFlight = func() {
		mockReplicationGauge.Dec()
	}

	job := jobRecord{state: JobStateReady}

	m := &MemoryDatastore{
		jobs: &struct {
			sync.RWMutex
			records map[uint64]jobRecord // all jobs indexed by ID
		}{
			records: map[uint64]jobRecord{1: job},
		},
	}

	replJob := ReplJob{ID: 1,
		TargetNode: models.Node{Storage: "target"},
		SourceNode: models.Node{Storage: "source"},
		Repository: models.Repository{Primary: models.Node{Storage: "target"}},
		State:      JobStateReady,
	}

	coordinator := &Coordinator{nodes: make(map[string]*grpc.ClientConn)}
	coordinator.RegisterNode("source", "tcp://127.0.0.1")
	coordinator.RegisterNode("target", "tcp://127.0.0.1")

	replicationLatencyMs := 10

	replMgr := &ReplMgr{
		log:         gitaly_log.Default(),
		datastore:   m,
		coordinator: coordinator,
		replicator:  &noopReplicator{replicationLatency: time.Duration(replicationLatencyMs) * time.Millisecond},
	}

	ctx, cancel := testhelper.Context()
	defer cancel()

	require.NoError(t, replMgr.processReplJob(ctx, replJob))
	require.Equal(t, 1, mockReplicationGauge.IncrementCalled)
	require.Equal(t, 1, mockReplicationGauge.DecrementCalled)
	require.Len(t, mockReplicationLatency.Values, 1)
	require.Equal(t, replicationLatencyMs, int(mockReplicationLatency.Values[0]))
}

func runFullGitalyServer(t *testing.T) (*grpc.Server, string) {
	server := serverPkg.NewInsecure(RubyServer)
	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName()

	listener, err := net.Listen("unix", serverSocketPath)
	if err != nil {
		t.Fatal(err)
	}

	go server.Serve(listener)

	return server, "unix://" + serverSocketPath
}

var RubyServer = &rubyserver.Server{}

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	gitaly_config.Config.Auth = gitaly_config.Auth{Token: testhelper.RepositoryAuthToken}

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
