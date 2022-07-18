//go:build !gitaly_test_sha256

package rubyserver

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/auth"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/log"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/version"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"google.golang.org/grpc/codes"
)

func TestStopSafe(t *testing.T) {
	badServers := []*Server{
		nil,
		New(config.Cfg{}, nil),
	}

	for _, bs := range badServers {
		bs.Stop()
	}
}

func TestSetHeaders(t *testing.T) {
	cfg, repo, _ := testcfg.BuildWithRepo(t)
	ctx := testhelper.Context(t)

	locator := config.NewLocator(cfg)

	testCases := []struct {
		desc    string
		repo    *gitalypb.Repository
		errType codes.Code
		setter  func(context.Context, storage.Locator, *gitalypb.Repository) (context.Context, error)
	}{
		{
			desc:    "SetHeaders invalid storage",
			repo:    &gitalypb.Repository{StorageName: "foo", RelativePath: "bar.git"},
			errType: codes.InvalidArgument,
			setter:  SetHeaders,
		},
		{
			desc:    "SetHeaders invalid rel path",
			repo:    &gitalypb.Repository{StorageName: repo.StorageName, RelativePath: "bar.git"},
			errType: codes.NotFound,
			setter:  SetHeaders,
		},
		{
			desc:    "SetHeaders OK",
			repo:    repo,
			errType: codes.OK,
			setter:  SetHeaders,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			clientCtx, err := tc.setter(ctx, locator, tc.repo)

			if tc.errType != codes.OK {
				testhelper.RequireGrpcCode(t, err, tc.errType)
				assert.Nil(t, clientCtx)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, clientCtx)
			}
		})
	}
}

type mockGitCommandFactory struct {
	git.CommandFactory
}

func (mockGitCommandFactory) GetExecutionEnvironment(context.Context) git.ExecutionEnvironment {
	return git.ExecutionEnvironment{
		BinaryPath: "/something",
		EnvironmentVariables: []string{
			"FOO=bar",
		},
	}
}

func (mockGitCommandFactory) HooksPath(context.Context) string {
	return "custom_hooks_path"
}

func (mockGitCommandFactory) SidecarGitConfiguration(context.Context) ([]git.ConfigPair, error) {
	return []git.ConfigPair{
		{Key: "core.gc", Value: "false"},
	}, nil
}

func TestSetupEnv(t *testing.T) {
	cfg := config.Cfg{
		BinDir:     "/bin/dit",
		RuntimeDir: "/gitaly",
		Logging: config.Logging{
			Config: log.Config{
				Dir: "/log/dir",
			},
			RubySentryDSN: "testDSN",
			Sentry: config.Sentry{
				Environment: "testEnvironment",
			},
		},
		Auth: auth.Config{Token: "paswd"},
		Ruby: config.Ruby{RuggedGitConfigSearchPath: "/bin/rugged"},
	}

	env, err := setupEnv(cfg, mockGitCommandFactory{})
	require.NoError(t, err)

	require.Subset(t, env, []string{
		"FOO=bar",
		"GITALY_LOG_DIR=/log/dir",
		"GITALY_RUBY_GIT_BIN_PATH=/something",
		fmt.Sprintf("GITALY_RUBY_WRITE_BUFFER_SIZE=%d", streamio.WriteBufferSize),
		fmt.Sprintf("GITALY_RUBY_MAX_COMMIT_OR_TAG_MESSAGE_SIZE=%d", helper.MaxCommitOrTagMessageSize),
		"GITALY_RUBY_GITALY_BIN_DIR=/bin/dit",
		"GITALY_VERSION=" + version.GetVersion(),
		fmt.Sprintf("GITALY_SOCKET=%s", cfg.InternalSocketPath()),
		"GITALY_TOKEN=paswd",
		"GITALY_RUGGED_GIT_CONFIG_SEARCH_PATH=/bin/rugged",
		"SENTRY_DSN=testDSN",
		"SENTRY_ENVIRONMENT=testEnvironment",
		"GITALY_GIT_HOOKS_DIR=custom_hooks_path",
		"GIT_CONFIG_KEY_0=core.gc",
		"GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_COUNT=1",
	})
}

func TestServer_gitconfig(t *testing.T) {
	for _, tc := range []struct {
		desc              string
		setup             func(t *testing.T) (config.Cfg, string)
		expectedGitconfig string
	}{
		{
			desc: "writes a default gitconfig",
			setup: func(t *testing.T) (config.Cfg, string) {
				cfg := testcfg.Build(t)
				expectedPath := filepath.Join(cfg.RuntimeDir, "ruby-gitconfig", "gitconfig")
				return cfg, expectedPath
			},
			// If no search path is provided, we should create a placeholder file that
			// contains all settings important to Rugged.
			expectedGitconfig: "[core]\n\tfsyncObjectFiles = true\n",
		},
		{
			desc: "uses injected gitconfig",
			setup: func(t *testing.T) (config.Cfg, string) {
				gitconfigDir := testhelper.TempDir(t)
				expectedPath := filepath.Join(gitconfigDir, "gitconfig")
				require.NoError(t, os.WriteFile(expectedPath, []byte("garbage"), 0o666))

				cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{
					Ruby: config.Ruby{
						RuggedGitConfigSearchPath: gitconfigDir,
					},
				}))

				return cfg, expectedPath
			},
			// The gitconfig shouldn't have been rewritten, so it should still contain
			// garbage after the server starts.
			expectedGitconfig: "garbage",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)
			cfg, expectedPath := tc.setup(t)

			gitCmdFactory := gittest.NewCommandFactory(t, cfg)
			locator := config.NewLocator(cfg)

			rubyServer := New(cfg, gitCmdFactory)
			require.NoError(t, rubyServer.Start())
			defer rubyServer.Stop()

			// Verify that the expected gitconfig exists and has expected contents.
			gitconfigContents := testhelper.MustReadFile(t, expectedPath)
			require.Equal(t, tc.expectedGitconfig, string(gitconfigContents))

			// Write a gitconfig that is invalidly formatted. Like this we can assert
			// whether the Ruby server tries to read it or not because it should in fact
			// fail.
			require.NoError(t, os.WriteFile(expectedPath, []byte(
				`[`,
			), 0o666))

			repo, _ := gittest.InitRepo(t, cfg, cfg.Storages[0])

			// We now do any random RPC request that hits the Ruby server...
			client, err := rubyServer.WikiServiceClient(ctx)
			require.NoError(t, err)

			ctx, err = SetHeaders(ctx, locator, repo)
			require.NoError(t, err)

			stream, err := client.WikiListPages(ctx, &gitalypb.WikiListPagesRequest{Repository: repo})
			require.NoError(t, err)

			// ... and expect it to fail with an error parsing the configuration. This
			// demonstrates the config was injected successfully.
			_, err = stream.Recv()
			testhelper.RequireGrpcError(t, fmt.Errorf("Rugged::ConfigError: failed to parse config file: missing ']' in section header (in %s:1)", expectedPath), err)
		})
	}
}
