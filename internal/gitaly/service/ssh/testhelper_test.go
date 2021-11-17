package ssh

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service"
	hookservice "gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func runSSHServer(t *testing.T, cfg config.Cfg, serverOpts ...testserver.GitalyServerOpt) string {
	return runSSHServerWithOptions(t, cfg, nil, serverOpts...)
}

func runSSHServerWithOptions(t *testing.T, cfg config.Cfg, opts []ServerOpt, serverOpts ...testserver.GitalyServerOpt) string {
	return testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterSSHServiceServer(srv, NewServer(
			deps.GetCfg(),
			deps.GetLocator(),
			deps.GetGitCmdFactory(),
			deps.GetTxManager(),
			opts...))
		gitalypb.RegisterHookServiceServer(srv, hookservice.NewServer(deps.GetCfg(), deps.GetHookManager(), deps.GetGitCmdFactory(), deps.GetPackObjectsCache()))
	}, serverOpts...)
}

func newSSHClient(t *testing.T, serverSocketPath string) (gitalypb.SSHServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewSSHServiceClient(conn), conn
}

func requireProcessingDetailsLogged(t testing.TB, hook *test.Hook) {
	t.Helper()
	var logEntry *logrus.Entry
	logEntries := hook.AllEntries()
	for _, e := range logEntries {
		if e.Message == "transferred bytes" {
			logEntry = e
			break
		}
	}
	require.NotNil(t, logEntry, "log entry is missing")
	_, ok := logEntry.Data["in_bytes"]
	require.True(t, ok, "no information about request_bytes")
	_, ok = logEntry.Data["out_bytes"]
	require.True(t, ok, "no information about response_bytes")
}
