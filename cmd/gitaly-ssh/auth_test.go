package main

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/client"
	"gitlab.com/gitlab-org/gitaly/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/internal/cache"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/server"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/internal/middleware/limithandler"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/internal/x509"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestConnectivity(t *testing.T) {
	cfg, repo, _ := testcfg.BuildWithRepo(t)

	testcfg.BuildGitalySSH(t, cfg)
	testcfg.BuildGitalyHooks(t, cfg)

	cwd, err := os.Getwd()
	require.NoError(t, err)

	tempDir := testhelper.TempDir(t)

	relativeSocketPath, err := filepath.Rel(cwd, filepath.Join(tempDir, "gitaly.socket"))
	require.NoError(t, err)

	require.NoError(t, os.RemoveAll(relativeSocketPath))
	require.NoError(t, os.Symlink(cfg.SocketPath, relativeSocketPath))

	runGitaly := func(t testing.TB, cfg config.Cfg) string {
		t.Helper()
		return testserver.RunGitalyServer(t, cfg, nil, setup.RegisterAll, testserver.WithDisablePraefect())
	}

	testCases := []struct {
		name  string
		addr  func(t *testing.T, cfg config.Cfg) (string, string)
		proxy bool
	}{
		{
			name: "tcp",
			addr: func(t *testing.T, cfg config.Cfg) (string, string) {
				cfg.ListenAddr = "localhost:0"
				return runGitaly(t, cfg), ""
			},
		},
		{
			name: "unix absolute",
			addr: func(t *testing.T, cfg config.Cfg) (string, string) {
				return runGitaly(t, cfg), ""
			},
		},
		{
			name: "unix abs with proxy",
			addr: func(t *testing.T, cfg config.Cfg) (string, string) {
				return runGitaly(t, cfg), ""
			},
			proxy: true,
		},
		{
			name: "unix relative",
			addr: func(t *testing.T, cfg config.Cfg) (string, string) {
				cfg.SocketPath = fmt.Sprintf("unix:%s", relativeSocketPath)
				return runGitaly(t, cfg), ""
			},
		},
		{
			name: "unix relative with proxy",
			addr: func(t *testing.T, cfg config.Cfg) (string, string) {
				cfg.SocketPath = fmt.Sprintf("unix:%s", relativeSocketPath)
				return runGitaly(t, cfg), ""
			},
			proxy: true,
		},
		{
			name: "tls",
			addr: func(t *testing.T, cfg config.Cfg) (string, string) {
				certFile, keyFile := testhelper.GenerateCerts(t)

				testhelper.ModifyEnvironment(t, x509.SSLCertFile, certFile)

				cfg.TLSListenAddr = "localhost:0"
				cfg.TLS = config.TLS{
					CertPath: certFile,
					KeyPath:  keyFile,
				}
				return runGitaly(t, cfg), certFile
			},
		},
	}

	payload, err := protojson.Marshal(&gitalypb.SSHUploadPackRequest{
		Repository: repo,
	})

	require.NoError(t, err)
	for _, testcase := range testCases {
		t.Run(testcase.name, func(t *testing.T) {
			addr, certFile := testcase.addr(t, cfg)

			env := []string{
				fmt.Sprintf("GITALY_PAYLOAD=%s", payload),
				fmt.Sprintf("GITALY_ADDRESS=%s", addr),
				fmt.Sprintf("GITALY_WD=%s", cwd),
				fmt.Sprintf("PATH=.:%s", os.Getenv("PATH")),
				fmt.Sprintf("GIT_SSH_COMMAND=%s upload-pack", filepath.Join(cfg.BinDir, "gitaly-ssh")),
				fmt.Sprintf("SSL_CERT_FILE=%s", certFile),
			}
			if testcase.proxy {
				env = append(env,
					"http_proxy=http://invalid:1234",
					"https_proxy=https://invalid:1234",
				)
			}

			output := gittest.ExecOpts(t, cfg, gittest.ExecConfig{
				Env: env,
			}, "ls-remote", "git@localhost:test/test.git", "refs/heads/master")
			require.True(t, strings.HasSuffix(strings.TrimSpace(string(output)), "refs/heads/master"))
		})
	}
}

func runServer(t *testing.T, secure bool, cfg config.Cfg, connectionType string, addr string) (int, func()) {
	conns := client.NewPool()
	locator := config.NewLocator(cfg)
	registry := backchannel.NewRegistry()
	txManager := transaction.NewManager(cfg, registry)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	hookManager := hook.NewManager(cfg, locator, gitCmdFactory, txManager, gitlab.NewMockClient(
		t, gitlab.MockAllowed, gitlab.MockPreReceive, gitlab.MockPostReceive,
	))
	limitHandler := limithandler.New(cfg, limithandler.LimitConcurrencyByRepo, limithandler.WithConcurrencyLimiters)
	diskCache := cache.New(cfg, locator)
	srv, err := server.New(secure, cfg, testhelper.NewDiscardingLogEntry(t), registry, diskCache, []*limithandler.LimiterMiddleware{limitHandler})
	require.NoError(t, err)
	setup.RegisterAll(srv, &service.Dependencies{
		Cfg:                cfg,
		GitalyHookManager:  hookManager,
		TransactionManager: txManager,
		StorageLocator:     locator,
		ClientPool:         conns,
		GitCmdFactory:      gitCmdFactory,
	})

	listener, err := net.Listen(connectionType, addr)
	require.NoError(t, err)

	go srv.Serve(listener)

	port := 0
	if connectionType != "unix" {
		addrSplit := strings.Split(listener.Addr().String(), ":")
		portString := addrSplit[len(addrSplit)-1]

		port, err = strconv.Atoi(portString)
		require.NoError(t, err)
	}

	return port, func() {
		conns.Close()
		srv.Stop()
	}
}
