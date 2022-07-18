//go:build !gitaly_test_sha256

package git_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	grpcmetadata "google.golang.org/grpc/metadata"
)

func TestWithRefHook(t *testing.T) {
	cfg, repo, _ := testcfg.BuildWithRepo(t)
	ctx := testhelper.Context(t)

	opt := git.WithRefTxHook(repo)
	subCmd := git.SubCmd{Name: "update-ref", Args: []string{"refs/heads/master", git.ZeroOID.String()}}

	for _, tt := range []struct {
		name string
		fn   func() (*command.Command, error)
	}{
		{
			name: "NewCommand",
			fn: func() (*command.Command, error) {
				return gittest.NewCommandFactory(t, cfg, git.WithSkipHooks()).New(ctx, repo, subCmd, opt)
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := tt.fn()
			require.NoError(t, err)
			require.NoError(t, cmd.Wait())

			var actualEnvVars []string
			for _, env := range cmd.Env() {
				kv := strings.SplitN(env, "=", 2)
				require.Len(t, kv, 2)
				key, val := kv[0], kv[1]

				if strings.HasPrefix(key, "GL_") || strings.HasPrefix(key, "GITALY_") {
					require.NotEmptyf(t, strings.TrimSpace(val),
						"env var %s value should not be empty string", key)
					actualEnvVars = append(actualEnvVars, key)
				}
			}

			require.EqualValues(t, []string{
				"GITALY_HOOKS_PAYLOAD",
				"GITALY_LOG_DIR",
			}, actualEnvVars)
		})
	}
}

func TestWithPackObjectsHookEnv(t *testing.T) {
	cfg, repo, _ := testcfg.BuildWithRepo(t)
	ctx := testhelper.Context(t)
	cfg.PackObjectsCache.Enabled = true

	userID := "user-123"
	username := "username"
	protocol := "protocol"

	opt := git.WithPackObjectsHookEnv(repo, protocol)
	subCmd := git.SubCmd{Name: "upload-pack", Args: []string{"a/b/c"}}

	ctx = grpcmetadata.AppendToOutgoingContext(ctx, "user_id", userID, "username", username)
	ctx = metadata.OutgoingToIncoming(ctx)

	cmd, err := gittest.NewCommandFactory(t, cfg, git.WithSkipHooks()).New(ctx, repo, subCmd, opt)
	require.NoError(t, err)

	payload, err := git.HooksPayloadFromEnv(cmd.Env())
	require.NoError(t, err)

	require.Equal(t, userID, payload.UserDetails.UserID)
	require.Equal(t, username, payload.UserDetails.Username)
	require.Equal(t, protocol, payload.UserDetails.Protocol)
}
