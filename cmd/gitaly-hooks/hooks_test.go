package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestMain(m *testing.M) {
	testhelper.Configure()
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	testhelper.ConfigureGitalyHooksBinary()
	testhelper.ConfigureGitalySSH()

	return m.Run()
}

func TestHooksPrePostReceive(t *testing.T) {
	_, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	secretToken := "secret token"
	glID := "key-1234"
	glUsername := "iamgitlab"
	glProtocol := "ssh"
	glRepository := "some_repo"

	tempGitlabShellDir, cleanup := testhelper.CreateTemporaryGitlabShellDir(t)
	defer cleanup()

	changes := "abc"

	gitPushOptions := []string{"gitpushoption1", "gitpushoption2"}
	gitObjectDir := filepath.Join(testRepoPath, "objects", "temp")
	gitAlternateObjectDirs := []string{(filepath.Join(testRepoPath, "objects"))}

	c := testhelper.GitlabTestServerOptions{
		User:                        "",
		Password:                    "",
		SecretToken:                 secretToken,
		GLID:                        glID,
		GLRepository:                glRepository,
		Changes:                     changes,
		PostReceiveCounterDecreased: true,
		Protocol:                    "ssh",
		GitPushOptions:              gitPushOptions,
		GitObjectDir:                gitObjectDir,
		GitAlternateObjectDirs:      gitAlternateObjectDirs,
		RepoPath:                    testRepoPath,
	}

	ts := testhelper.NewGitlabTestServer(t, c)
	defer ts.Close()
	gitlabShellDir := config.Config.GitlabShell.Dir
	defer func() {
		config.Config.GitlabShell.Dir = gitlabShellDir
	}()

	config.Config.GitlabShell.Dir = tempGitlabShellDir

	testhelper.WriteTemporaryGitlabShellConfigFile(t, tempGitlabShellDir, testhelper.GitlabShellConfig{GitlabURL: ts.URL})
	testhelper.WriteShellSecretFile(t, tempGitlabShellDir, secretToken)

	gitObjectDirRegex := regexp.MustCompile(`(?m)^GIT_OBJECT_DIRECTORY=(.*)$`)
	gitAlternateObjectDirRegex := regexp.MustCompile(`(?m)^GIT_ALTERNATE_OBJECT_DIRECTORIES=(.*)$`)

	for _, hook := range []string{"pre-receive", "post-receive"} {
		t.Run(hook, func(t *testing.T) {
			customHookOutputPath, cleanup := testhelper.WriteEnvToCustomHook(t, testRepoPath, hook)
			defer cleanup()

			var stderr, stdout bytes.Buffer
			stdin := bytes.NewBuffer([]byte(changes))
			hookPath, err := filepath.Abs(fmt.Sprintf("../../ruby/git-hooks/%s", hook))
			require.NoError(t, err)
			cmd := exec.Command(hookPath)
			cmd.Stderr = &stderr
			cmd.Stdout = &stdout
			cmd.Stdin = stdin
			cmd.Env = testhelper.EnvForHooks(
				t,
				tempGitlabShellDir,
				testhelper.GlHookValues{
					GLID:                   glID,
					GLUsername:             glUsername,
					GLRepo:                 glRepository,
					GLProtocol:             glProtocol,
					GitObjectDir:           c.GitObjectDir,
					GitAlternateObjectDirs: c.GitAlternateObjectDirs,
				},
				gitPushOptions...,
			)
			cmd.Dir = testRepoPath

			require.NoError(t, cmd.Run())
			require.Empty(t, stderr.String())
			require.Empty(t, stdout.String())

			output := string(testhelper.MustReadFile(t, customHookOutputPath))
			require.Contains(t, output, "GL_USERNAME="+glUsername)
			require.Contains(t, output, "GL_ID="+glID)
			require.Contains(t, output, "GL_REPOSITORY="+glRepository)
			if hook != "pre-receive" {
				require.Contains(t, output, "GL_PROTOCOL="+glProtocol)
			}

			gitObjectDirMatches := gitObjectDirRegex.FindStringSubmatch(output)
			require.Len(t, gitObjectDirMatches, 2)
			require.Equal(t, gitObjectDir, gitObjectDirMatches[1])

			gitAlternateObjectDirMatches := gitAlternateObjectDirRegex.FindStringSubmatch(output)
			require.Len(t, gitAlternateObjectDirMatches, 2)
			require.Equal(t, strings.Join(gitAlternateObjectDirs, ":"), gitAlternateObjectDirMatches[1])
		})
	}
}

func TestHooksUpdate(t *testing.T) {
	glID := "key-1234"
	glUsername := "iamgitlab"
	glProtocol := "ssh"
	glRepository := "some_repo"

	tempGitlabShellDir, cleanup := testhelper.CreateTemporaryGitlabShellDir(t)
	defer cleanup()

	testhelper.WriteTemporaryGitlabShellConfigFile(t, tempGitlabShellDir, testhelper.GitlabShellConfig{GitlabURL: "http://www.example.com"})
	_, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	os.Symlink(filepath.Join(config.Config.GitlabShell.Dir, "config.yml"), filepath.Join(tempGitlabShellDir, "config.yml"))

	testhelper.WriteShellSecretFile(t, tempGitlabShellDir, "the wrong token")

	customHookOutputPath, cleanup := testhelper.WriteEnvToCustomHook(t, testRepoPath, "update")
	defer cleanup()

	gitlabShellDir := config.Config.GitlabShell.Dir
	defer func() {
		config.Config.GitlabShell.Dir = gitlabShellDir
	}()

	config.Config.GitlabShell.Dir = tempGitlabShellDir

	require.NoError(t, os.MkdirAll(filepath.Join(tempGitlabShellDir, "hooks", "update.d"), 0755))
	testhelper.MustRunCommand(t, nil, "cp", "testdata/update", filepath.Join(tempGitlabShellDir, "hooks", "update.d", "update"))
	tempFilePath := filepath.Join(testRepoPath, "tempfile")

	refval, oldval, newval := "refval", "oldval", "newval"
	var stdout, stderr bytes.Buffer

	updateHookPath, err := filepath.Abs("../../ruby/git-hooks/update")
	require.NoError(t, err)
	cmd := exec.Command(updateHookPath, refval, oldval, newval)
	cmd.Env = testhelper.EnvForHooks(t, tempGitlabShellDir, testhelper.GlHookValues{
		GLID:       glID,
		GLUsername: glUsername,
		GLRepo:     glRepository,
		GLProtocol: glProtocol,
	})
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = testRepoPath

	require.NoError(t, cmd.Run())
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())

	require.FileExists(t, tempFilePath)

	var inputs []string

	f, err := os.Open(tempFilePath)
	require.NoError(t, err)
	require.NoError(t, json.NewDecoder(f).Decode(&inputs))
	require.Equal(t, []string{refval, oldval, newval}, inputs)
	require.NoError(t, f.Close())

	output := string(testhelper.MustReadFile(t, customHookOutputPath))
	require.Contains(t, output, "GL_USERNAME="+glUsername)
	require.Contains(t, output, "GL_ID="+glID)
	require.Contains(t, output, "GL_REPOSITORY="+glRepository)
	require.Contains(t, output, "GL_PROTOCOL="+glProtocol)
}

func TestHooksPostReceiveFailed(t *testing.T) {
	secretToken := "secret token"
	glID := "key-1234"
	glUsername := "iamgitlab"
	glProtocol := "ssh"
	glRepository := "some_repo"
	changes := "oldhead newhead"

	tempGitlabShellDir, cleanup := testhelper.CreateTemporaryGitlabShellDir(t)
	defer cleanup()

	_, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	// By setting the last parameter to false, the post-receive API call will
	// send back {"reference_counter_increased": false}, indicating something went wrong
	// with the call

	c := testhelper.GitlabTestServerOptions{
		User:                        "",
		Password:                    "",
		SecretToken:                 secretToken,
		Changes:                     changes,
		GLID:                        glID,
		GLRepository:                glRepository,
		PostReceiveCounterDecreased: false,
		Protocol:                    "ssh",
	}
	ts := testhelper.NewGitlabTestServer(t, c)
	defer ts.Close()

	testhelper.WriteTemporaryGitlabShellConfigFile(t, tempGitlabShellDir, testhelper.GitlabShellConfig{GitlabURL: ts.URL})
	testhelper.WriteShellSecretFile(t, tempGitlabShellDir, secretToken)

	gitlabShellDir := config.Config.GitlabShell.Dir
	defer func() {
		config.Config.GitlabShell.Dir = gitlabShellDir
	}()

	config.Config.GitlabShell.Dir = tempGitlabShellDir

	customHookOutputPath, cleanup := testhelper.WriteEnvToCustomHook(t, testRepoPath, "post-receive")
	defer cleanup()

	var stdout, stderr bytes.Buffer

	postReceiveHookPath, err := filepath.Abs("../../ruby/git-hooks/post-receive")
	require.NoError(t, err)
	cmd := exec.Command(postReceiveHookPath)
	cmd.Env = testhelper.EnvForHooks(t, tempGitlabShellDir, testhelper.GlHookValues{
		GLID:       glID,
		GLUsername: glUsername,
		GLRepo:     glRepository,
		GLProtocol: glProtocol,
	})
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = bytes.NewBuffer([]byte(changes))
	cmd.Dir = testRepoPath

	err = cmd.Run()
	code, ok := command.ExitStatus(err)

	require.True(t, ok, "expect exit status in %v", err)
	require.Equal(t, 1, code, "exit status")
	require.Empty(t, stdout.String())
	require.Empty(t, stderr.String())

	output := string(testhelper.MustReadFile(t, customHookOutputPath))
	require.Empty(t, output, "custom hook should not have run")
}

func TestHooksNotAllowed(t *testing.T) {
	secretToken := "secret token"
	glID := "key-1234"
	glUsername := "iamgitlab"
	glProtocol := "ssh"
	glRepository := "some_repo"
	changes := "oldhead newhead"

	tempGitlabShellDir, cleanup := testhelper.CreateTemporaryGitlabShellDir(t)
	defer cleanup()

	c := testhelper.GitlabTestServerOptions{
		User:                        "",
		Password:                    "",
		SecretToken:                 secretToken,
		GLID:                        glID,
		GLRepository:                glRepository,
		Changes:                     changes,
		PostReceiveCounterDecreased: true,
		Protocol:                    "ssh",
	}
	ts := testhelper.NewGitlabTestServer(t, c)
	defer ts.Close()
	_, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	testhelper.WriteTemporaryGitlabShellConfigFile(t, tempGitlabShellDir, testhelper.GitlabShellConfig{GitlabURL: ts.URL})
	testhelper.WriteShellSecretFile(t, tempGitlabShellDir, "the wrong token")

	gitlabShellDir := config.Config.GitlabShell.Dir
	defer func() {
		config.Config.GitlabShell.Dir = gitlabShellDir
	}()

	customHookOutputPath, cleanup := testhelper.WriteEnvToCustomHook(t, testRepoPath, "post-receive")
	defer cleanup()

	config.Config.GitlabShell.Dir = tempGitlabShellDir

	var stderr, stdout bytes.Buffer

	preReceiveHookPath, err := filepath.Abs("../../ruby/git-hooks/pre-receive")
	require.NoError(t, err)
	cmd := exec.Command(preReceiveHookPath)
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout
	cmd.Stdin = strings.NewReader(changes)
	cmd.Env = testhelper.EnvForHooks(t, tempGitlabShellDir, testhelper.GlHookValues{
		GLID:       glID,
		GLUsername: glUsername,
		GLRepo:     glRepository,
		GLProtocol: glProtocol,
	})
	cmd.Dir = testRepoPath

	require.Error(t, cmd.Run())
	require.Equal(t, "GitLab: 401 Unauthorized\n", stderr.String())
	require.Equal(t, "", stdout.String())

	output := string(testhelper.MustReadFile(t, customHookOutputPath))
	require.Empty(t, output, "custom hook should not have run")
}

func TestCheckOK(t *testing.T) {
	user, password := "user123", "password321"

	c := testhelper.GitlabTestServerOptions{
		User:                        user,
		Password:                    password,
		SecretToken:                 "",
		GLRepository:                "",
		Changes:                     "",
		PostReceiveCounterDecreased: false,
		Protocol:                    "ssh",
	}
	ts := testhelper.NewGitlabTestServer(t, c)
	defer ts.Close()

	tempDir, err := ioutil.TempDir("", t.Name())
	require.NoError(t, err)
	defer func() {
		os.RemoveAll(tempDir)
	}()

	gitlabShellDir := filepath.Join(tempDir, "gitlab-shell")
	binDir := filepath.Join(gitlabShellDir, "bin")
	require.NoError(t, os.MkdirAll(gitlabShellDir, 0755))
	require.NoError(t, os.MkdirAll(binDir, 0755))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(filepath.Join(cwd, "../../ruby/gitlab-shell/bin/check"), filepath.Join(binDir, "check")))

	testhelper.WriteShellSecretFile(t, gitlabShellDir, "the secret")
	testhelper.WriteTemporaryGitlabShellConfigFile(t, gitlabShellDir, testhelper.GitlabShellConfig{GitlabURL: ts.URL, HTTPSettings: testhelper.HTTPSettings{User: user, Password: password}})

	configPath, cleanup := testhelper.WriteTemporaryGitalyConfigFile(t, tempDir)
	defer cleanup()

	cmd := exec.Command(fmt.Sprintf("%s/gitaly-hooks", config.Config.BinDir), "check", configPath)

	var stderr, stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	require.NoError(t, cmd.Run())
	require.Empty(t, stderr.String())
	expectedCheckOutput := "Check GitLab API access: OK\nRedis available via internal API: OK\n"
	require.Equal(t, expectedCheckOutput, stdout.String())
}

func TestCheckBadCreds(t *testing.T) {
	user, password := "user123", "password321"

	c := testhelper.GitlabTestServerOptions{
		User:                        user,
		Password:                    password,
		SecretToken:                 "",
		GLRepository:                "",
		Changes:                     "",
		PostReceiveCounterDecreased: false,
		Protocol:                    "ssh",
		GitPushOptions:              nil,
	}
	ts := testhelper.NewGitlabTestServer(t, c)
	defer ts.Close()

	tempDir, err := ioutil.TempDir("", t.Name())
	require.NoError(t, err)
	defer func() {
		os.RemoveAll(tempDir)
	}()

	gitlabShellDir := filepath.Join(tempDir, "gitlab-shell")
	binDir := filepath.Join(gitlabShellDir, "bin")
	require.NoError(t, os.MkdirAll(gitlabShellDir, 0755))
	require.NoError(t, os.MkdirAll(binDir, 0755))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Symlink(filepath.Join(cwd, "../../ruby/gitlab-shell/bin/check"), filepath.Join(binDir, "check")))

	testhelper.WriteTemporaryGitlabShellConfigFile(t, gitlabShellDir, testhelper.GitlabShellConfig{GitlabURL: ts.URL, HTTPSettings: testhelper.HTTPSettings{User: user + "wrong", Password: password}})
	testhelper.WriteShellSecretFile(t, gitlabShellDir, "the secret")

	configPath, cleanup := testhelper.WriteTemporaryGitalyConfigFile(t, tempDir)
	defer cleanup()

	cmd := exec.Command(fmt.Sprintf("%s/gitaly-hooks", config.Config.BinDir), "check", configPath)

	var stderr, stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	require.Error(t, cmd.Run())
	require.Equal(t, "Check GitLab API access: ", stdout.String())
	require.Equal(t, "FAILED. code: 401\n", stderr.String())
}
