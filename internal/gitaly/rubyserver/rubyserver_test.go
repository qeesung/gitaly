package rubyserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config/auth"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config/log"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v14/internal/version"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v14/streamio"
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
