package gittest

import (
	"compress/gzip"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
)

// RemoteUploadPackServer implements two HTTP routes for git-upload-pack by copying stdin and stdout into and out of the git upload-pack command
func RemoteUploadPackServer(ctx context.Context, t *testing.T, cfg config.Cfg, repoName, httpToken, repoPath string) (*httptest.Server, string) {
	s := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.String() {
			case fmt.Sprintf("/%s.git/git-upload-pack", repoName):
				w.WriteHeader(http.StatusOK)

				var err error
				reader := r.Body

				if r.Header.Get("Content-Encoding") == "gzip" {
					reader, err = gzip.NewReader(r.Body)
					require.NoError(t, err)
				}
				defer r.Body.Close()

				ExecOpts(t, cfg, ExecConfig{Stdin: reader, Stdout: w},
					"-C", repoPath, "upload-pack", "--stateless-rpc", ".",
				)
			case fmt.Sprintf("/%s.git/info/refs?service=git-upload-pack", repoName):
				if httpToken != "" && r.Header.Get("Authorization") != httpToken {
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
				w.WriteHeader(http.StatusOK)

				_, err := w.Write([]byte("001e# service=git-upload-pack\n"))
				require.NoError(t, err)
				_, err = w.Write([]byte("0000"))
				require.NoError(t, err)

				ExecOpts(t, cfg, ExecConfig{Stdout: w},
					"-C", repoPath, "upload-pack", "--advertise-refs", ".",
				)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}),
	)

	return s, fmt.Sprintf("%s/%s.git", s.URL, repoName)
}

// GitServer starts an HTTP server with git-http-backend(1) as CGI handler. The
// repository is prepared such that git-http-backend(1) will serve it by
// creating the "git-daemon-export-ok" magic file.
func GitServer(t testing.TB, cfg config.Cfg, repoPath string, middleware func(http.ResponseWriter, *http.Request, http.Handler)) (int, func() error) {
	require.NoError(t, os.WriteFile(filepath.Join(repoPath, "git-daemon-export-ok"), nil, 0o644))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	gitHTTPBackend := &cgi.Handler{
		Path: cfg.Git.BinPath,
		Dir:  "/",
		Args: []string{"http-backend"},
		Env: []string{
			"GIT_PROJECT_ROOT=" + filepath.Dir(repoPath),
			"GIT_CONFIG_COUNT=1",
			"GIT_CONFIG_KEY_0=http.receivepack",
			"GIT_CONFIG_VALUE_0=true",
		},
	}
	s := http.Server{Handler: gitHTTPBackend}

	if middleware != nil {
		s.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			middleware(w, r, gitHTTPBackend)
		})
	}

	go s.Serve(listener)

	return listener.Addr().(*net.TCPAddr).Port, s.Close
}
