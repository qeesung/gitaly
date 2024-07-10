package gittest

import (
	"context"
	"net"
	"net/http"
	"net/http/cgi"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

// HTTPServer starts an HTTP server with git-http-backend(1) as CGI handler. The repository is
// prepared such that git-http-backend(1) will serve it by creating the "git-daemon-export-ok" magic
// file.
func HTTPServer(tb testing.TB, ctx context.Context, gitCmdFactory git.CommandFactory, repoPath string, middleware func(http.ResponseWriter, *http.Request, http.Handler)) int {
	require.NoError(tb, os.WriteFile(filepath.Join(repoPath, "git-daemon-export-ok"), nil, perm.PrivateWriteOnceFile))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(tb, err)

	gitExecEnv := gitCmdFactory.GetExecutionEnvironment(ctx)

	gitHTTPBackend := &cgi.Handler{
		Path: gitExecEnv.BinaryPath,
		Dir:  "/",
		Args: []string{"http-backend"},
		Env: append([]string{
			"GIT_PROJECT_ROOT=" + filepath.Dir(repoPath),
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.receivepack",
			"GIT_CONFIG_VALUE_0=true",
		}, gitExecEnv.EnvironmentVariables...),
	}

	s := &http.Server{Handler: gitHTTPBackend}
	tb.Cleanup(func() {
		testhelper.MustClose(tb, s)
	})

	if middleware != nil {
		s.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			middleware(w, r, gitHTTPBackend)
		})
	}

	go testhelper.MustServe(tb, s, listener)

	return listener.Addr().(*net.TCPAddr).Port
}
