package hook

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/metadata"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestPrereceive_customHooks(t *testing.T) {
	repo, repoPath, cleanup := testhelper.NewTestRepo(t)
	defer cleanup()

	hookManager := NewManager(config.NewLocator(config.Config), GitlabAPIStub, config.Config)

	standardEnv := []string{
		"GL_ID=1234",
		fmt.Sprintf("GL_PROJECT_PATH=%s", repo.GetGlProjectPath()),
		"GL_PROTOCOL=web",
		fmt.Sprintf("GL_REPO=%s", repo),
		fmt.Sprintf("GL_REPOSITORY=%s", repo.GetGlRepository()),
		"GL_USERNAME=user",
	}

	payload, err := git.NewHooksPayload(config.Config, repo, nil, nil).Env()
	require.NoError(t, err)

	primaryPayload, err := git.NewHooksPayload(
		config.Config,
		repo,
		&metadata.Transaction{
			ID: 1234, Node: "primary", Primary: true,
		},
		&metadata.PraefectServer{
			SocketPath: "/path/to/socket",
			Token:      "secret",
		},
	).Env()
	require.NoError(t, err)

	secondaryPayload, err := git.NewHooksPayload(
		config.Config,
		repo,
		&metadata.Transaction{
			ID: 1234, Node: "secondary", Primary: false,
		},
		&metadata.PraefectServer{
			SocketPath: "/path/to/socket",
			Token:      "secret",
		},
	).Env()
	require.NoError(t, err)

	testCases := []struct {
		desc           string
		env            []string
		hook           string
		stdin          string
		expectedErr    string
		expectedStdout string
		expectedStderr string
	}{
		{
			desc:           "hook receives environment variables",
			env:            append(standardEnv, payload),
			hook:           "#!/bin/sh\nenv | grep -e '^GL_' -e '^GITALY_' | sort\n",
			stdin:          "change\n",
			expectedStdout: strings.Join(append([]string{payload}, standardEnv...), "\n") + "\n",
		},
		{
			desc:           "hook can write to stderr and stdout",
			env:            append(standardEnv, payload),
			hook:           "#!/bin/sh\necho foo >&1 && echo bar >&2\n",
			stdin:          "change\n",
			expectedStdout: "foo\n",
			expectedStderr: "bar\n",
		},
		{
			desc:           "hook receives standard input",
			env:            append(standardEnv, payload),
			hook:           "#!/bin/sh\ncat\n",
			stdin:          "foo\n",
			expectedStdout: "foo\n",
		},
		{
			desc:           "hook succeeds without consuming stdin",
			env:            append(standardEnv, payload),
			hook:           "#!/bin/sh\necho foo\n",
			stdin:          "ignore me\n",
			expectedStdout: "foo\n",
		},
		{
			desc:        "invalid hook results in error",
			env:         append(standardEnv, payload),
			hook:        "",
			stdin:       "change\n",
			expectedErr: "exec format error",
		},
		{
			desc:        "failing hook results in error",
			env:         append(standardEnv, payload),
			hook:        "#!/bin/sh\nexit 123",
			stdin:       "change\n",
			expectedErr: "exit status 123",
		},
		{
			desc:           "hook is executed on primary",
			env:            append(standardEnv, primaryPayload),
			hook:           "#!/bin/sh\necho foo\n",
			stdin:          "change\n",
			expectedStdout: "foo\n",
		},
		{
			desc:  "hook is not executed on secondary",
			env:   append(standardEnv, secondaryPayload),
			hook:  "#!/bin/sh\necho foo\n",
			stdin: "change\n",
		},
		{
			desc:        "missing GL_ID causes error",
			env:         envWithout(append(standardEnv, payload), "GL_ID"),
			stdin:       "change\n",
			expectedErr: "GL_ID not set",
		},
		{
			desc:        "missing GL_REPOSITORY causes error",
			env:         envWithout(append(standardEnv, payload), "GL_REPOSITORY"),
			stdin:       "change\n",
			expectedErr: "GL_REPOSITORY not set",
		},
		{
			desc:        "missing GL_PROTOCOL causes error",
			env:         envWithout(append(standardEnv, payload), "GL_PROTOCOL"),
			stdin:       "change\n",
			expectedErr: "GL_PROTOCOL not set",
		},
		{
			desc:        "missing changes cause error",
			env:         append(standardEnv, payload),
			expectedErr: "hook got no reference updates",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, cleanup := testhelper.Context()
			defer cleanup()

			cleanup, err := testhelper.WriteCustomHook(repoPath, "pre-receive", []byte(tc.hook))
			require.NoError(t, err)
			defer cleanup()

			var stdout, stderr bytes.Buffer
			err = hookManager.PreReceiveHook(ctx, repo, tc.env, strings.NewReader(tc.stdin), &stdout, &stderr)

			if tc.expectedErr != "" {
				require.Contains(t, err.Error(), tc.expectedErr)
			} else {
				require.NoError(t, err)
			}

			require.Equal(t, tc.expectedStdout, stdout.String())
			require.Equal(t, tc.expectedStderr, stderr.String())
		})
	}
}

type prereceiveAPIMock struct {
	allowed    func(context.Context, AllowedParams) (bool, string, error)
	prereceive func(context.Context, string) (bool, error)
}

func (m *prereceiveAPIMock) Allowed(ctx context.Context, params AllowedParams) (bool, string, error) {
	return m.allowed(ctx, params)
}

func (m *prereceiveAPIMock) PreReceive(ctx context.Context, glRepository string) (bool, error) {
	return m.prereceive(ctx, glRepository)
}

func (m *prereceiveAPIMock) Check(ctx context.Context) (*CheckInfo, error) {
	return nil, errors.New("unexpected call")
}

func (m *prereceiveAPIMock) PostReceive(context.Context, string, string, string, ...string) (bool, []PostReceiveMessage, error) {
	return true, nil, errors.New("unexpected call")
}

func TestPrereceive_gitlab(t *testing.T) {
	testRepo, testRepoPath, cleanup := testhelper.NewTestRepo(t)
	defer cleanup()

	payload, err := git.NewHooksPayload(config.Config, testRepo, nil, nil).Env()
	require.NoError(t, err)

	standardEnv := []string{
		payload,
		"GL_ID=1234",
		fmt.Sprintf("GL_PROJECT_PATH=%s", testRepo.GetGlProjectPath()),
		"GL_PROTOCOL=web",
		fmt.Sprintf("GL_REPO=%s", testRepo),
		fmt.Sprintf("GL_REPOSITORY=%s", testRepo.GetGlRepository()),
		"GL_USERNAME=user",
	}

	testCases := []struct {
		desc           string
		env            []string
		changes        string
		allowed        func(*testing.T, context.Context, AllowedParams) (bool, string, error)
		prereceive     func(*testing.T, context.Context, string) (bool, error)
		expectHookCall bool
		expectedErr    error
	}{
		{
			desc:    "allowed change",
			env:     standardEnv,
			changes: "changes\n",
			allowed: func(t *testing.T, ctx context.Context, params AllowedParams) (bool, string, error) {
				require.Equal(t, testRepoPath, params.RepoPath)
				require.Equal(t, testRepo.GlRepository, params.GLRepository)
				require.Equal(t, "1234", params.GLID)
				require.Equal(t, "web", params.GLProtocol)
				require.Equal(t, "changes\n", params.Changes)
				return true, "", nil
			},
			prereceive: func(t *testing.T, ctx context.Context, glRepo string) (bool, error) {
				require.Equal(t, testRepo.GlRepository, glRepo)
				return true, nil
			},
			expectHookCall: true,
		},
		{
			desc:    "disallowed change",
			env:     standardEnv,
			changes: "changes\n",
			allowed: func(t *testing.T, ctx context.Context, params AllowedParams) (bool, string, error) {
				return false, "you shall not pass", nil
			},
			expectHookCall: false,
			expectedErr:    errors.New("you shall not pass"),
		},
		{
			desc:    "allowed returns error",
			env:     standardEnv,
			changes: "changes\n",
			allowed: func(t *testing.T, ctx context.Context, params AllowedParams) (bool, string, error) {
				return false, "", errors.New("oops")
			},
			expectHookCall: false,
			expectedErr:    errors.New("GitLab: oops"),
		},
		{
			desc:    "prereceive rejects",
			env:     standardEnv,
			changes: "changes\n",
			allowed: func(t *testing.T, ctx context.Context, params AllowedParams) (bool, string, error) {
				return true, "", nil
			},
			prereceive: func(t *testing.T, ctx context.Context, glRepo string) (bool, error) {
				return false, nil
			},
			expectHookCall: true,
			expectedErr:    errors.New(""),
		},
		{
			desc:    "prereceive errors",
			env:     standardEnv,
			changes: "changes\n",
			allowed: func(t *testing.T, ctx context.Context, params AllowedParams) (bool, string, error) {
				return true, "", nil
			},
			prereceive: func(t *testing.T, ctx context.Context, glRepo string) (bool, error) {
				return false, errors.New("prereceive oops")
			},
			expectHookCall: true,
			expectedErr:    helper.ErrInternalf("calling pre_receive endpoint: %v", errors.New("prereceive oops")),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, cleanup := testhelper.Context()
			defer cleanup()

			gitlabAPI := prereceiveAPIMock{
				allowed: func(ctx context.Context, params AllowedParams) (bool, string, error) {
					return tc.allowed(t, ctx, params)
				},
				prereceive: func(ctx context.Context, glRepo string) (bool, error) {
					return tc.prereceive(t, ctx, glRepo)
				},
			}

			hookManager := NewManager(config.NewLocator(config.Config), &gitlabAPI, config.Config)

			cleanup, err := testhelper.WriteCustomHook(testRepoPath, "pre-receive", []byte("#!/bin/sh\necho called\n"))
			require.NoError(t, err)
			defer cleanup()

			var stdout, stderr bytes.Buffer
			err = hookManager.PreReceiveHook(ctx, testRepo, tc.env, strings.NewReader(tc.changes), &stdout, &stderr)

			if tc.expectedErr != nil {
				require.Equal(t, tc.expectedErr, err)
			} else {
				require.NoError(t, err)
			}

			if tc.expectHookCall {
				require.Equal(t, "called\n", stdout.String())
			} else {
				require.Empty(t, stdout.String())
			}
			require.Empty(t, stderr.String())
		})
	}
}
