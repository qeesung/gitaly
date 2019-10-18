package praefect

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/mock"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/version"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

func TestServerRouteServerAccessor(t *testing.T) {
	var (
		conf = testConfig(1)
		reqQ = make(chan *mock.SimpleRequest)

		expectResp = &mock.SimpleResponse{Value: 2}

		// note: a server scoped RPC will be randomly routed
		// to an available backend server. To simplify our
		// test, a single backend server is used.
		backends = map[int]mock.SimpleServiceServer{
			0: &mockSvc{
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
		Nodes: []*models.Node{
			&models.Node{
				ID:             1,
				Storage:        "praefect-internal-1",
				DefaultPrimary: true,
				Token:          "abc",
			},
			&models.Node{
				ID:      2,
				Storage: "praefect-internal-2",
				Token:   "xyz",
			}},
	}
	cc, srv := runPraefectServerWithGitaly(t, conf)
	defer srv.s.Stop()

	client := gitalypb.NewServerServiceClient(cc)

	ctx, cancel := testhelper.Context()
	defer cancel()

	metadata, err := client.ServerInfo(ctx, &gitalypb.ServerInfoRequest{})
	require.NoError(t, err)
	require.Len(t, metadata.GetStorageStatuses(), len(conf.Nodes))
	require.Equal(t, version.GetVersion(), metadata.GetServerVersion())

	gitVersion, err := git.Version()
	require.NoError(t, err)
	require.Equal(t, gitVersion, metadata.GetGitVersion())

	for _, storageStatus := range metadata.GetStorageStatuses() {
		require.NotNil(t, storageStatus, "none of the storage statuses should be nil")
	}
}

func TestHealthCheck(t *testing.T) {
	cc, srv := runPraefectServerWithGitaly(t, config.Config{})
	defer srv.s.Stop()

	ctx, cancel := testhelper.Context()
	defer cancel()

	client := healthpb.NewHealthClient(cc)
	_, err := client.Check(ctx, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
}

func TestRejectBadStorage(t *testing.T) {
	conf := config.Config{
		VirtualStorageName: "praefect",
		Nodes: []*models.Node{
			&models.Node{
				DefaultPrimary: true,
				Storage:        "praefect-internal-0",
				Address:        "tcp::/this-doesnt-matter",
			},
		},
	}

	cc, srv := runPraefectServerWithGitaly(t, conf)
	defer srv.s.Stop()

	badTargetRepo := gitalypb.Repository{
		StorageName:  "default",
		RelativePath: "/path/to/hashed/storage",
	}

	repoClient := gitalypb.NewRepositoryServiceClient(cc)

	ctx, cancel := testhelper.Context()
	defer cancel()

	_, err := repoClient.GarbageCollect(ctx, &gitalypb.GarbageCollectRequest{Repository: &badTargetRepo})
	testhelper.RequireGrpcError(t, err, codes.InvalidArgument)
	require.Equal(t, fmt.Sprintf("only messages for %s are allowed", conf.VirtualStorageName), status.Convert(err).Message())
}

func TestWarnDuplicateAddrs(t *testing.T) {
	conf := config.Config{
		VirtualStorageName: "praefect",
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
	}

	tLogger, hook := test.NewNullLogger()

	wireServer(t, conf, logrus.NewEntry(tLogger), nil) // instantiates a praefect server and triggers warning

	for _, entry := range hook.Entries {
		if strings.Contains(entry.Message, "contains duplicate address") {
			return // pass!
		}
	}
	t.Fatal("could not find expected log message")
}
