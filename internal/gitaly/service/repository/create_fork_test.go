//go:build !gitaly_test_sha256

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
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/praefectutil"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	gitalyx509 "gitlab.com/gitlab-org/gitaly/v15/internal/x509"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

func TestCreateFork_successful(t *testing.T) {
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

			createRepoConfig := gittest.CreateRepositoryConfig{
				Seed: gittest.SeedGitLabTest,
			}
			getReplicaPathConfig := gittest.GetReplicaPathConfig{}

			var client gitalypb.RepositoryServiceClient
			if tt.secure {
				cfg.TLS = tlsConfig
				cfg.TLSListenAddr = "localhost:0"

				_, addr := runRepositoryService(t, cfg, nil)
				cfg.TLSListenAddr = addr

				var conn *grpc.ClientConn
				client, conn = newSecureRepoClient(t, cfg.TLSListenAddr, cfg.Auth.Token, certPool)
				t.Cleanup(func() { conn.Close() })

				createRepoConfig.ClientConn = conn
				getReplicaPathConfig.ClientConn = conn
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

			replicaPath := gittest.GetReplicaPath(ctx, t, cfg, forkedRepo, getReplicaPathConfig)
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
	t.Setenv(gitalyx509.SSLCertFile, certFile)

	caPEMBytes := testhelper.MustReadFile(t, certFile)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(caPEMBytes))

	return pool, config.TLS{CertPath: certFile, KeyPath: keyFile}
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
