package git

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/metadata"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestHooksPayload(t *testing.T) {
	repo, _, cleanup := testhelper.NewTestRepo(t)
	defer cleanup()

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
		env, err := NewHooksPayload(config.Config, repo, nil, nil, nil).Env()
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(env, ErrHooksPayloadNotFound+"="))
	})

	t.Run("roundtrip succeeds", func(t *testing.T) {
		env, err := NewHooksPayload(config.Config, repo, nil, nil, nil).Env()
		require.NoError(t, err)

		payload, err := HooksPayloadFromEnv([]string{
			"UNRELATED=value",
			env,
			"ANOTHOR=unrelated-value",
			ErrHooksPayloadNotFound + "_WITH_SUFFIX=is-ignored",
		})
		require.NoError(t, err)

		require.Equal(t, HooksPayload{
			Repo:           repo,
			BinDir:         config.Config.BinDir,
			InternalSocket: config.Config.GitalyInternalSocketPath(),
		}, payload)
	})

	t.Run("roundtrip with transaction succeeds", func(t *testing.T) {
		env, err := NewHooksPayload(config.Config, repo, &tx, &praefect, nil).Env()
		require.NoError(t, err)

		payload, err := HooksPayloadFromEnv([]string{env})
		require.NoError(t, err)

		require.Equal(t, HooksPayload{
			Repo:           repo,
			BinDir:         config.Config.BinDir,
			InternalSocket: config.Config.GitalyInternalSocketPath(),
			Transaction:    &tx,
			Praefect:       &praefect,
		}, payload)
	})

	t.Run("missing envvar", func(t *testing.T) {
		_, err := HooksPayloadFromEnv([]string{"OTHER_ENV=foobar"})
		require.Error(t, err)
		require.Equal(t, ErrPayloadNotFound, err)
	})

	t.Run("bogus value", func(t *testing.T) {
		_, err := HooksPayloadFromEnv([]string{ErrHooksPayloadNotFound + "=foobar"})
		require.Error(t, err)
	})

	t.Run("payload with missing Praefect", func(t *testing.T) {
		env, err := NewHooksPayload(config.Config, repo, &tx, nil, nil).Env()
		require.NoError(t, err)

		_, err = HooksPayloadFromEnv([]string{env})
		require.Equal(t, err, metadata.ErrPraefectServerNotFound)
	})

	t.Run("receive hooks payload", func(t *testing.T) {
		env, err := NewHooksPayload(config.Config, repo, nil, nil, &ReceiveHooksPayload{
			UserID:   "1234",
			Username: "user",
			Protocol: "ssh",
		}).Env()
		require.NoError(t, err)

		payload, err := HooksPayloadFromEnv([]string{
			env,
			"GL_ID=wrong",
			"GL_USERNAME=wrong",
			"GL_PROTOCOL=wrong",
		})
		require.NoError(t, err)

		require.Equal(t, HooksPayload{
			Repo:                repo,
			BinDir:              config.Config.BinDir,
			InternalSocket:      config.Config.GitalyInternalSocketPath(),
			InternalSocketToken: config.Config.Auth.Token,
			ReceiveHooksPayload: &ReceiveHooksPayload{
				UserID:   "1234",
				Username: "user",
				Protocol: "ssh",
			},
		}, payload)
	})
}
