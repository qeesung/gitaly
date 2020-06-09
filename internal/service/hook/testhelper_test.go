package hook

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/auth"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func TestMain(m *testing.M) {
	testhelper.Configure()
	os.Exit(m.Run())
}

func newHooksClient(t *testing.T, serverSocketPath string) (gitalypb.HookServiceClient, *grpc.ClientConn) {
	connOpts := []grpc.DialOption{
		grpc.WithInsecure(),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(config.Config.Auth.Token)),
	}
	conn, err := grpc.Dial(serverSocketPath, connOpts...)
	if err != nil {
		t.Fatal(err)
	}

	return gitalypb.NewHookServiceClient(conn), conn
}

func runHooksServer(t *testing.T, hooksCfg config.Hooks) (string, func()) {
	return runHooksServerWithAPI(t, testhelper.GitlabAPIStub, hooksCfg)
}

func runHooksServerWithAPI(t *testing.T, gitlabAPI GitlabAPI, hooksCfg config.Hooks) (string, func()) {
	srv := testhelper.NewServer(t, nil, nil)

	gitalypb.RegisterHookServiceServer(srv.GrpcServer(), NewServer(gitlabAPI, hooksCfg))
	reflection.Register(srv.GrpcServer())

	require.NoError(t, srv.Start())

	return "unix://" + srv.Socket(), srv.Stop
}
