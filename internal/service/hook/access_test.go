package hook_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/service/hook"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestAllowedVerifyParams(t *testing.T) {
	user, password := "user", "password"
	secretToken := "topsecret"
	glID, glRepository := "key-123", "repo-1"

	testRepo, testRepoPath, cleanup := testhelper.NewTestRepo(t)
	defer cleanup()
	changes := "changes1\nchanges2\nchanges3"
	protocol := "protocol"

	testRepo.GitObjectDirectory = "object/dir"
	testRepo.GitAlternateObjectDirectories = []string{"alt/object/dir1", "alt/object/dir2"}

	gitObjectDirFull := filepath.Join(testRepoPath, testRepo.GitObjectDirectory)
	var gitAlternateObjectDirsFull []string

	for _, gitAlternateObjectDirRel := range testRepo.GitAlternateObjectDirectories {
		gitAlternateObjectDirsFull = append(gitAlternateObjectDirsFull, filepath.Join(testRepoPath, gitAlternateObjectDirRel))
	}

	tempDir, cleanup := testhelper.TempDir(t)
	defer cleanup()
	testhelper.WriteShellSecretFile(t, tempDir, secretToken)

	secretFilePath := filepath.Join(tempDir, ".gitlab_shell_secret")

	server := testhelper.NewGitlabTestServer(testhelper.GitlabTestServerOptions{
		User:                        user,
		Password:                    password,
		SecretToken:                 secretToken,
		GLID:                        glID,
		GLRepository:                glRepository,
		Changes:                     changes,
		PostReceiveCounterDecreased: true,
		Protocol:                    protocol,
		GitPushOptions:              nil,
		GitObjectDir:                gitObjectDirFull,
		GitAlternateObjectDirs:      gitAlternateObjectDirsFull,
		RepoPath:                    testRepoPath,
	})

	defer server.Close()

	c, err := hook.NewGitlabAPI(config.Gitlab{
		URL:        server.URL,
		SecretFile: secretFilePath,
		HTTPSettings: config.HTTPSettings{
			User:     user,
			Password: password,
		},
	})
	require.NoError(t, err)

	badRepo := *testRepo
	badRepo.GitObjectDirectory = filepath.Join(testRepoPath, "bad/object/directory")

	testCases := []struct {
		desc                                  string
		repo                                  *gitalypb.Repository
		glRepository, glID, protocol, changes string
		allowed                               bool
	}{
		{
			desc:         "success",
			repo:         testRepo,
			glRepository: glRepository,
			glID:         glID,
			protocol:     protocol,
			changes:      changes,
			allowed:      true,
		},
		{
			desc:         "repo with bad quarantine directories",
			repo:         &badRepo,
			glRepository: glRepository,
			glID:         glID,
			protocol:     protocol,
			changes:      changes,
			allowed:      false,
		},
	}

	for _, tc := range testCases {
		allowed, err := c.Allowed(tc.repo, tc.glRepository, tc.glID, tc.protocol, tc.changes)
		require.NoError(t, err)
		require.Equal(t, tc.allowed, allowed)
	}
}

func TestAllowedResponseHandling(t *testing.T) {
	testRepo, testRepoPath, cleanup := testhelper.NewTestRepo(t)

	// set git quarantine directories
	gitObjectDir := filepath.Join(testRepoPath, "quarantine", "object", "dir")
	testRepo.GitObjectDirectory = gitObjectDir
	gitAltObjectDir := filepath.Join(testRepoPath, "objects")
	testRepo.GitAlternateObjectDirectories = []string{gitAltObjectDir}

	defer cleanup()

	tempDir, cleanup := testhelper.TempDir(t)
	defer cleanup()
	testhelper.WriteShellSecretFile(t, tempDir, "secret_token")

	secretFilePath := filepath.Join(tempDir, ".gitlab_shell_secret")

	testCases := []struct {
		desc           string
		allowedHandler func(w http.ResponseWriter, r *http.Request)
		allowed        bool
		errMsg         string
	}{
		{
			desc: "allowed",
			allowedHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"status": true}`))
			},
			allowed: true,
			errMsg:  "",
		},
		{
			desc: "bad content type in response",
			allowedHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "bad mime type")
				w.WriteHeader(http.StatusOK)
			},
			allowed: false,
			errMsg:  "unsupported content type",
		},
		{
			desc: "internal server error",
			allowedHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"status": true}`))
			},
			allowed: false,
			errMsg:  "",
		},
		{
			desc: "bad response",
			allowedHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`this is not json`))
			},
			allowed: false,
			errMsg:  "decoding response from /allowed endpoint",
		},
		{
			desc: "status multiple choice",
			allowedHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusMultipleChoices)
				w.Write([]byte(`{"status": true}`))
			},
			allowed: true,
			errMsg:  "",
		},
		{
			desc: "status unauthorized with message",
			allowedHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"message": "you're not allowed here'"}`))
			},
			allowed: false,
			errMsg:  "you're not allowed here",
		},
		{
			desc: "status unauthorized",
			allowedHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
			},
			allowed: false,
			errMsg:  "Internal API error",
		},
		{
			desc: "status not found",
			allowedHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				w.Write([]byte(`{"message": "not found"}`))
			},
			allowed: false,
			errMsg:  "not found",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tc.allowedHandler))
			defer server.Close()

			c, err := hook.NewGitlabAPI(config.Gitlab{
				URL:        server.URL,
				SecretFile: secretFilePath,
			})
			require.NoError(t, err)

			allowed, err := c.Allowed(testRepo, "repo-1", "key-123", "http", "a\nb\nc\nd")
			require.Equal(t, tc.allowed, allowed)
			if err != nil {
				require.Contains(t, err.Error(), tc.errMsg)
			}
		})
	}
}

func TestPrereceive(t *testing.T) {
	tempDir, cleanup := testhelper.TempDir(t)
	defer cleanup()

	testhelper.WriteShellSecretFile(t, tempDir, "secret_token")

	secretFilePath := filepath.Join(tempDir, ".gitlab_shell_secret")

	testCases := []struct {
		desc              string
		prereceiveHandler func(w http.ResponseWriter, r *http.Request)
		success           bool
		errMsg            string
	}{
		{
			desc: "everything ok",
			prereceiveHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"reference_counter_increased": true}`))
			},
			success: true,
			errMsg:  "",
		},
		{
			desc: "reference counter not increased",
			prereceiveHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"reference_counter_increased": false}`))
			},
			success: false,
			errMsg:  "",
		},
		{
			desc: "server unavailable",
			prereceiveHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				w.Write([]byte(`{"message": "server is down!"}`))
			},
			success: false,
			errMsg:  "server is down!",
		},
		{
			desc: "non json content type",
			prereceiveHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`{"reference_counter_increased": true}`))
			},
			success: false,
			errMsg:  "unsupported content type",
		},
		{
			desc: "bad data",
			prereceiveHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(`not json`))
			},
			success: false,
			errMsg:  "decoding response from /pre_receive endpoint",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tc.prereceiveHandler))
			defer server.Close()

			c, err := hook.NewGitlabAPI(config.Gitlab{
				URL:        server.URL,
				SecretFile: secretFilePath,
			})
			require.NoError(t, err)

			success, err := c.PreReceive("key-123")
			require.Equal(t, tc.success, success)
			if err != nil {
				require.Contains(t, err.Error(), tc.errMsg)
			}
		})
	}
}
