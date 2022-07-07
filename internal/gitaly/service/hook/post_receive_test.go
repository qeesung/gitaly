package hook

import (
	"bytes"
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/prometheus"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v15/streamio"
	"google.golang.org/grpc/codes"
)

func TestPostReceiveInvalidArgument(t *testing.T) {
	ctx := testhelper.Context(t)
	_, _, _, client := setupHookService(ctx, t)

	stream, err := client.PostReceiveHook(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&gitalypb.PostReceiveHookRequest{}), "empty repository should result in an error")
	_, err = stream.Recv()

	testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
}

func TestHooksMissingStdin(t *testing.T) {
	t.Parallel()

	user, password, secretToken := "user", "password", "secret token"
	tempDir := testhelper.TempDir(t)
	gitlab.WriteShellSecretFile(t, tempDir, secretToken)

	testCases := []struct {
		desc    string
		primary bool
		fail    bool
	}{
		{
			desc:    "empty stdin fails if primary",
			primary: true,
			fail:    true,
		},
		{
			desc:    "empty stdin success on secondary",
			primary: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, repo, repoPath := testcfg.BuildWithRepo(t)

			c := gitlab.TestServerOptions{
				User:                        user,
				Password:                    password,
				SecretToken:                 secretToken,
				GLID:                        "key_id",
				GLRepository:                repo.GetGlRepository(),
				Changes:                     "changes",
				PostReceiveCounterDecreased: true,
				Protocol:                    "protocol",
				RepoPath:                    repoPath,
			}

			serverURL, cleanup := gitlab.NewTestServer(t, c)
			defer cleanup()

			cfg.Gitlab = config.Gitlab{
				SecretFile: filepath.Join(tempDir, ".gitlab_shell_secret"),
				URL:        serverURL,
				HTTPSettings: config.HTTPSettings{
					User:     user,
					Password: password,
				},
			}

			gitlabClient, err := gitlab.NewHTTPClient(testhelper.NewDiscardingLogger(t), cfg.Gitlab, cfg.TLS, prometheus.Config{})
			require.NoError(t, err)

			serverSocketPath := runHooksServer(t, cfg, nil, testserver.WithGitLabClient(gitlabClient))

			client, conn := newHooksClient(t, serverSocketPath)
			defer conn.Close()
			ctx := testhelper.Context(t)

			hooksPayload, err := git.NewHooksPayload(
				cfg,
				repo,
				&txinfo.Transaction{
					ID:      1234,
					Node:    "node-1",
					Primary: tc.primary,
				},
				&git.UserDetails{
					UserID:   "key_id",
					Username: "username",
					Protocol: "protocol",
				},
				git.PostReceiveHook,
				featureflag.FromContext(ctx),
			).Env()
			require.NoError(t, err)

			stream, err := client.PostReceiveHook(ctx)
			require.NoError(t, err)
			require.NoError(t, stream.Send(&gitalypb.PostReceiveHookRequest{
				Repository: repo,
				EnvironmentVariables: []string{
					hooksPayload,
				},
			}))

			go func() {
				writer := streamio.NewWriter(func(p []byte) error {
					return stream.Send(&gitalypb.PostReceiveHookRequest{Stdin: p})
				})
				_, err := io.Copy(writer, bytes.NewBuffer(nil))
				require.NoError(t, err)
				require.NoError(t, stream.CloseSend(), "close send")
			}()

			var status int32
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}

				status = resp.GetExitStatus().GetValue()
			}

			if tc.fail {
				require.NotEqual(t, int32(0), status, "exit code should be non-zero")
			} else {
				require.Equal(t, int32(0), status, "exit code unequal")
			}
		})
	}
}

func TestPostReceiveMessages(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		desc                         string
		basicMessages, alertMessages []string
		expectedStdout               string
	}{
		{
			desc:          "basic MR message",
			basicMessages: []string{"To create a merge request for okay, visit:\n  http://localhost/project/-/merge_requests/new?merge_request"},
			expectedStdout: `
To create a merge request for okay, visit:
  http://localhost/project/-/merge_requests/new?merge_request
`,
		},
		{
			desc:          "alert",
			alertMessages: []string{"something went very wrong"},
			expectedStdout: `
========================================================================

                       something went very wrong

========================================================================
`,
		},
	}

	secretToken := "secret token"
	user, password := "user", "password"

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			cfg, repo, repoPath := testcfg.BuildWithRepo(t)

			tempDir := testhelper.TempDir(t)
			gitlab.WriteShellSecretFile(t, tempDir, secretToken)

			c := gitlab.TestServerOptions{
				User:                        user,
				Password:                    password,
				SecretToken:                 secretToken,
				GLID:                        "key_id",
				GLRepository:                repo.GetGlRepository(),
				Changes:                     "changes",
				PostReceiveCounterDecreased: true,
				PostReceiveMessages:         tc.basicMessages,
				PostReceiveAlerts:           tc.alertMessages,
				Protocol:                    "protocol",
				RepoPath:                    repoPath,
			}

			serverURL, cleanup := gitlab.NewTestServer(t, c)
			defer cleanup()

			cfg.Gitlab = config.Gitlab{
				SecretFile: filepath.Join(tempDir, ".gitlab_shell_secret"),
				URL:        serverURL,
				HTTPSettings: config.HTTPSettings{
					User:     user,
					Password: password,
				},
			}

			gitlabClient, err := gitlab.NewHTTPClient(testhelper.NewDiscardingLogger(t), cfg.Gitlab, cfg.TLS, prometheus.Config{})
			require.NoError(t, err)

			serverSocketPath := runHooksServer(t, cfg, nil, testserver.WithGitLabClient(gitlabClient))

			client, conn := newHooksClient(t, serverSocketPath)
			defer conn.Close()
			ctx := testhelper.Context(t)

			stream, err := client.PostReceiveHook(ctx)
			require.NoError(t, err)

			hooksPayload, err := git.NewHooksPayload(
				cfg,
				repo,
				nil,
				&git.UserDetails{
					UserID:   "key_id",
					Username: "username",
					Protocol: "protocol",
				},
				git.PostReceiveHook,
				featureflag.FromContext(ctx),
			).Env()
			require.NoError(t, err)

			envVars := []string{
				hooksPayload,
			}

			require.NoError(t, stream.Send(&gitalypb.PostReceiveHookRequest{
				Repository:           repo,
				EnvironmentVariables: envVars,
			}))

			go func() {
				writer := streamio.NewWriter(func(p []byte) error {
					return stream.Send(&gitalypb.PostReceiveHookRequest{Stdin: p})
				})
				_, err := writer.Write([]byte("changes"))
				require.NoError(t, err)
				require.NoError(t, stream.CloseSend(), "close send")
			}()

			var status int32
			var stdout, stderr bytes.Buffer
			for {
				resp, err := stream.Recv()
				if err == io.EOF {
					break
				}

				_, err = stdout.Write(resp.GetStdout())
				require.NoError(t, err)
				stderr.Write(resp.GetStderr())
				status = resp.GetExitStatus().GetValue()
			}

			assert.Equal(t, int32(0), status)
			assert.Equal(t, "", text.ChompBytes(stderr.Bytes()), "hook stderr")
			assert.Equal(t, tc.expectedStdout, text.ChompBytes(stdout.Bytes()), "hook stdout")
		})
	}
}
