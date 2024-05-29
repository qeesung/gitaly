package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/pktline"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/auth"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/prometheus"
	gitalyhook "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/service/hook"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/limiter"
	gitalylog "gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/labkit/correlation"
	labkitcorrelation "gitlab.com/gitlab-org/labkit/correlation/grpc"
	labkittracing "gitlab.com/gitlab-org/labkit/tracing"
	"google.golang.org/grpc"
	grpcmetadata "google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/durationpb"
)

type glHookValues struct {
	GLID, GLUsername, GLProtocol, GitObjectDir, RemoteIP string
	GitalyLogDir                                         string
	GitAlternateObjectDirs                               []string
}

type proxyValues struct {
	HTTPProxy, HTTPSProxy, NoProxy string
}

var (
	enabledFeatureFlag  = featureflag.FeatureFlag{Name: "enabled-feature-flag", OnByDefault: false}
	disabledFeatureFlag = featureflag.FeatureFlag{Name: "disabled-feature-flag", OnByDefault: true}
	correlationID       = "correlation-id-999"
	glID                = "key-1234"
	glUsername          = "iamgitlab"
	remoteIP            = "1.2.3.4"
)

func featureFlags(ctx context.Context) map[featureflag.FeatureFlag]bool {
	ctx = featureflag.IncomingCtxWithFeatureFlag(ctx, enabledFeatureFlag, true)
	ctx = featureflag.IncomingCtxWithFeatureFlag(ctx, disabledFeatureFlag, false)
	return featureflag.FromContext(ctx)
}

// envForHooks generates a set of environment variables for gitaly hooks
func envForHooks(tb testing.TB, ctx context.Context, cfg config.Cfg, repo *gitalypb.Repository, glHookValues glHookValues, proxyValues proxyValues, gitPushOptions ...string) []string {
	payload, err := git.NewHooksPayload(
		cfg,
		repo,
		gittest.DefaultObjectHash,
		nil,
		&git.UserDetails{
			UserID:   glHookValues.GLID,
			Username: glHookValues.GLUsername,
			Protocol: glHookValues.GLProtocol,
			RemoteIP: glHookValues.RemoteIP,
		},
		git.AllHooks,
		featureFlags(ctx),
		storage.ExtractTransactionID(ctx),
	).Env()
	require.NoError(tb, err)

	env := append(command.AllowedEnvironment(os.Environ()), []string{
		payload,
		fmt.Sprintf("%s=%s", gitalylog.GitalyLogDirEnvKey, cfg.Logging.Dir),
		"GITLAB_TRACING=opentracing://jaeger",
		"user-tracer-id=1:2:3:4",
	}...)
	env = append(env, gitPushOptions...)

	ctx = correlation.ContextWithCorrelation(ctx, correlationID)
	env = labkittracing.NewEnvInjector()(ctx, env)

	if proxyValues.HTTPProxy != "" {
		env = append(env, fmt.Sprintf("HTTP_PROXY=%s", proxyValues.HTTPProxy))
		env = append(env, fmt.Sprintf("http_proxy=%s", proxyValues.HTTPProxy))
	}
	if proxyValues.HTTPSProxy != "" {
		env = append(env, fmt.Sprintf("HTTPS_PROXY=%s", proxyValues.HTTPSProxy))
		env = append(env, fmt.Sprintf("https_proxy=%s", proxyValues.HTTPSProxy))
	}
	if proxyValues.NoProxy != "" {
		env = append(env, fmt.Sprintf("NO_PROXY=%s", proxyValues.NoProxy))
		env = append(env, fmt.Sprintf("no_proxy=%s", proxyValues.NoProxy))
	}

	if glHookValues.GitObjectDir != "" {
		env = append(env, fmt.Sprintf("GIT_OBJECT_DIRECTORY=%s", glHookValues.GitObjectDir))
	}
	if len(glHookValues.GitAlternateObjectDirs) > 0 {
		env = append(env, fmt.Sprintf("GIT_ALTERNATE_OBJECT_DIRECTORIES=%s", strings.Join(glHookValues.GitAlternateObjectDirs, ":")))
	}
	if glHookValues.GitalyLogDir != "" {
		env = append(env, fmt.Sprintf("GITALY_LOG_DIR=%s", glHookValues.GitalyLogDir))
	}

	return env
}

func TestHooksPrePostWithSymlinkedStoragePath(t *testing.T) {
	tempDir := testhelper.TempDir(t)

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	testcfg.BuildGitalyHooks(t, cfg)
	testcfg.BuildGitalySSH(t, cfg)

	originalStoragePath := cfg.Storages[0].Path
	symlinkedStoragePath := filepath.Join(tempDir, "storage")
	require.NoError(t, os.Symlink(originalStoragePath, symlinkedStoragePath))
	cfg.Storages[0].Path = symlinkedStoragePath

	testHooksPrePostReceive(t, cfg, repo, repoPath)
}

func TestHooksPrePostReceive(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	testcfg.BuildGitalyHooks(t, cfg)
	testcfg.BuildGitalySSH(t, cfg)
	testHooksPrePostReceive(t, cfg, repo, repoPath)
}

func testHooksPrePostReceive(t *testing.T, cfg config.Cfg, repo *gitalypb.Repository, repoPath string) {
	ctx := testhelper.Context(t)

	secretToken := "secret token"
	glProtocol := "ssh"
	changes := "abc"

	gitPushOptions := []string{"gitpushoption1", "gitpushoption2"}
	gitObjectDir := filepath.Join(repoPath, "objects", "temp")
	gitAlternateObjectDirs := []string{filepath.Join(repoPath, "objects")}

	gitlabUser, gitlabPassword := "gitlab_user-1234", "gitlabsecret9887"
	httpProxy, httpsProxy, noProxy := "http://test.example.com:8080", "https://test.example.com:8080", "*"

	logger := testhelper.NewLogger(t)

	c := gitlab.TestServerOptions{
		User:                        gitlabUser,
		Password:                    gitlabPassword,
		SecretToken:                 secretToken,
		GLID:                        glID,
		GLRepository:                repo.GetGlRepository(),
		Changes:                     changes,
		PostReceiveCounterDecreased: true,
		Protocol:                    "ssh",
		GitPushOptions:              gitPushOptions,
		GitObjectDir:                gitObjectDir,
		GitAlternateObjectDirs:      gitAlternateObjectDirs,
		RepoPath:                    repoPath,
	}

	gitlabURL, cleanup := gitlab.NewTestServer(t, c)
	defer cleanup()
	cfg.Gitlab.URL = gitlabURL
	cfg.Gitlab.SecretFile = gitlab.WriteShellSecretFile(t, cfg.GitlabShell.Dir, secretToken)
	cfg.Gitlab.HTTPSettings.User = gitlabUser
	cfg.Gitlab.HTTPSettings.Password = gitlabPassword

	gitlabClient, err := gitlab.NewHTTPClient(logger, cfg.Gitlab, cfg.TLS, prometheus.Config{})
	require.NoError(t, err)

	runHookServiceWithGitlabClient(t, cfg, true, gitlabClient)

	gitObjectDirRegex := regexp.MustCompile(`(?m)^GIT_OBJECT_DIRECTORY=(.*)$`)
	gitAlternateObjectDirRegex := regexp.MustCompile(`(?m)^GIT_ALTERNATE_OBJECT_DIRECTORIES=(.*)$`)

	hookNames := []string{"pre-receive", "post-receive"}

	for _, hookName := range hookNames {
		t.Run(fmt.Sprintf("hookName: %s", hookName), func(t *testing.T) {
			customHookOutputPath := gittest.WriteEnvToCustomHook(t, repoPath, hookName)

			var stderr, stdout bytes.Buffer
			stdin := bytes.NewBuffer([]byte(changes))
			require.NoError(t, err)
			cmd := exec.Command(cfg.BinaryPath("gitaly-hooks"))
			cmd.Args = []string{hookName}
			cmd.Stderr = &stderr
			cmd.Stdout = &stdout
			cmd.Stdin = stdin
			cmd.Env = envForHooks(
				t,
				ctx,
				cfg,
				repo,
				glHookValues{
					GLID:                   glID,
					GLUsername:             glUsername,
					GLProtocol:             glProtocol,
					GitObjectDir:           c.GitObjectDir,
					GitAlternateObjectDirs: c.GitAlternateObjectDirs,
					RemoteIP:               remoteIP,
				},
				proxyValues{
					HTTPProxy:  httpProxy,
					HTTPSProxy: httpsProxy,
					NoProxy:    noProxy,
				},
				"GIT_PUSH_OPTION_COUNT=2",
				"GIT_PUSH_OPTION_0=gitpushoption1",
				"GIT_PUSH_OPTION_1=gitpushoption2",
			)

			cmd.Dir = repoPath

			err = cmd.Run()
			assert.Empty(t, stderr.String())
			assert.Empty(t, stdout.String())
			require.NoError(t, err)

			output := string(testhelper.MustReadFile(t, customHookOutputPath))
			requireContainsOnce(t, output, "GL_USERNAME="+glUsername)
			requireContainsOnce(t, output, "GL_ID="+glID)
			requireContainsOnce(t, output, "GL_REPOSITORY="+repo.GetGlRepository())
			requireContainsOnce(t, output, "GL_PROTOCOL="+glProtocol)
			requireContainsOnce(t, output, "GIT_PUSH_OPTION_COUNT=2")
			requireContainsOnce(t, output, "GIT_PUSH_OPTION_0=gitpushoption1")
			requireContainsOnce(t, output, "GIT_PUSH_OPTION_1=gitpushoption2")
			requireContainsOnce(t, output, "HTTP_PROXY="+httpProxy)
			requireContainsOnce(t, output, "http_proxy="+httpProxy)
			requireContainsOnce(t, output, "HTTPS_PROXY="+httpsProxy)
			requireContainsOnce(t, output, "https_proxy="+httpsProxy)
			requireContainsOnce(t, output, "no_proxy="+noProxy)
			requireContainsOnce(t, output, "NO_PROXY="+noProxy)

			if hookName == "pre-receive" {
				gitObjectDirMatches := gitObjectDirRegex.FindStringSubmatch(output)
				require.Len(t, gitObjectDirMatches, 2)
				require.Equal(t, gitObjectDir, gitObjectDirMatches[1])

				gitAlternateObjectDirMatches := gitAlternateObjectDirRegex.FindStringSubmatch(output)
				require.Len(t, gitAlternateObjectDirMatches, 2)
				require.Equal(t, strings.Join(gitAlternateObjectDirs, ":"), gitAlternateObjectDirMatches[1])
			} else {
				require.Contains(t, output, "GL_PROTOCOL="+glProtocol)
			}
		})
	}
}

func TestHooksUpdate(t *testing.T) {
	ctx := testhelper.Context(t)

	glProtocol := "ssh"

	customHooksDir := testhelper.TempDir(t)

	cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{
		Auth:  auth.Config{Token: "abc123"},
		Hooks: config.Hooks{CustomHooksDir: customHooksDir},
	}))
	testcfg.BuildGitalyHooks(t, cfg)
	testcfg.BuildGitalySSH(t, cfg)

	require.NoError(t, os.Symlink(filepath.Join(cfg.GitlabShell.Dir, "config.yml"), filepath.Join(cfg.GitlabShell.Dir, "config.yml")))

	cfg.Gitlab.SecretFile = gitlab.WriteShellSecretFile(t, cfg.GitlabShell.Dir, "the wrong token")

	runHookServiceServer(t, cfg, true)

	testHooksUpdate(t, ctx, cfg, glHookValues{
		GLID:       glID,
		GLUsername: glUsername,
		GLProtocol: glProtocol,
		RemoteIP:   remoteIP,
	})
}

func testHooksUpdate(t *testing.T, ctx context.Context, cfg config.Cfg, glValues glHookValues) {
	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	refval := "refval"
	oldval := gittest.DefaultObjectHash.ZeroOID.String()
	newval := gittest.DefaultObjectHash.ZeroOID.String()

	// Write a custom update hook that dumps all arguments seen by the hook...
	customHookArgsPath := filepath.Join(testhelper.TempDir(t), "containsarguments")
	testhelper.WriteExecutable(t,
		filepath.Join(repoPath, "custom_hooks", "update.d", "dumpargsscript"),
		[]byte(fmt.Sprintf(`#!/usr/bin/env bash
			echo "$@" >%q
		`, customHookArgsPath)),
	)

	// ... and a second custom hook that dumps the environment variables.
	customHookEnvPath := gittest.WriteEnvToCustomHook(t, repoPath, "update")

	var stdout, stderr bytes.Buffer
	cmd := exec.Command(cfg.BinaryPath("gitaly-hooks"))
	cmd.Args = []string{"update", refval, oldval, newval}
	cmd.Env = envForHooks(t, ctx, cfg, repo, glValues, proxyValues{})
	cmd.Dir = repoPath
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = repoPath

	require.NoError(t, cmd.Run())
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())

	// Ensure that the hook was executed with the expected arguments...
	require.Equal(t,
		fmt.Sprintf("%s %s %s", refval, oldval, newval),
		text.ChompBytes(testhelper.MustReadFile(t, customHookArgsPath)),
	)

	// ... and with the expected environment variables.
	output := string(testhelper.MustReadFile(t, customHookEnvPath))
	require.Contains(t, output, "GL_USERNAME="+glValues.GLUsername)
	require.Contains(t, output, "GL_ID="+glValues.GLID)
	require.Contains(t, output, "GL_REPOSITORY="+repo.GetGlRepository())
	require.Contains(t, output, "GL_PROTOCOL="+glValues.GLProtocol)
}

func TestHooksPostReceiveFailed(t *testing.T) {
	t.Parallel()

	secretToken := "secret token"
	glProtocol := "ssh"
	changes := "oldhead newhead"

	logger := testhelper.NewLogger(t)

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{Auth: auth.Config{Token: "abc123"}}))

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	gitalyHooksPath := testcfg.BuildGitalyHooks(t, cfg)
	testcfg.BuildGitalySSH(t, cfg)

	// By setting the last parameter to false, the post-receive API call will
	// send back {"reference_counter_increased": false}, indicating something went wrong
	// with the call

	c := gitlab.TestServerOptions{
		User:                        "",
		Password:                    "",
		SecretToken:                 secretToken,
		Changes:                     changes,
		GLID:                        glID,
		GLRepository:                repo.GetGlRepository(),
		PostReceiveCounterDecreased: false,
		Protocol:                    "ssh",
	}
	serverURL, cleanup := gitlab.NewTestServer(t, c)
	defer cleanup()
	cfg.Gitlab.URL = serverURL
	cfg.Gitlab.SecretFile = gitlab.WriteShellSecretFile(t, cfg.GitlabShell.Dir, secretToken)

	gitlabClient, err := gitlab.NewHTTPClient(logger, cfg.Gitlab, cfg.TLS, prometheus.Config{})
	require.NoError(t, err)

	txManager := transaction.NewTrackingManager()

	runHookServiceWithGitlabClient(t, cfg, true, gitlabClient, testserver.WithTransactionManager(txManager))

	customHookOutputPath := gittest.WriteEnvToCustomHook(t, repoPath, "post-receive")

	var stdout, stderr bytes.Buffer

	testcases := []struct {
		desc    string
		primary bool
		verify  func(*testing.T, *exec.Cmd, *bytes.Buffer, *bytes.Buffer)
	}{
		{
			desc:    "Primary calls out to post_receive endpoint",
			primary: true,
			verify: func(t *testing.T, cmd *exec.Cmd, stdout, stderr *bytes.Buffer) {
				err = cmd.Run()
				code, ok := command.ExitStatus(err)
				require.True(t, ok, "expect exit status in %v", err)

				require.Equal(t, 1, code, "exit status")
				require.Empty(t, stdout.String())
				require.Empty(t, stderr.String())
				require.NoFileExists(t, customHookOutputPath)
				require.Empty(t, txManager.Votes())
			},
		},
		{
			desc:    "Secondary does not call out to post_receive endpoint",
			primary: false,
			verify: func(t *testing.T, cmd *exec.Cmd, stdout, stderr *bytes.Buffer) {
				err = cmd.Run()
				require.NoError(t, err)

				require.Empty(t, stdout.String())
				require.Empty(t, stderr.String())
				require.NoFileExists(t, customHookOutputPath)
				require.Len(t, txManager.Votes(), 1)
			},
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			txManager.Reset()

			hooksPayload, err := git.NewHooksPayload(
				cfg,
				repo,
				gittest.DefaultObjectHash,
				&txinfo.Transaction{
					ID:      1,
					Node:    "node",
					Primary: tc.primary,
				},
				&git.UserDetails{
					UserID:   glID,
					Username: glUsername,
					Protocol: glProtocol,
					RemoteIP: remoteIP,
				},
				git.PostReceiveHook,
				featureFlags(ctx),
				storage.ExtractTransactionID(ctx),
			).Env()
			require.NoError(t, err)

			env := envForHooks(t, ctx, cfg, repo, glHookValues{}, proxyValues{})
			env = append(env, hooksPayload)

			cmd := exec.Command(gitalyHooksPath)
			cmd.Args = []string{"post-receive"}
			cmd.Env = env
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			cmd.Stdin = bytes.NewBuffer([]byte(changes))
			cmd.Dir = repoPath

			tc.verify(t, cmd, &stdout, &stderr)
		})
	}
}

func TestHooksProcReceive(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)
	hooksPath := testcfg.BuildGitalyHooks(t, cfg)
	registry := gitalyhook.NewProcReceiveRegistry()
	runHookServiceServer(t, cfg, false, testserver.WithProcReceiveRegistry(registry))

	repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	for _, tc := range []struct {
		desc             string
		transactionID    storage.TransactionID
		expectedStdout   string
		expectedStderr   string
		expectedExitCode int
	}{
		{
			desc:             "failed proc-receive execution",
			transactionID:    0, // A transaction ID with a value of zero is invalid.
			expectedStderr:   "error executing git hook\n",
			expectedExitCode: 1,
		},
		{
			desc:             "successful proc-receive execution",
			transactionID:    1,
			expectedStdout:   "0015version=1\x00 atomic00000018ok refs/heads/main_10000",
			expectedExitCode: 0,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			handlers, cleanup, err := registry.RegisterWaiter(tc.transactionID)
			require.NoError(t, err)
			defer cleanup()

			hooksPayload, err := git.NewHooksPayload(
				cfg,
				repo,
				gittest.DefaultObjectHash,
				nil,
				nil,
				git.ProcReceiveHook,
				featureFlags(ctx),
				tc.transactionID,
			).Env()
			require.NoError(t, err)

			env := envForHooks(t, ctx, cfg, repo, glHookValues{}, proxyValues{})
			env = append(env, hooksPayload)

			var stdin bytes.Buffer
			_, err = pktline.WriteString(&stdin, "version=1\000 atomic")
			require.NoError(t, err)
			require.NoError(t, pktline.WriteFlush(&stdin))
			ref := git.ReferenceName(fmt.Sprintf("refs/heads/main_%d", tc.transactionID))
			_, err = pktline.WriteString(&stdin, fmt.Sprintf("%s %s %s",
				gittest.DefaultObjectHash.ZeroOID, gittest.DefaultObjectHash.EmptyTreeOID, ref))
			require.NoError(t, err)
			require.NoError(t, pktline.WriteFlush(&stdin))

			var stdout, stderr bytes.Buffer
			cmd := exec.Command(hooksPath)
			cmd.Args = []string{"proc-receive"}
			cmd.Env = env
			cmd.Stdin = &stdin
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			require.NoError(t, cmd.Start())

			if tc.expectedExitCode == 0 {
				handler := <-handlers
				require.Equal(t, tc.transactionID, handler.TransactionID())
				require.NoError(t, handler.AcceptUpdate(ref))
				require.NoError(t, handler.Close(nil))
			}

			err = cmd.Wait()
			code, _ := command.ExitStatus(err)
			require.Equal(t, tc.expectedExitCode, code)
			require.Equal(t, tc.expectedStdout, stdout.String())
			require.Equal(t, tc.expectedStderr, stderr.String())
		})
	}
}

func TestHooksNotAllowed(t *testing.T) {
	secretToken := "secret token"
	glProtocol := "ssh"
	changes := "oldhead newhead"

	logger := testhelper.NewLogger(t)

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{Auth: auth.Config{Token: "abc123"}}))

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	gitalyHooksPath := testcfg.BuildGitalyHooks(t, cfg)
	testcfg.BuildGitalySSH(t, cfg)

	c := gitlab.TestServerOptions{
		User:                        "",
		Password:                    "",
		SecretToken:                 secretToken,
		GLID:                        glID,
		GLRepository:                repo.GetGlRepository(),
		Changes:                     changes,
		PostReceiveCounterDecreased: true,
		Protocol:                    "ssh",
	}
	serverURL, cleanup := gitlab.NewTestServer(t, c)
	defer cleanup()

	cfg.Gitlab.URL = serverURL
	cfg.Gitlab.SecretFile = gitlab.WriteShellSecretFile(t, cfg.GitlabShell.Dir, "the wrong token")

	customHookOutputPath := gittest.WriteEnvToCustomHook(t, repoPath, "post-receive")

	gitlabClient, err := gitlab.NewHTTPClient(logger, cfg.Gitlab, cfg.TLS, prometheus.Config{})
	require.NoError(t, err)

	runHookServiceWithGitlabClient(t, cfg, true, gitlabClient)

	var stderr, stdout bytes.Buffer

	cmd := exec.Command(gitalyHooksPath)
	cmd.Args = []string{"pre-receive"}
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	cmd.Stdin = strings.NewReader(changes)
	cmd.Env = envForHooks(t, ctx, cfg, repo,
		glHookValues{
			GLID:       glID,
			GLUsername: glUsername,
			GLProtocol: glProtocol,
			RemoteIP:   remoteIP,
		},
		proxyValues{})
	cmd.Dir = repoPath

	require.Error(t, cmd.Run())
	require.Equal(t, "GitLab: 401 Unauthorized\n", stderr.String())
	require.Equal(t, "", stdout.String())
	require.NoFileExists(t, customHookOutputPath)
}

func TestRequestedHooks(t *testing.T) {
	for hook, hookArgs := range map[git.Hook][]string{
		git.ReferenceTransactionHook: {"reference-transaction"},
		git.UpdateHook:               {"update"},
		git.PreReceiveHook:           {"pre-receive"},
		git.PostReceiveHook:          {"post-receive"},
		git.PackObjectsHook:          {"gitaly-hooks", "git"},
	} {
		t.Run(hookArgs[0], func(t *testing.T) {
			t.Run("unrequested hook is ignored", func(t *testing.T) {
				cfg := testcfg.Build(t)
				testcfg.BuildGitalyHooks(t, cfg)
				testcfg.BuildGitalySSH(t, cfg)

				payload, err := git.NewHooksPayload(
					cfg,
					&gitalypb.Repository{},
					gittest.DefaultObjectHash,
					nil,
					nil,
					git.AllHooks&^hook,
					nil,
					0,
				).Env()
				require.NoError(t, err)

				cmd := exec.Command(cfg.BinaryPath("gitaly-hooks"))
				cmd.Args = hookArgs
				cmd.Env = []string{payload}
				require.NoError(t, cmd.Run())
			})

			t.Run("requested hook runs", func(t *testing.T) {
				cfg := testcfg.Build(t)
				testcfg.BuildGitalyHooks(t, cfg)
				testcfg.BuildGitalySSH(t, cfg)

				payload, err := git.NewHooksPayload(
					cfg,
					&gitalypb.Repository{},
					gittest.DefaultObjectHash,
					nil,
					nil,
					hook,
					nil,
					0,
				).Env()
				require.NoError(t, err)

				cmd := exec.Command(cfg.BinaryPath("gitaly-hooks"))
				cmd.Args = hookArgs
				cmd.Env = []string{payload}

				// We simply check that there is an error here as an indicator that
				// the hook logic ran. We don't care for the actual error because we
				// know that in the previous testcase without the hook being
				// requested, there was no error.
				require.Error(t, cmd.Run(), "hook should have run and failed due to incomplete setup")
			})
		})
	}
}

func TestGitalyHooksPackObjects(t *testing.T) {
	logDir := testhelper.TempDir(t)

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{
		Auth:    auth.Config{Token: "abc123"},
		Logging: config.Logging{Config: gitalylog.Config{Dir: logDir}},
	}))

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})

	logger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(logger)
	runHookServiceServer(t, cfg, false, testserver.WithLogger(logger))

	testcfg.BuildGitalyHooks(t, cfg)
	testcfg.BuildGitalySSH(t, cfg)

	baseArgs := []string{
		"clone",
		"-u",
		"git -c uploadpack.allowFilter -c uploadpack.packObjectsHook=" + cfg.BinaryPath("gitaly-hooks") + " upload-pack",
		"--quiet",
		"--no-local",
		"--bare",
	}

	testCases := []struct {
		desc      string
		extraArgs []string
	}{
		{desc: "regular clone"},
		{desc: "shallow clone", extraArgs: []string{"--depth=1"}},
		{desc: "partial clone", extraArgs: []string{"--filter=blob:none"}},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			hook.Reset()

			tempDir := testhelper.TempDir(t)

			args := append(baseArgs, tc.extraArgs...)
			args = append(args, repoPath, tempDir)
			gittest.ExecOpts(t, cfg, gittest.ExecConfig{
				Env: envForHooks(t, ctx, cfg, repo, glHookValues{}, proxyValues{}),
			}, args...)
		})
	}
}

func TestGitalyServerReturnsError(t *testing.T) {
	resourceExhaustedErr := structerr.NewResourceExhausted("%w", limiter.ErrMaxQueueTime).WithDetail(&gitalypb.LimitError{
		ErrorMessage: limiter.ErrMaxQueueTime.Error(),
		RetryAfter:   durationpb.New(0),
	})

	for _, tc := range []struct {
		hook           string
		args           []string
		stdin          string
		expectedStderr string
	}{
		{
			hook:           "pre-receive",
			stdin:          "abc",
			expectedStderr: "error executing git hook\n",
		},
		{
			hook:           "post-receive",
			stdin:          "abc",
			expectedStderr: "error executing git hook\n",
		},
		{
			hook:           "update",
			args:           []string{"ref", "oldValue", "newValue"},
			stdin:          "abc",
			expectedStderr: "error executing git hook\n",
		},
		{
			hook:           "reference-transaction",
			args:           []string{"prepared"},
			stdin:          "abc",
			expectedStderr: "error executing git hook\n",
		},
	} {
		t.Run(tc.hook, func(t *testing.T) {
			logDir := testhelper.TempDir(t)

			ctx := testhelper.Context(t)
			cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{
				Auth:  auth.Config{Token: "abc123"},
				Hooks: config.Hooks{CustomHooksDir: testhelper.TempDir(t)},
			}))

			repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})

			runHookServiceWithMockServer(t, cfg, &hookMockServer{
				preReceiveHook: func(server gitalypb.HookService_PreReceiveHookServer) error {
					return resourceExhaustedErr
				},
				postReceiveHook: func(server gitalypb.HookService_PostReceiveHookServer) error {
					return resourceExhaustedErr
				},
				updateHook: func(request *gitalypb.UpdateHookRequest, server gitalypb.HookService_UpdateHookServer) error {
					return resourceExhaustedErr
				},
				referenceTransactionHook: func(server gitalypb.HookService_ReferenceTransactionHookServer) error {
					return resourceExhaustedErr
				},
			})

			gitalyHooksPath := testcfg.BuildGitalyHooks(t, cfg)
			testcfg.BuildGitalySSH(t, cfg)

			var stderr, stdout bytes.Buffer

			cmd := exec.Command(gitalyHooksPath)
			cmd.Args = append([]string{tc.hook}, tc.args...)
			cmd.Stderr = &stderr
			cmd.Stdout = &stdout
			cmd.Stdin = strings.NewReader(tc.stdin)
			cmd.Env = envForHooks(t, ctx, cfg, repo,
				glHookValues{
					GLID:         glID,
					GLUsername:   glUsername,
					GLProtocol:   "ssh",
					RemoteIP:     remoteIP,
					GitalyLogDir: logDir,
				},
				proxyValues{})
			cmd.Dir = repoPath

			require.Error(t, cmd.Run())
			require.Equal(t, tc.expectedStderr, stderr.String())
			require.Equal(t, "", stdout.String())
			hookLogs := string(testhelper.MustReadFile(t, filepath.Join(logDir, "gitaly_hooks.log")))

			require.NotEmpty(t, hookLogs)
			require.Contains(t, hookLogs, "error executing git hook")
			require.Contains(t, hookLogs, fmt.Sprintf("correlation_id=%s", correlationID))
		})
	}
}

func TestGitalyServerReturnsError_packObjects(t *testing.T) {
	for _, tc := range []struct {
		name           string
		err            error
		expectedStderr string
		expectedLogs   string
	}{
		{
			name: "resource exhausted with LimitError detail",
			err: structerr.NewResourceExhausted("%w", limiter.ErrMaxQueueTime).WithDetail(&gitalypb.LimitError{
				ErrorMessage: limiter.ErrMaxQueueTime.Error(),
				RetryAfter:   durationpb.New(0),
			}),
			expectedStderr: `
remote: error executing git hook
remote: error resource exhausted, please try again later
`,
			expectedLogs: `RPC failed: rpc error: code = ResourceExhausted desc = maximum time in concurrency queue reached`,
		},
		{
			name: "resource exhausted without LimitError detail",
			err:  structerr.NewResourceExhausted("not enough resource"),
			expectedStderr: `
remote: error executing git hook
remote: error resource exhausted, please try again later
`,
			expectedLogs: `RPC failed: rpc error: code = ResourceExhausted desc = not enough resource`,
		},
		{
			name: "other error - status code is hidden",
			//nolint:gitaly-linters
			err: structerr.NewUnavailable("server is not available"),
			expectedStderr: `
remote: error executing git hook
`,
			expectedLogs: `RPC failed: rpc error: code = Unavailable desc = server is not available`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testhelper.SkipQuarantinedTest(t,
				"https://gitlab.com/gitlab-org/gitaly/-/issues/5105",
				"TestGitalyServerReturnsError_packObjects/resource_exhausted_with_LimitError_detail",
				"TestGitalyServerReturnsError_packObjects/resource_exhausted_without_LimitError_detail",
			)

			logDir := testhelper.TempDir(t)

			ctx := testhelper.Context(t)
			cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{
				Auth:  auth.Config{Token: "abc123"},
				Hooks: config.Hooks{CustomHooksDir: testhelper.TempDir(t)},
			}))

			repo, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})
			gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch))

			testcfg.BuildGitalyHooks(t, cfg)
			testcfg.BuildGitalySSH(t, cfg)

			runHookServiceWithMockServer(t, cfg, &hookMockServer{
				packObjectsHookWithSidechannel: func(ctx context.Context, request *gitalypb.PackObjectsHookWithSidechannelRequest) (*gitalypb.PackObjectsHookWithSidechannelResponse, error) {
					return nil, tc.err
				},
			})

			args := []string{
				"clone",
				"-u",
				"git -c uploadpack.allowFilter -c uploadpack.packObjectsHook=" + cfg.BinaryPath("gitaly-hooks") + " upload-pack",
				"--quiet",
				"--no-local",
				"--bare",
				repoPath,
				testhelper.TempDir(t),
			}

			var stderr, stdout bytes.Buffer

			gittest.ExecOpts(t, cfg, gittest.ExecConfig{
				Env:              envForHooks(t, ctx, cfg, repo, glHookValues{GitalyLogDir: logDir}, proxyValues{}),
				Stdout:           &stdout,
				Stderr:           &stderr,
				ExpectedExitCode: 128,
			}, args...)

			require.Equal(t, "", stdout.String())

			var actualStderr []string
			for _, line := range strings.Split(stderr.String(), "\n") {
				actualStderr = append(actualStderr, strings.TrimSpace(line))
			}

			require.Contains(t, strings.Join(actualStderr, "\n"), strings.TrimSpace(tc.expectedStderr))
			hookLogs := string(testhelper.MustReadFile(t, filepath.Join(logDir, "gitaly_hooks.log")))
			require.NotEmpty(t, hookLogs)
			require.Contains(t, hookLogs, tc.expectedLogs)
			require.Contains(t, hookLogs, fmt.Sprintf("correlation_id=%s", correlationID))
		})
	}
}

func runHookServiceServer(t *testing.T, cfg config.Cfg, assertUserDetails bool, serverOpts ...testserver.GitalyServerOpt) {
	t.Helper()

	runHookServiceWithGitlabClient(t, cfg, assertUserDetails, gitlab.NewMockClient(
		t, gitlab.MockAllowed, gitlab.MockPreReceive, gitlab.MockPostReceive,
	), serverOpts...)
}

func runHookServiceWithGitlabClient(t *testing.T, cfg config.Cfg, assertUserDetails bool, gitlabClient gitlab.Client, serverOpts ...testserver.GitalyServerOpt) {
	t.Helper()

	testserver.RunGitalyServer(t, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterHookServiceServer(srv, baggageAsserter{
			t:                 t,
			assertUserDetails: assertUserDetails,
			wrapped:           hook.NewServer(deps),
		})
	}, append(serverOpts, testserver.WithGitLabClient(gitlabClient))...)
}

func runHookServiceWithMockServer(t *testing.T, cfg config.Cfg, mockServer gitalypb.HookServiceServer) {
	t.Helper()

	testserver.RunGitalyServer(t, cfg, func(srv *grpc.Server, deps *service.Dependencies) {
		gitalypb.RegisterHookServiceServer(srv, baggageAsserter{
			t:       t,
			wrapped: mockServer,
		})
	}, testserver.WithDisablePraefect())
}

type hookMockServer struct {
	gitalypb.UnimplementedHookServiceServer
	preReceiveHook                 func(gitalypb.HookService_PreReceiveHookServer) error
	postReceiveHook                func(gitalypb.HookService_PostReceiveHookServer) error
	updateHook                     func(*gitalypb.UpdateHookRequest, gitalypb.HookService_UpdateHookServer) error
	referenceTransactionHook       func(gitalypb.HookService_ReferenceTransactionHookServer) error
	packObjectsHookWithSidechannel func(context.Context, *gitalypb.PackObjectsHookWithSidechannelRequest) (*gitalypb.PackObjectsHookWithSidechannelResponse, error)
}

func (s *hookMockServer) PreReceiveHook(server gitalypb.HookService_PreReceiveHookServer) error {
	return s.preReceiveHook(server)
}

func (s *hookMockServer) PostReceiveHook(server gitalypb.HookService_PostReceiveHookServer) error {
	return s.postReceiveHook(server)
}

func (s *hookMockServer) UpdateHook(request *gitalypb.UpdateHookRequest, server gitalypb.HookService_UpdateHookServer) error {
	return s.updateHook(request, server)
}

func (s *hookMockServer) ReferenceTransactionHook(server gitalypb.HookService_ReferenceTransactionHookServer) error {
	return s.referenceTransactionHook(server)
}

func (s *hookMockServer) PackObjectsHookWithSidechannel(ctx context.Context, request *gitalypb.PackObjectsHookWithSidechannelRequest) (*gitalypb.PackObjectsHookWithSidechannelResponse, error) {
	return s.packObjectsHookWithSidechannel(ctx, request)
}

type baggageAsserter struct {
	gitalypb.UnimplementedHookServiceServer
	t                 testing.TB
	wrapped           gitalypb.HookServiceServer
	assertUserDetails bool
}

func (svc baggageAsserter) assert(ctx context.Context) {
	assert.True(svc.t, enabledFeatureFlag.IsEnabled(ctx))
	assert.True(svc.t, disabledFeatureFlag.IsDisabled(ctx))

	md, ok := grpcmetadata.FromIncomingContext(ctx)
	assert.True(svc.t, ok)
	correlationID := labkitcorrelation.CorrelationIDFromMetadata(md)
	assert.NotEmpty(svc.t, correlationID)

	if svc.assertUserDetails {
		assert.Equal(svc.t, glID, metadata.GetValue(ctx, "user_id"))
		assert.Equal(svc.t, glUsername, metadata.GetValue(ctx, "username"))
		assert.Equal(svc.t, remoteIP, metadata.GetValue(ctx, "remote_ip"))
	}
}

func (svc baggageAsserter) PreReceiveHook(stream gitalypb.HookService_PreReceiveHookServer) error {
	svc.assert(stream.Context())
	return svc.wrapped.PreReceiveHook(stream)
}

func (svc baggageAsserter) PostReceiveHook(stream gitalypb.HookService_PostReceiveHookServer) error {
	svc.assert(stream.Context())
	return svc.wrapped.PostReceiveHook(stream)
}

func (svc baggageAsserter) UpdateHook(request *gitalypb.UpdateHookRequest, stream gitalypb.HookService_UpdateHookServer) error {
	svc.assert(stream.Context())
	return svc.wrapped.UpdateHook(request, stream)
}

func (svc baggageAsserter) ReferenceTransactionHook(stream gitalypb.HookService_ReferenceTransactionHookServer) error {
	svc.assert(stream.Context())
	return svc.wrapped.ReferenceTransactionHook(stream)
}

func (svc baggageAsserter) ProcReceiveHook(stream gitalypb.HookService_ProcReceiveHookServer) error {
	svc.assert(stream.Context())
	return svc.wrapped.ProcReceiveHook(stream)
}

func (svc baggageAsserter) PackObjectsHookWithSidechannel(ctx context.Context, req *gitalypb.PackObjectsHookWithSidechannelRequest) (*gitalypb.PackObjectsHookWithSidechannelResponse, error) {
	svc.assert(ctx)
	return svc.wrapped.PackObjectsHookWithSidechannel(ctx, req)
}

func requireContainsOnce(t *testing.T, s string, contains string) {
	r := regexp.MustCompile(contains)
	matches := r.FindAllStringIndex(s, -1)
	require.Equal(t, 1, len(matches))
}
