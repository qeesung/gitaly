package praefect

import (
	"context"
	"io/ioutil"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	gconfig "gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/mock"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/version"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestServerRouteServerAccessor(t *testing.T) {
	var (
		conf = testConfig(1)
		reqQ = make(chan *mock.SimpleRequest)

		expectResp = &mock.SimpleResponse{Value: 2}

		// note: a server scoped RPC will be randomly routed
		// to an available backend server. To simplify our
		// test, a single backend server is used.
		backends = map[string]mock.SimpleServiceServer{
			conf.VirtualStorages[0].Nodes[0].Storage: &mockSvc{
				serverAccessor: func(_ context.Context, req *mock.SimpleRequest) (*mock.SimpleResponse, error) {
					reqQ <- req
					return expectResp, nil
				},
			},
		}
	)

	cli, _, cleanup := runPraefectServerWithMock(t, conf, backends)
	defer cleanup()

	expectReq := &mock.SimpleRequest{Value: 1}

	done := make(chan struct{})
	go func() {
		defer close(done)

		actualReq := <-reqQ
		assert.True(t, proto.Equal(expectReq, actualReq),
			"received unexpected request value: %+v instead of %+v", actualReq, expectReq)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	actualResp, err := cli.ServerAccessor(ctx, expectReq)
	require.NoError(t, err)
	require.True(t, proto.Equal(expectResp, actualResp),
		"expected response was not routed back")

	waitUntil(t, done, time.Second)
}

func TestGitalyServerInfo(t *testing.T) {
	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			&config.VirtualStorage{
				Nodes: []*models.Node{
					&models.Node{
						Storage:        "praefect-internal-1",
						DefaultPrimary: true,
						Token:          "abc",
					},
					&models.Node{
						Storage: "praefect-internal-2",
						Token:   "xyz",
					},
				},
			},
		},
	}

	cc, _, cleanup := runPraefectServerWithGitaly(t, conf)
	defer cleanup()

	client := gitalypb.NewServerServiceClient(cc)

	ctx, cancel := testhelper.Context()
	defer cancel()

	metadata, err := client.ServerInfo(ctx, &gitalypb.ServerInfoRequest{})
	require.NoError(t, err)
	require.Len(t, metadata.GetStorageStatuses(), len(conf.VirtualStorages[0].Nodes))
	require.Equal(t, version.GetVersion(), metadata.GetServerVersion())

	gitVersion, err := git.Version()
	require.NoError(t, err)
	require.Equal(t, gitVersion, metadata.GetGitVersion())

	for _, storageStatus := range metadata.GetStorageStatuses() {
		require.NotNil(t, storageStatus, "none of the storage statuses should be nil")
	}
}

func TestGitalyServerInfoBadNode(t *testing.T) {
	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			&config.VirtualStorage{
				Nodes: []*models.Node{
					&models.Node{
						Storage:        "praefect-internal-1",
						Address:        "tcp://unreachable:1234",
						DefaultPrimary: true,
						Token:          "abc",
					},
				},
			},
		},
	}

	entry := testhelper.DiscardTestEntry(t)
	nodeMgr, err := nodes.NewManager(entry, conf)
	require.NoError(t, err)

	_, srv := setupServer(t, conf, nodeMgr, entry, protoregistry.GitalyProtoFileDescriptors)

	listener, port := listenAvailPort(t)
	go func() {
		srv.RegisterServices()
		srv.Serve(listener, false)
	}()

	cc := dialLocalPort(t, port, false)
	ctx, cancel := testhelper.Context()
	defer cancel()

	client := gitalypb.NewServerServiceClient(cc)

	metadata, err := client.ServerInfo(ctx, &gitalypb.ServerInfoRequest{})
	require.NoError(t, err)
	require.Len(t, metadata.GetStorageStatuses(), 0)
}

func TestGitalyDiskStatistics(t *testing.T) {
	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Nodes: []*models.Node{
					{
						Storage:        "praefect-internal-1",
						DefaultPrimary: true,
						Token:          "abc",
					},
					{
						Storage: "praefect-internal-2",
						Token:   "xyz",
					}},
			},
		},
	}

	cc, _, cleanup := runPraefectServerWithGitaly(t, conf)
	defer cleanup()

	client := gitalypb.NewServerServiceClient(cc)

	ctx, cancel := testhelper.Context()
	defer cancel()

	metadata, err := client.DiskStatistics(ctx, &gitalypb.DiskStatisticsRequest{})
	require.NoError(t, err)
	require.Len(t, metadata.GetStorageStatuses(), len(conf.VirtualStorages[0].Nodes))

	for _, storageStatus := range metadata.GetStorageStatuses() {
		require.NotNil(t, storageStatus, "none of the storage statuses should be nil")
	}
}

func TestHealthCheck(t *testing.T) {
	cc, _, cleanup := runPraefectServerWithGitaly(t, testConfig(1))
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	client := healthpb.NewHealthClient(cc)
	_, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
}

func TestRejectBadStorage(t *testing.T) {
	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			&config.VirtualStorage{
				Name: "praefect",
				Nodes: []*models.Node{
					&models.Node{
						DefaultPrimary: true,
						Storage:        "praefect-internal-0",
						Address:        "tcp::/this-doesnt-matter",
					},
				},
			},
		},
	}

	cc, _, cleanup := runPraefectServerWithGitaly(t, conf)
	defer cleanup()

	badTargetRepo := gitalypb.Repository{
		StorageName:  "default",
		RelativePath: "/path/to/hashed/storage",
	}

	repoClient := gitalypb.NewRepositoryServiceClient(cc)

	ctx, cancel := testhelper.Context()
	defer cancel()

	_, err := repoClient.GarbageCollect(ctx, &gitalypb.GarbageCollectRequest{Repository: &badTargetRepo})
	require.Error(t, err)
}

func TestWarnDuplicateAddrs(t *testing.T) {
	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			&config.VirtualStorage{
				Name: "default",
				Nodes: []*models.Node{
					&models.Node{
						DefaultPrimary: true,
						Storage:        "praefect-internal-0",
						Address:        "tcp://abc",
					},
					&models.Node{
						Storage: "praefect-internal-1",
						Address: "tcp://xyz",
					},
				},
			},
			&config.VirtualStorage{
				Name: "praefect",
				Nodes: []*models.Node{
					&models.Node{
						DefaultPrimary: true,
						Storage:        "praefect-internal-0",
						Address:        "tcp://abc",
					},
					&models.Node{
						Storage: "praefect-internal-1",
						Address: "tcp://xyz",
					},
				},
			},
		},
	}

	tLogger, hook := test.NewNullLogger()

	setupServer(t, conf, nil, logrus.NewEntry(tLogger), nil) // instantiates a praefect server and triggers warning

	for _, entry := range hook.Entries {
		require.NotContains(t, entry.Message, "more than one backend node")
	}

	conf = config.Config{
		VirtualStorages: []*config.VirtualStorage{
			&config.VirtualStorage{
				Name: "praefect",
				Nodes: []*models.Node{
					&models.Node{
						DefaultPrimary: true,
						Storage:        "praefect-internal-0",
						Address:        "tcp::/samesies",
					},
					&models.Node{
						Storage: "praefect-internal-1",
						Address: "tcp::/samesies",
					},
				},
			},
		},
	}

	tLogger, hook = test.NewNullLogger()

	setupServer(t, conf, nil, logrus.NewEntry(tLogger), nil) // instantiates a praefect server and triggers warning

	var found bool
	for _, entry := range hook.Entries {
		if strings.Contains(entry.Message, "more than one backend node") {
			found = true
			break
		}
	}
	require.True(t, found, "expected to find error log")

	conf = config.Config{
		VirtualStorages: []*config.VirtualStorage{
			&config.VirtualStorage{
				Name: "default",
				Nodes: []*models.Node{
					&models.Node{
						DefaultPrimary: true,
						Storage:        "praefect-internal-0",
						Address:        "tcp://abc",
					},
					&models.Node{
						Storage: "praefect-internal-1",
						Address: "tcp://xyz",
					},
				},
			},
			&config.VirtualStorage{
				Name: "praefect",
				Nodes: []*models.Node{
					&models.Node{
						DefaultPrimary: true,
						Storage:        "praefect-internal-0",
						Address:        "tcp://abc",
					},
					&models.Node{
						Storage: "praefect-internal-2",
						Address: "tcp://xyz",
					},
				},
			},
		},
	}

	tLogger, hook = test.NewNullLogger()

	setupServer(t, conf, nil, logrus.NewEntry(tLogger), nil) // instantiates a praefect server and triggers warning

	for _, entry := range hook.Entries {
		require.NotContains(t, entry.Message, "more than one backend node")
	}
}

func TestRepoRemoval(t *testing.T) {
	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			&config.VirtualStorage{
				Name: "praefect",
				Nodes: []*models.Node{
					&models.Node{
						DefaultPrimary: true,
						Storage:        gconfig.Config.Storages[0].Name,
						Address:        "tcp::/samesies",
					},
					&models.Node{
						Storage: "praefect-internal-1",
						Address: "tcp::/this-doesnt-matter",
					},
					&models.Node{
						Storage: "praefect-internal-2",
						Address: "tcp::/this-doesnt-matter",
					},
				},
			},
		},
	}

	oldStorages := gconfig.Config.Storages
	defer func() { gconfig.Config.Storages = oldStorages }()

	testStorages := []gconfig.Storage{
		{
			Name: conf.VirtualStorages[0].Nodes[1].Storage,
			Path: tempStoragePath(t),
		},
		{
			Name: conf.VirtualStorages[0].Nodes[2].Storage,
			Path: tempStoragePath(t),
		},
	}
	gconfig.Config.Storages = append(gconfig.Config.Storages, testStorages...)
	defer func() {
		for _, s := range testStorages {
			require.NoError(t, os.RemoveAll(s.Path))
		}
	}()

	tRepo, _, tCleanup := testhelper.NewTestRepo(t)
	defer tCleanup()

	_, path1, cleanup1 := cloneRepoAtStorage(t, tRepo, conf.VirtualStorages[0].Nodes[1].Storage)
	defer cleanup1()
	_, path2, cleanup2 := cloneRepoAtStorage(t, tRepo, conf.VirtualStorages[0].Nodes[2].Storage)
	defer cleanup2()

	// prerequisite: repos should exist at expected paths
	require.DirExists(t, path1)
	require.DirExists(t, path2)

	cc, _, cleanup := runPraefectServerWithGitaly(t, conf)
	defer cleanup()

	ctx, cancel := testhelper.Context()
	defer cancel()

	virtualRepo := *tRepo
	virtualRepo.StorageName = conf.VirtualStorages[0].Name

	rClient := gitalypb.NewRepositoryServiceClient(cc)

	_, err := rClient.RemoveRepository(ctx, &gitalypb.RemoveRepositoryRequest{
		Repository: &virtualRepo,
	})
	require.NoError(t, err)

	resp, err := rClient.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
		Repository: &virtualRepo,
	})
	require.NoError(t, err)
	require.Equal(t, false, resp.GetExists())

	// the removal of the repo on the secondary servers is not deterministic
	// since it relies on eventually consistent replication
	pollUntilRemoved(t, path1, time.After(10*time.Second))
	pollUntilRemoved(t, path2, time.After(10*time.Second))
}

func pollUntilRemoved(t testing.TB, path string, deadline <-chan time.Time) {
	for {
		select {
		case <-deadline:
			require.Failf(t, "unable to detect path removal for %s", path)
		default:
			_, err := os.Stat(path)
			switch {
			case err != nil && os.IsNotExist(err):
				return
			case err == nil:
				break
			default:
				require.Failf(t, "unexpected error while checking path %s", path)
			}
		}
		time.Sleep(time.Millisecond)
	}
}

func tempStoragePath(t testing.TB) string {
	p, err := ioutil.TempDir("", t.Name())
	require.NoError(t, err)
	return p
}

func cloneRepoAtStorage(t testing.TB, src *gitalypb.Repository, storageName string) (*gitalypb.Repository, string, func()) {
	dst := *src
	dst.StorageName = storageName

	dstP, err := helper.GetPath(&dst)
	require.NoError(t, err)

	srcP, err := helper.GetPath(src)
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(dstP, 0755))
	testhelper.MustRunCommand(t, nil, "git",
		"clone", "--no-hardlinks", "--dissociate", "--bare", srcP, dstP)

	return &dst, dstP, func() { require.NoError(t, os.RemoveAll(dstP)) }
}
