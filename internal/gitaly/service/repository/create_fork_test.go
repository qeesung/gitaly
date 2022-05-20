package repository

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v15/auth"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/cache"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/rubyserver"
	gserver "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/server"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/commit"
	hookservice "gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/ref"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/remote"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/ssh"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/middleware/limithandler"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/praefectutil"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	gitalyx509 "gitlab.com/gitlab-org/gitaly/v15/internal/x509"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func TestCreateFork_successful(t *testing.T) {
	t.Parallel()

	// We need to inject this once across all tests given that crypto/x509 only initializes
	// certificates once. Changing injected certs during our tests is thus not going to fly well
	// and would cause failure. We should eventually address this and provide better testing
	// utilities around this, but now's not the time.
	certPool, tlsConfig := injectCustomCATestCerts(t)
	ctx := testhelper.Context(t)

	for _, tt := range []struct {
		name   string
		secure bool
	}{
		{
			name:   "secure",
			secure: true,
		},
		{
			name: "insecure",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testcfg.Build(t)

			testcfg.BuildGitalyHooks(t, cfg)
			testcfg.BuildGitalySSH(t, cfg)

			var (
				client gitalypb.RepositoryServiceClient
				conn   *grpc.ClientConn
			)

			createRepoConfig := gittest.CreateRepositoryConfig{
				Seed: gittest.SeedGitLabTest,
			}
			if tt.secure {
				cfg.TLS = tlsConfig
				cfg.TLSListenAddr = runSecureServer(t, cfg, nil)
				client, conn = newSecureRepoClient(t, cfg.TLSListenAddr, cfg.Auth.Token, certPool)
				t.Cleanup(func() { conn.Close() })
				createRepoConfig.ClientConn = conn
			} else {
				client, cfg.SocketPath = runRepositoryService(t, cfg, nil)
			}

			repo, _ := gittest.CreateRepository(ctx, t, cfg, createRepoConfig)

			ctx = testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

			forkedRepo := &gitalypb.Repository{
				RelativePath: gittest.NewRepositoryName(t, true),
				StorageName:  repo.GetStorageName(),
			}

			_, err := client.CreateFork(ctx, &gitalypb.CreateForkRequest{
				Repository:       forkedRepo,
				SourceRepository: repo,
			})
			require.NoError(t, err)

			replicaPath := forkedRepo.GetRelativePath()
			if !tt.secure {
				// Only the insecure test cases run through Praefect, so we only rewrite the path
				// in that case.
				replicaPath = gittest.GetReplicaPath(ctx, t, cfg, forkedRepo)
			}

			forkedRepoPath := filepath.Join(cfg.Storages[0].Path, replicaPath)

			gittest.Exec(t, cfg, "-C", forkedRepoPath, "fsck")
			require.Empty(t, gittest.Exec(t, cfg, "-C", forkedRepoPath, "remote"))

			_, err = os.Lstat(filepath.Join(forkedRepoPath, "hooks"))
			require.True(t, os.IsNotExist(err), "hooks directory should not have been created")
		})
	}
}

func TestCreateFork_refs(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg := testcfg.Build(t)
	testcfg.BuildGitalyHooks(t, cfg)
	testcfg.BuildGitalySSH(t, cfg)

	client, socketPath := runRepositoryService(t, cfg, nil)
	cfg.SocketPath = socketPath

	sourceRepo, sourceRepoPath := gittest.CreateRepository(ctx, t, cfg)

	// Prepare the source repository with a bunch of refs and a non-default HEAD ref so we can
	// assert that the target repo gets created with the correct set of refs.
	commitID := gittest.WriteCommit(t, cfg, sourceRepoPath, gittest.WithParents())
	for _, ref := range []string{
		"refs/environments/something",
		"refs/heads/something",
		"refs/remotes/origin/something",
		"refs/tags/something",
	} {
		gittest.Exec(t, cfg, "-C", sourceRepoPath, "update-ref", ref, commitID.String())
	}
	gittest.Exec(t, cfg, "-C", sourceRepoPath, "symbolic-ref", "HEAD", "refs/heads/something")

	ctx = testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

	targetRepo := &gitalypb.Repository{
		RelativePath: gittest.NewRepositoryName(t, true),
		StorageName:  sourceRepo.GetStorageName(),
	}

	_, err := client.CreateFork(ctx, &gitalypb.CreateForkRequest{
		Repository:       targetRepo,
		SourceRepository: sourceRepo,
	})
	require.NoError(t, err)

	storagePath, err := config.NewLocator(cfg).GetStorageByName(targetRepo.GetStorageName())
	require.NoError(t, err)

	targetRepoPath := filepath.Join(storagePath, gittest.GetReplicaPath(ctx, t, cfg, targetRepo))

	require.Equal(t,
		[]string{
			commitID.String() + " refs/heads/something",
			commitID.String() + " refs/tags/something",
		},
		strings.Split(text.ChompBytes(gittest.Exec(t, cfg, "-C", targetRepoPath, "show-ref")), "\n"),
	)

	require.Equal(t,
		string(gittest.Exec(t, cfg, "-C", sourceRepoPath, "symbolic-ref", "HEAD")),
		string(gittest.Exec(t, cfg, "-C", targetRepoPath, "symbolic-ref", "HEAD")),
	)
}

func TestCreateFork_targetExists(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc                          string
		seed                          func(t *testing.T, targetPath string)
		expectedErrWithAtomicCreation error
	}{
		{
			desc: "empty target directory",
			seed: func(t *testing.T, targetPath string) {
				require.NoError(t, os.MkdirAll(targetPath, 0o770))
			},
			expectedErrWithAtomicCreation: helper.ErrAlreadyExistsf("creating fork: repository exists already"),
		},
		{
			desc: "non-empty target directory",
			seed: func(t *testing.T, targetPath string) {
				require.NoError(t, os.MkdirAll(targetPath, 0o770))
				require.NoError(t, os.WriteFile(
					filepath.Join(targetPath, "config"),
					nil,
					0o644,
				))
			},
			expectedErrWithAtomicCreation: helper.ErrAlreadyExistsf("creating fork: repository exists already"),
		},
		{
			desc: "target file",
			seed: func(t *testing.T, targetPath string) {
				require.NoError(t, os.MkdirAll(filepath.Dir(targetPath), 0o770))
				require.NoError(t, os.WriteFile(targetPath, nil, 0o644))
			},
			expectedErrWithAtomicCreation: helper.ErrAlreadyExistsf("creating fork: repository exists already"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, repo, _, client := setupRepositoryService(ctx, t)

			ctx = testhelper.MergeOutgoingMetadata(ctx, testcfg.GitalyServersMetadataFromCfg(t, cfg))

			forkedRepo := &gitalypb.Repository{
				// As this test can run with Praefect in front of it, we'll use the next replica path Praefect will
				// assign in order to ensure this repository creation conflicts even with Praefect in front of it.
				// As the source repository created in the setup is the first one, this would get the repository
				// ID 2.
				RelativePath: praefectutil.DeriveReplicaPath(2),
				StorageName:  repo.StorageName,
			}

			tc.seed(t, filepath.Join(cfg.Storages[0].Path, forkedRepo.GetRelativePath()))

			_, err := client.CreateFork(ctx, &gitalypb.CreateForkRequest{
				Repository:       forkedRepo,
				SourceRepository: repo,
			})
			testhelper.RequireGrpcError(t, tc.expectedErrWithAtomicCreation, err)
		})
	}
}

func injectCustomCATestCerts(t *testing.T) (*x509.CertPool, config.TLS) {
	certFile, keyFile := testhelper.GenerateCerts(t)

	testhelper.ModifyEnvironment(t, gitalyx509.SSLCertFile, certFile)

	caPEMBytes := testhelper.MustReadFile(t, certFile)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(caPEMBytes))

	return pool, config.TLS{CertPath: certFile, KeyPath: keyFile}
}

func runSecureServer(t *testing.T, cfg config.Cfg, rubySrv *rubyserver.Server) string {
	t.Helper()

	registry := backchannel.NewRegistry()
	locator := config.NewLocator(cfg)
	cache := cache.New(cfg, locator)
	limitHandler := limithandler.New(cfg, limithandler.LimitConcurrencyByRepo, limithandler.WithConcurrencyLimiters)
	server, err := gserver.New(true, cfg, testhelper.NewDiscardingLogEntry(t), registry, cache, []*limithandler.LimiterMiddleware{limitHandler})
	require.NoError(t, err)
	listener, addr := testhelper.GetLocalhostListener(t)

	txManager := transaction.NewManager(cfg, registry)
	gitCmdFactory := gittest.NewCommandFactory(t, cfg)
	hookManager := hook.NewManager(cfg, locator, gittest.NewCommandFactory(t, cfg), txManager, gitlab.NewMockClient(
		t, gitlab.MockAllowed, gitlab.MockPreReceive, gitlab.MockPostReceive,
	))
	catfileCache := catfile.NewCache(cfg)
	t.Cleanup(catfileCache.Stop)

	housekeepingManager := housekeeping.NewManager(cfg.Prometheus, txManager)

	connsPool := client.NewPool()
	t.Cleanup(func() { testhelper.MustClose(t, connsPool) })

	git2goExecutor := git2go.NewExecutor(cfg, gitCmdFactory, locator)

	gitalypb.RegisterRepositoryServiceServer(server, NewServer(
		cfg,
		rubySrv,
		locator,
		txManager,
		gitCmdFactory,
		catfileCache,
		connsPool,
		git2goExecutor,
		housekeepingManager,
	))
	gitalypb.RegisterHookServiceServer(server, hookservice.NewServer(
		hookManager,
		gitCmdFactory,
		nil,
	))
	gitalypb.RegisterRemoteServiceServer(server, remote.NewServer(
		locator,
		gitCmdFactory,
		catfileCache,
		txManager,
		connsPool,
	))
	gitalypb.RegisterSSHServiceServer(server, ssh.NewServer(
		locator,
		gitCmdFactory,
		txManager,
	))
	gitalypb.RegisterRefServiceServer(server, ref.NewServer(
		locator,
		gitCmdFactory,
		txManager,
		catfileCache,
	))
	gitalypb.RegisterCommitServiceServer(server, commit.NewServer(
		locator,
		gitCmdFactory,
		nil,
		catfileCache,
	))
	errQ := make(chan error, 1)

	// This creates a secondary GRPC server which isn't "secure". Reusing
	// the one created above won't work as its internal socket would be
	// protected by the same TLS certificate.

	cfg.TLS.KeyPath = ""
	testserver.RunGitalyServer(t, cfg, nil, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterHookServiceServer(srv, hookservice.NewServer(deps.GetHookManager(), deps.GetGitCmdFactory(), deps.GetPackObjectsCache()))
	})

	t.Cleanup(func() { require.NoError(t, <-errQ) })

	t.Cleanup(server.Stop)
	go func() { errQ <- server.Serve(listener) }()

	return "tls://" + addr
}

func newSecureRepoClient(t testing.TB, addr, token string, pool *x509.CertPool) (gitalypb.RepositoryServiceClient, *grpc.ClientConn) {
	t.Helper()

	connOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		})),
		grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(token)),
	}

	conn, err := client.Dial(addr, connOpts)
	require.NoError(t, err)

	return gitalypb.NewRepositoryServiceClient(conn), conn
}
