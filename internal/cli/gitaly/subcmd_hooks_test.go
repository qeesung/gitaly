package gitaly

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	gitalyauth "gitlab.com/gitlab-org/gitaly/v16/auth"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/setup"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/client"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
	"google.golang.org/grpc"
)

func TestSetHooksSubcommand(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	umask := testhelper.Umask()

	cfg := testcfg.Build(t, testcfg.WithStorages("default", "another-storage"))
	testcfg.BuildGitaly(t, cfg)

	serverSocketPath := testserver.RunGitalyServer(t, cfg, setup.RegisterAll)

	repoCfg := cfg
	repoCfg.SocketPath = serverSocketPath

	// The generated socket path already has the unix prefix. This needs to be
	// removed because the Gitaly config does not expect a scheme to be present.
	cfg.SocketPath = strings.TrimPrefix(serverSocketPath, "unix://")
	client := newRepositoryClient(t, ctx, cfg, serverSocketPath)

	configPath := testcfg.WriteTemporaryGitalyConfigFile(t, cfg)

	expectedDirectoryMode := testhelper.WithOrWithoutWAL(
		// TAR does not store the directory mode in the mode field. It's stored
		// in the type field of the header. Remove the directory mode bit.
		storage.ModeDirectory^fs.ModeDir,
		umask.Mask(fs.ModePerm),
	)

	for _, tc := range []struct {
		desc          string
		setup         func() ([]string, *gitalypb.Repository)
		hooks         io.Reader
		expectedErr   string
		expectedState testhelper.DirectoryState
	}{
		{
			desc: "missing repository flag",
			setup: func() ([]string, *gitalypb.Repository) {
				repo, _ := gittest.CreateRepository(t, ctx, repoCfg)
				return []string{
					"--storage=" + repo.StorageName,
					"--config=" + configPath,
				}, repo
			},
			hooks:       &bytes.Buffer{},
			expectedErr: "Required flag \"repository\" not set\n",
		},
		{
			desc: "missing config flag",
			setup: func() ([]string, *gitalypb.Repository) {
				repo, _ := gittest.CreateRepository(t, ctx, repoCfg)
				return []string{
					"--storage=" + repo.StorageName,
					"--repository=" + repo.RelativePath,
				}, repo
			},
			hooks:       &bytes.Buffer{},
			expectedErr: "Required flag \"config\" not set\n",
		},
		{
			desc: "missing storage flag and config has multiple storages",
			setup: func() ([]string, *gitalypb.Repository) {
				repo, _ := gittest.CreateRepository(t, ctx, repoCfg)
				return []string{
					"--repository=" + repo.RelativePath,
					"--config=" + configPath,
				}, repo
			},
			hooks:       &bytes.Buffer{},
			expectedErr: "multiple storages configured: use --storage to target storage explicitly",
		},
		{
			desc: "storage not found",
			setup: func() ([]string, *gitalypb.Repository) {
				repo, _ := gittest.CreateRepository(t, ctx, repoCfg)
				return []string{
					"--storage=non-existent",
					"--repository=" + repo.RelativePath,
					"--config=" + configPath,
				}, repo
			},
			hooks:       testhelper.MustCreateCustomHooksTar(t),
			expectedErr: storage.NewStorageNotFoundError("non-existent").Error(),
		},
		{
			desc: "repository not found",
			setup: func() ([]string, *gitalypb.Repository) {
				repo, _ := gittest.CreateRepository(t, ctx, repoCfg)
				return []string{
					"--storage=" + repo.StorageName,
					"--repository=non-existent",
					"--config=" + configPath,
				}, repo
			},
			hooks:       testhelper.MustCreateCustomHooksTar(t),
			expectedErr: storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, "non-existent").Error(),
		},
		{
			desc: "successfully set with empty hooks",
			setup: func() ([]string, *gitalypb.Repository) {
				repo, _ := gittest.CreateRepository(t, ctx, repoCfg)
				return []string{
					"--storage=" + repo.StorageName,
					"--repository=" + repo.RelativePath,
					"--config=" + configPath,
				}, repo
			},
			hooks: &bytes.Buffer{},
			expectedState: testhelper.DirectoryState{
				"custom_hooks/": {Mode: expectedDirectoryMode},
			},
		},
		{
			desc: "successfully set with hooks",
			setup: func() ([]string, *gitalypb.Repository) {
				repo, _ := gittest.CreateRepository(t, ctx, repoCfg)
				return []string{
					"--storage=" + repo.StorageName,
					"--repository=" + repo.RelativePath,
					"--config=" + configPath,
				}, repo
			},
			hooks: testhelper.MustCreateCustomHooksTar(t),
			expectedState: testhelper.DirectoryState{
				"custom_hooks/":            {Mode: expectedDirectoryMode},
				"custom_hooks/pre-commit":  {Mode: umask.Mask(perm.SharedExecutable), Content: []byte("pre-commit content")},
				"custom_hooks/pre-push":    {Mode: umask.Mask(perm.SharedExecutable), Content: []byte("pre-push content")},
				"custom_hooks/pre-receive": {Mode: umask.Mask(perm.SharedExecutable), Content: []byte("pre-receive content")},
			},
		},
		{
			desc: "successfully set with hooks using default storage",
			setup: func() ([]string, *gitalypb.Repository) {
				// The default storage can only be determined if there is a single storage in the
				// config. This test creates a config with only a single storage to verify a default
				// storage value is being used.
				singleStorageCfg := cfg
				singleStorageCfg.Storages = singleStorageCfg.Storages[:1]
				singleStorageCfgPath := testcfg.WriteTemporaryGitalyConfigFile(t, singleStorageCfg)

				repo, _ := gittest.CreateRepository(t, ctx, repoCfg)
				return []string{
					"--repository=" + repo.RelativePath,
					"--config=" + singleStorageCfgPath,
				}, repo
			},
			hooks: testhelper.MustCreateCustomHooksTar(t),
			expectedState: testhelper.DirectoryState{
				"custom_hooks/":            {Mode: expectedDirectoryMode},
				"custom_hooks/pre-commit":  {Mode: umask.Mask(perm.SharedExecutable), Content: []byte("pre-commit content")},
				"custom_hooks/pre-push":    {Mode: umask.Mask(perm.SharedExecutable), Content: []byte("pre-push content")},
				"custom_hooks/pre-receive": {Mode: umask.Mask(perm.SharedExecutable), Content: []byte("pre-receive content")},
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			flags, repo := tc.setup()

			args := append([]string{"hooks", "set"}, flags...)
			cmd := exec.Command(cfg.BinaryPath("gitaly"), args...)

			var stderr bytes.Buffer
			cmd.Stdin = tc.hooks
			cmd.Stderr = &stderr

			err := cmd.Run()

			if tc.expectedErr != "" {
				require.Error(t, err)
				require.Contains(t, stderr.String(), tc.expectedErr)
				require.False(t, cmd.ProcessState.Success())
			} else {
				require.Empty(t, stderr.String())
				require.NoError(t, err)
				require.True(t, cmd.ProcessState.Success())
			}

			stream, err := client.GetCustomHooks(ctx, &gitalypb.GetCustomHooksRequest{
				Repository: &gitalypb.Repository{
					StorageName:  repo.StorageName,
					RelativePath: repo.RelativePath,
				},
			})
			require.NoError(t, err)

			hooksReader := streamio.NewReader(func() ([]byte, error) {
				response, err := stream.Recv()
				return response.GetData(), err
			})

			testhelper.RequireTarState(t, hooksReader, tc.expectedState)
		})
	}
}

func newRepositoryClient(tb testing.TB, ctx context.Context, cfg config.Cfg, serverSocketPath string) gitalypb.RepositoryServiceClient {
	tb.Helper()

	connOpts := []grpc.DialOption{
		client.UnaryInterceptor(), client.StreamInterceptor(),
	}
	if cfg.Auth.Token != "" {
		connOpts = append(connOpts, grpc.WithPerRPCCredentials(gitalyauth.RPCCredentialsV2(cfg.Auth.Token)))
	}
	conn, err := client.Dial(ctx, serverSocketPath, client.WithGrpcOptions(connOpts))
	require.NoError(tb, err)
	tb.Cleanup(func() { require.NoError(tb, conn.Close()) })

	return gitalypb.NewRepositoryServiceClient(conn)
}
