package git_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/metadata"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testcfg"
)

func TestHooksPayload(t *testing.T) {
	cfgBuilder := testcfg.NewGitalyCfgBuilder()
	defer cfgBuilder.Cleanup()
	cfg, repos := cfgBuilder.BuildWithRepoAt(t, t.Name())

	tx := metadata.Transaction{
		ID:      1234,
		Node:    "primary",
		Primary: true,
	}

	praefect := metadata.PraefectServer{
		ListenAddr:    "127.0.0.1:1234",
		TLSListenAddr: "127.0.0.1:4321",
		SocketPath:    "/path/to/unix",
		Token:         "secret",
	}

	t.Run("envvar has proper name", func(t *testing.T) {
		env, err := git.NewHooksPayload(cfg, repos[0], nil, nil, nil, git.AllHooks).Env()
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(env, git.EnvHooksPayload+"="))
	})

	t.Run("roundtrip succeeds", func(t *testing.T) {
		env, err := git.NewHooksPayload(cfg, repos[0], nil, nil, nil, git.PreReceiveHook).Env()
		require.NoError(t, err)

		payload, err := git.HooksPayloadFromEnv([]string{
			"UNRELATED=value",
			env,
			"ANOTHOR=unrelated-value",
			git.EnvHooksPayload + "_WITH_SUFFIX=is-ignored",
		})
		require.NoError(t, err)

		require.Equal(t, git.HooksPayload{
			Repo:           repos[0],
			BinDir:         cfg.BinDir,
			GitPath:        cfg.Git.BinPath,
			InternalSocket: cfg.GitalyInternalSocketPath(),
			RequestedHooks: git.PreReceiveHook,
		}, payload)
	})

	t.Run("roundtrip with transaction succeeds", func(t *testing.T) {
		env, err := git.NewHooksPayload(cfg, repos[0], &tx, &praefect, nil, git.UpdateHook).Env()
		require.NoError(t, err)

		payload, err := git.HooksPayloadFromEnv([]string{env})
		require.NoError(t, err)

		require.Equal(t, git.HooksPayload{
			Repo:           repos[0],
			BinDir:         cfg.BinDir,
			GitPath:        cfg.Git.BinPath,
			InternalSocket: cfg.GitalyInternalSocketPath(),
			Transaction:    &tx,
			Praefect:       &praefect,
			RequestedHooks: git.UpdateHook,
		}, payload)
	})

	t.Run("missing envvar", func(t *testing.T) {
		_, err := git.HooksPayloadFromEnv([]string{"OTHER_ENV=foobar"})
		require.Error(t, err)
		require.Equal(t, git.ErrPayloadNotFound, err)
	})

	t.Run("bogus value", func(t *testing.T) {
		_, err := git.HooksPayloadFromEnv([]string{git.EnvHooksPayload + "=foobar"})
		require.Error(t, err)
	})

	t.Run("payload with missing Praefect", func(t *testing.T) {
		env, err := git.NewHooksPayload(cfg, repos[0], &tx, nil, nil, git.AllHooks).Env()
		require.NoError(t, err)

		_, err = git.HooksPayloadFromEnv([]string{env})
		require.Equal(t, err, metadata.ErrPraefectServerNotFound)
	})

	t.Run("receive hooks payload", func(t *testing.T) {
		env, err := git.NewHooksPayload(cfg, repos[0], nil, nil, &git.ReceiveHooksPayload{
			UserID:   "1234",
			Username: "user",
			Protocol: "ssh",
		}, git.PostReceiveHook).Env()
		require.NoError(t, err)

		payload, err := git.HooksPayloadFromEnv([]string{
			env,
			"GL_ID=wrong",
			"GL_USERNAME=wrong",
			"GL_PROTOCOL=wrong",
		})
		require.NoError(t, err)

		require.Equal(t, git.HooksPayload{
			Repo:                repos[0],
			BinDir:              cfg.BinDir,
			GitPath:             cfg.Git.BinPath,
			InternalSocket:      cfg.GitalyInternalSocketPath(),
			InternalSocketToken: cfg.Auth.Token,
			ReceiveHooksPayload: &git.ReceiveHooksPayload{
				UserID:   "1234",
				Username: "user",
				Protocol: "ssh",
			},
			RequestedHooks: git.PostReceiveHook,
		}, payload)
	})

	t.Run("payload with fallback git path", func(t *testing.T) {
		cfgBuilder := testcfg.NewGitalyCfgBuilder()
		defer cfgBuilder.Cleanup()
		cfg, repos := cfgBuilder.BuildWithRepoAt(t, t.Name())
		cfg.Git.BinPath = ""

		env, err := git.NewHooksPayload(cfg, repos[0], nil, nil, nil, git.ReceivePackHooks).Env()
		require.NoError(t, err)

		payload, err := git.HooksPayloadFromEnv([]string{
			env,
			"GITALY_GIT_BIN_PATH=/foo/bar",
		})
		require.NoError(t, err)
		require.Equal(t, git.HooksPayload{
			Repo:           repos[0],
			BinDir:         cfg.BinDir,
			GitPath:        "/foo/bar",
			InternalSocket: cfg.GitalyInternalSocketPath(),
			RequestedHooks: git.ReceivePackHooks,
		}, payload)
	})
}

func TestHooksPayload_IsHookRequested(t *testing.T) {
	for _, tc := range []struct {
		desc       string
		configured git.Hook
		request    git.Hook
		expected   bool
	}{
		{
			desc:       "exact match",
			configured: git.PreReceiveHook,
			request:    git.PreReceiveHook,
			expected:   true,
		},
		{
			desc:       "hook matches a set",
			configured: git.PreReceiveHook | git.PostReceiveHook,
			request:    git.PreReceiveHook,
			expected:   true,
		},
		{
			desc:       "no match",
			configured: git.PreReceiveHook,
			request:    git.PostReceiveHook,
			expected:   false,
		},
		{
			desc:       "no match with a set",
			configured: git.PreReceiveHook | git.UpdateHook,
			request:    git.PostReceiveHook,
			expected:   false,
		},
		{
			desc:       "no match with nothing set",
			configured: 0,
			request:    git.PostReceiveHook,
			expected:   false,
		},
		{
			desc:       "pre-receive hook with AllHooks",
			configured: git.AllHooks,
			request:    git.PreReceiveHook,
			expected:   true,
		},
		{
			desc:       "post-receive hook with AllHooks",
			configured: git.AllHooks,
			request:    git.PostReceiveHook,
			expected:   true,
		},
		{
			desc:       "update hook with AllHooks",
			configured: git.AllHooks,
			request:    git.UpdateHook,
			expected:   true,
		},
		{
			desc:       "reference-transaction hook with AllHooks",
			configured: git.AllHooks,
			request:    git.ReferenceTransactionHook,
			expected:   true,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			actual := git.HooksPayload{
				RequestedHooks: tc.configured,
			}.IsHookRequested(tc.request)
			require.Equal(t, tc.expected, actual)
		})
	}
}
