package server_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestServer_ReadinessCheck(t *testing.T) {
	t.Parallel()
	stubCheck := func(t *testing.T, triggered chan string, name string) *service.Check {
		return &service.Check{
			Name: name,
			Run: func(ctx context.Context) error {
				_, ok := ctx.Deadline()
				assert.True(t, ok, "the deadline should be set as we provide timeout")
				triggered <- name
				return nil
			},
		}
	}

	const gitalyStorageName = "praefect-internal-0"
	gitalyCfg := testcfg.Build(t, testcfg.WithStorages(gitalyStorageName))
	gitalyAddr := testserver.RunGitalyServer(t, gitalyCfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())

	praefectConf := config.Config{
		SocketPath: testhelper.GetTemporaryGitalySocketFileName(t),
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "default",
				Nodes: []*config.Node{
					{
						Storage: gitalyStorageName,
						Address: gitalyAddr,
					},
				},
			},
		},
	}
	ctx := testhelper.Context(t)
	triggered := make(chan string, 2)
	grpcPraefectConn, _, cleanup := praefect.RunPraefectServer(t, ctx, praefectConf, praefect.BuildOptions{
		WithChecks: []service.CheckFunc{
			func(conf config.Config, w io.Writer, quiet bool) *service.Check {
				return stubCheck(t, triggered, "1")
			},
			func(conf config.Config, w io.Writer, quiet bool) *service.Check {
				return stubCheck(t, triggered, "2")
			},
		},
	})
	t.Cleanup(cleanup)
	serverClient := gitalypb.NewServerServiceClient(grpcPraefectConn)
	resp, err := serverClient.ReadinessCheck(ctx, &gitalypb.ReadinessCheckRequest{Timeout: durationpb.New(time.Second)})
	require.NoError(t, err)
	assert.NotNil(t, resp.GetOkResponse())
	if !assert.Nil(t, resp.GetFailureResponse()) {
		for _, failure := range resp.GetFailureResponse().GetFailedChecks() {
			assert.Failf(t, "failed check", "%s: %s", failure.Name, failure.ErrorMessage)
		}
	}
	names := make([]string, 0, cap(triggered))
	for i := 0; i < cap(triggered); i++ {
		name := <-triggered
		names = append(names, name)
	}
	require.ElementsMatch(t, []string{"1", "2"}, names, "both tasks should be triggered for an execution")
}

func TestServer_ReadinessCheck_unreachableGitaly(t *testing.T) {
	t.Parallel()
	praefectConf := config.Config{
		SocketPath: testhelper.GetTemporaryGitalySocketFileName(t),
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "default",
				Nodes: []*config.Node{
					{
						Storage: "praefect-internal-0",
						Address: "tcp://non-existing:42",
					},
				},
			},
		},
	}
	ctx := testhelper.Context(t)
	grpcConn, _, cleanup := praefect.RunPraefectServer(t, ctx, praefectConf, praefect.BuildOptions{})
	t.Cleanup(cleanup)
	serverClient := gitalypb.NewServerServiceClient(grpcConn)
	resp, err := serverClient.ReadinessCheck(ctx, &gitalypb.ReadinessCheckRequest{Timeout: durationpb.New(time.Nanosecond)})
	require.NoError(t, err)
	require.Nil(t, resp.GetOkResponse())
	require.NotNil(t, resp.GetFailureResponse())
	require.Len(t, resp.GetFailureResponse().FailedChecks, 5)
	require.Equal(t, "clock synchronization", resp.GetFailureResponse().FailedChecks[0].Name)
	require.Equal(t, "database read/write", resp.GetFailureResponse().FailedChecks[1].Name)
	require.Equal(t, "gitaly node connectivity & disk access", resp.GetFailureResponse().FailedChecks[2].Name)
	require.Equal(t, "praefect migrations", resp.GetFailureResponse().FailedChecks[3].Name)
	require.Equal(t, "unavailable repositories", resp.GetFailureResponse().FailedChecks[4].Name)
}
