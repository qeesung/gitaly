package praefect

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/proxy"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestRemoveAllHandler(t *testing.T) {
	testhelper.SkipWithWAL(t, `
RemoveAll is removing the entire content of the storage. This would also remove the database's and
the transaction manager's disk state. The RPC needs to be updated to shut down all partitions and
the database and only then perform the removal.

Issue: https://gitlab.com/gitlab-org/gitaly/-/issues/5269`)

	t.Parallel()
	ctx := testhelper.Context(t)

	const virtualStorage, relativePath = "virtual-storage", "relative-path"

	errServedByGitaly := structerr.NewInternal("request passed to Gitaly")
	db := testdb.New(t)

	const gitaly1Storage = "gitaly-1"
	gitaly1Cfg := testcfg.Build(t, testcfg.WithStorages(gitaly1Storage))
	gitaly1RepoPath := filepath.Join(gitaly1Cfg.Storages[0].Path, relativePath)
	gitaly1Addr := testserver.RunGitalyServer(t, gitaly1Cfg, setup.RegisterAll, testserver.WithDisablePraefect())

	const gitaly2Storage = "gitaly-2"
	gitaly2Cfg := testcfg.Build(t, testcfg.WithStorages(gitaly2Storage))
	gitaly2RepoPath := filepath.Join(gitaly2Cfg.Storages[0].Path, relativePath)
	gitaly2Addr := testserver.RunGitalyServer(t, gitaly2Cfg, setup.RegisterAll, testserver.WithDisablePraefect())

	cfg := config.Config{VirtualStorages: []*config.VirtualStorage{
		{
			Name: virtualStorage,
			Nodes: []*config.Node{
				{Storage: gitaly1Storage, Address: gitaly1Addr},
				{Storage: gitaly2Storage, Address: gitaly2Addr},
			},
		},
	}}

	for _, repoPath := range []string{gitaly1RepoPath, gitaly2RepoPath} {
		gittest.Exec(t, gitaly1Cfg, "init", "--bare", repoPath)
	}

	rs := datastore.NewPostgresRepositoryStore(db, cfg.StorageNames())

	tmp := testhelper.TempDir(t)

	ln, err := net.Listen("unix", filepath.Join(tmp, "praefect"))
	require.NoError(t, err)

	nodeSet, err := DialNodes(ctx, cfg.VirtualStorages, nil, nil, nil, nil, testhelper.SharedLogger(t))
	require.NoError(t, err)
	defer nodeSet.Close()

	srv := NewGRPCServer(&Dependencies{
		Config: config.Config{Failover: config.Failover{ElectionStrategy: config.ElectionStrategyPerRepository}},
		Logger: testhelper.SharedLogger(t),
		Director: func(ctx context.Context, fullMethodName string, peeker proxy.StreamPeeker) (*proxy.StreamParameters, error) {
			return nil, errServedByGitaly
		},
		RepositoryStore: rs,
		Registry:        protoregistry.GitalyProtoPreregistered,
		Conns:           nodeSet.Connections(),
	}, nil)
	defer srv.Stop()

	go testhelper.MustServe(t, srv, ln)

	clientConn, err := grpc.DialContext(ctx, "unix:"+ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer clientConn.Close()

	client := gitalypb.NewRepositoryServiceClient(clientConn)
	_, err = client.RepositorySize(ctx, &gitalypb.RepositorySizeRequest{Repository: &gitalypb.Repository{}})
	testhelper.RequireGrpcError(t, errServedByGitaly, err)

	resp, err := client.RemoveAll(ctx, &gitalypb.RemoveAllRequest{StorageName: virtualStorage})
	require.NoError(t, err)

	testhelper.ProtoEqual(t, &gitalypb.RemoveAllResponse{}, resp)
	require.NoDirExists(t, gitaly1RepoPath)
	require.NoDirExists(t, gitaly2RepoPath)
}
