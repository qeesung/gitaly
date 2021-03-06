package testserver

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/rubyserver"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/server"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"google.golang.org/grpc"
)

// RunGitalyServer starts gitaly server based on the provided cfg.
// Returns connection address and a cleanup function.
// It accepts addition Registrar to register all required service instead of
// calling service.RegisterAll explicitly because it creates a circular dependency
// when the function is used in on of internal/gitaly/service/... packages.
func RunGitalyServer(t *testing.T, cfg config.Cfg, rubyServer *rubyserver.Server, registrar func(grpcServer *grpc.Server, deps *service.Dependencies)) (string, testhelper.Cleanup) {
	t.Helper()

	conns := client.NewPool()
	locator := config.NewLocator(cfg)
	txManager := transaction.NewManager(cfg)

	srv, err := server.New(cfg.TLS.CertPath != "", cfg, testhelper.DiscardTestEntry(t))
	require.NoError(t, err)

	registrar(srv, &service.Dependencies{
		Cfg:                cfg,
		RubyServer:         rubyServer,
		GitalyHookManager:  hook.NewManager(locator, txManager, hook.GitlabAPIStub, cfg),
		TransactionManager: txManager,
		StorageLocator:     locator,
		ClientPool:         conns,
		GitCmdFactory:      git.NewExecCommandFactory(cfg),
	})

	serverSocketPath := testhelper.GetTemporaryGitalySocketFileName(t)
	listener, err := net.Listen("unix", serverSocketPath)
	require.NoError(t, err)

	//listen on internal socket
	internalListener, err := net.Listen("unix", cfg.GitalyInternalSocketPath())
	require.NoError(t, err)

	go srv.Serve(internalListener)
	go srv.Serve(listener)

	return "unix://" + serverSocketPath, func() {
		conns.Close()
		srv.Stop()
	}
}
