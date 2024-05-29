package gitlab

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

var changeLineRegex = regexp.MustCompile("^[a-f0-9]{40} [a-f0-9]{40} refs/[^ ]+$")

// WriteShellSecretFile writes a .gitlab_shell_secret file in the specified directory
func WriteShellSecretFile(tb testing.TB, dir, secretToken string) string {
	tb.Helper()

	require.NoError(tb, os.MkdirAll(dir, perm.PublicDir))
	filePath := filepath.Join(dir, ".gitlab_shell_secret")
	require.NoError(tb, os.WriteFile(filePath, []byte(secretToken), perm.SharedFile))
	return filePath
}

// TestServerOptions is a config for a mock gitlab server containing expected values
type TestServerOptions struct {
	User, Password, SecretToken string
	GLID                        string
	GLRepository                string
	Changes                     string
	PostReceiveMessages         []string
	PostReceiveAlerts           []string
	PostReceiveCounterDecreased bool
	UnixSocket                  bool
	LfsStatusCode               int
	LfsOid                      string
	LfsBody                     string
	Protocol                    string
	GitPushOptions              []string
	GitObjectDir                string
	GitAlternateObjectDirs      []string
	RepoPath                    string
	RelativeURLRoot             string
	GlRepository                string
	ClientCertificate           *testhelper.Certificate
	ServerCertificate           *testhelper.Certificate
}

// NewTestServer returns a mock gitlab server that responds to the hook api endpoints
func NewTestServer(tb testing.TB, options TestServerOptions) (url string, cleanup func()) {
	tb.Helper()

	mux := http.NewServeMux()
	prefix := strings.TrimRight(options.RelativeURLRoot, "/") + "/api/v4/internal"
	mux.Handle(prefix+"/allowed", http.HandlerFunc(handleAllowed(tb, options)))
	mux.Handle(prefix+"/pre_receive", http.HandlerFunc(handlePreReceive(tb, options)))
	mux.Handle(prefix+"/post_receive", http.HandlerFunc(handlePostReceive(options)))
	mux.Handle(prefix+"/check", http.HandlerFunc(handleCheck(tb, options)))
	mux.Handle(prefix+"/lfs", http.HandlerFunc(handleLfs(tb, options)))

	var tlsCfg *tls.Config
	if options.ClientCertificate != nil {
		require.NotNil(tb, options.ServerCertificate)

		clientCertPool := options.ClientCertificate.CertPool(tb)
		serverCert := options.ServerCertificate.Cert(tb)

		tlsCfg = &tls.Config{
			ClientCAs:    clientCertPool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			Certificates: []tls.Certificate{serverCert},
			MinVersion:   tls.VersionTLS12,
		}
	}

	if options.UnixSocket {
		return startSocketHTTPServer(tb, mux, tlsCfg)
	}

	var server *httptest.Server
	if tlsCfg == nil {
		server = httptest.NewServer(mux)
	} else {
		server = httptest.NewUnstartedServer(mux)
		server.TLS = tlsCfg
		server.StartTLS()
	}
	return server.URL, server.Close
}

type preReceiveForm struct {
	Action       string   `json:"action,omitempty"`
	GLRepository string   `json:"gl_repository,omitempty"`
	Project      string   `json:"project,omitempty"`
	Changes      string   `json:"changes,omitempty"`
	Protocol     string   `json:"protocol,omitempty"`
	RelativePath string   `json:"relative_path,omitempty"`
	Env          string   `json:"env,omitempty"`
	Username     string   `json:"username,omitempty"`
	KeyID        string   `json:"key_id,omitempty"`
	UserID       string   `json:"user_id,omitempty"`
	PushOptions  []string `json:"push_options,omitempty"`
}

func parsePreReceiveForm(u url.Values) preReceiveForm {
	return preReceiveForm{
		Action:       u.Get("action"),
		GLRepository: u.Get("gl_repository"),
		Project:      u.Get("project"),
		Changes:      u.Get("changes"),
		Protocol:     u.Get("protocol"),
		RelativePath: u.Get("relative_path"),
		Env:          u.Get("env"),
		Username:     u.Get("username"),
		KeyID:        u.Get("key_id"),
		UserID:       u.Get("user_id"),
		PushOptions:  u["push_options[]"],
	}
}

type postReceiveForm struct {
	GLRepository   string   `json:"gl_repository"`
	SecretToken    string   `json:"secret_token"`
	Changes        string   `json:"changes"`
	Identifier     string   `json:"identifier"`
	GitPushOptions []string `json:"push_options"`
}

func parsePostReceiveForm(u url.Values) postReceiveForm {
	return postReceiveForm{
		GLRepository:   u.Get("gl_repository"),
		SecretToken:    u.Get("secret_token"),
		Changes:        u.Get("changes"),
		Identifier:     u.Get("identifier"),
		GitPushOptions: u["push_options[]"],
	}
}

func handleAllowed(tb testing.TB, options TestServerOptions) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "could not parse form", http.StatusBadRequest)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "method not POST", http.StatusMethodNotAllowed)
			return
		}

		var params preReceiveForm

		switch r.Header.Get("Content-Type") {
		case "application/x-www-form-urlencoded":
			params = parsePreReceiveForm(r.Form)
		case "application/json":
			if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
				http.Error(w, "could not unmarshal json body", http.StatusBadRequest)
				return
			}
		}

		user, password, _ := r.BasicAuth()
		if user != options.User || password != options.Password {
			http.Error(w, "user or password incorrect", http.StatusUnauthorized)
			return
		}

		if options.GLID != "" {
			glidSplit := strings.SplitN(options.GLID, "-", 2)
			if len(glidSplit) != 2 {
				http.Error(w, "gl_id invalid", http.StatusUnauthorized)
				return
			}

			glKey, glVal := glidSplit[0], glidSplit[1]

			var glIDMatches bool
			switch glKey {
			case "user":
				glIDMatches = glVal == params.UserID
			case "key":
				glIDMatches = glVal == params.KeyID
			case "username":
				glIDMatches = glVal == params.Username
			default:
				http.Error(w, "gl_id invalid", http.StatusUnauthorized)
				return
			}

			if !glIDMatches {
				http.Error(w, "gl_id invalid", http.StatusUnauthorized)
				return
			}
		}

		if params.GLRepository == "" {
			http.Error(w, "gl_repository is empty", http.StatusUnauthorized)
			return
		}

		if options.GLRepository != "" {
			if params.GLRepository != options.GLRepository {
				http.Error(w, "gl_repository is invalid", http.StatusUnauthorized)
				return
			}
		}

		if params.Protocol == "" {
			http.Error(w, "protocol is empty", http.StatusUnauthorized)
			return
		}

		if options.Protocol != "" {
			if params.Protocol != options.Protocol {
				http.Error(w, "protocol is invalid", http.StatusUnauthorized)
				return
			}
		}

		if options.Changes != "" {
			if params.Changes != options.Changes {
				http.Error(w, "changes is invalid", http.StatusUnauthorized)
				return
			}
		} else {
			changeLines := strings.Split(strings.TrimSuffix(params.Changes, "\n"), "\n")
			for _, line := range changeLines {
				if !changeLineRegex.MatchString(line) {
					http.Error(w, "changes is invalid", http.StatusUnauthorized)
					return
				}
			}
		}

		env := params.Env
		if len(env) == 0 {
			http.Error(w, "env is empty", http.StatusUnauthorized)
			return
		}

		var gitVars struct {
			GitAlternateObjectDirsRel []string `json:"GIT_ALTERNATE_OBJECT_DIRECTORIES_RELATIVE"`
			GitObjectDirRel           string   `json:"GIT_OBJECT_DIRECTORY_RELATIVE"`
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.Unmarshal([]byte(env), &gitVars); err != nil {
			http.Error(w, "could not unmarshal env", http.StatusUnauthorized)
			return
		}

		if options.GitObjectDir != "" {
			relObjectDir, err := filepath.Rel(options.RepoPath, options.GitObjectDir)
			if err != nil {
				http.Error(w, "git object dirs is invalid", http.StatusUnauthorized)
				return
			}
			if relObjectDir != gitVars.GitObjectDirRel {
				_, err := w.Write([]byte(`{"status":false}`))
				require.NoError(tb, err)
				return
			}
		}

		if len(options.GitAlternateObjectDirs) > 0 {
			if len(gitVars.GitAlternateObjectDirsRel) != len(options.GitAlternateObjectDirs) {
				http.Error(w, "git alternate object dirs is invalid", http.StatusUnauthorized)
				return
			}

			for i, gitAlterateObjectDir := range options.GitAlternateObjectDirs {
				relAltObjectDir, err := filepath.Rel(options.RepoPath, gitAlterateObjectDir)
				if err != nil {
					http.Error(w, "git alternate object dirs is invalid", http.StatusUnauthorized)
					return
				}

				if relAltObjectDir != gitVars.GitAlternateObjectDirsRel[i] {
					_, err := w.Write([]byte(`{"status":false}`))
					require.NoError(tb, err)
					return
				}
			}
		}

		if verifyJWT(r.Header.Get("Gitlab-Shell-Api-Request"), options.SecretToken) {
			_, err := w.Write([]byte(`{"status":true}`))
			require.NoError(tb, err)
			return
		}

		w.WriteHeader(http.StatusUnauthorized)
		_, err := w.Write([]byte(`{"message":"401 Unauthorized\n"}`))
		require.NoError(tb, err)
	}
}

func handlePreReceive(tb testing.TB, options TestServerOptions) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "could not parse form", http.StatusBadRequest)
			return
		}

		params := make(map[string]string)

		switch r.Header.Get("Content-Type") {
		case "application/x-www-form-urlencoded":
			b, err := json.Marshal(r.Form)
			if err != nil {
				http.Error(w, "could not marshal form", http.StatusBadRequest)
				return
			}

			var reqForm struct {
				GLRepository []string `json:"gl_repository"`
			}

			if err = json.Unmarshal(b, &reqForm); err != nil {
				http.Error(w, "could not unmarshal form", http.StatusBadRequest)
				return
			}

			if len(reqForm.GLRepository) == 0 {
				http.Error(w, "gl_repository is missing", http.StatusBadRequest)
				return
			}

			params["gl_repository"] = reqForm.GLRepository[0]
		case "application/json":
			if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
				http.Error(w, "error when unmarshalling json body", http.StatusBadRequest)
				return
			}
		}

		if r.Method != http.MethodPost {
			http.Error(w, "method not POST", http.StatusMethodNotAllowed)
			return
		}

		if params["gl_repository"] == "" {
			http.Error(w, "gl_repository is empty", http.StatusUnauthorized)
			return
		}

		if options.GLRepository != "" {
			if params["gl_repository"] != options.GLRepository {
				http.Error(w, "gl_repository is invalid", http.StatusUnauthorized)
				return
			}
		}

		if !verifyJWT(r.Header.Get("Gitlab-Shell-Api-Request"), options.SecretToken) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		_, err := w.Write([]byte(`{"reference_counter_increased": true}`))
		require.NoError(tb, err)
	}
}

func handlePostReceive(options TestServerOptions) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "couldn't parse form", http.StatusBadRequest)
			return
		}

		if r.Method != http.MethodPost {
			http.Error(w, "method not POST", http.StatusMethodNotAllowed)
			return
		}

		var params postReceiveForm

		switch r.Header.Get("Content-Type") {
		case "application/x-www-form-urlencoded":
			params = parsePostReceiveForm(r.Form)
		case "application/json":
			if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
				http.Error(w, "could not parse json body", http.StatusBadRequest)
				return
			}
		}

		if params.GLRepository == "" {
			http.Error(w, "gl_repository is empty", http.StatusUnauthorized)
			return
		}

		if options.GLRepository != "" {
			if params.GLRepository != options.GLRepository {
				http.Error(w, "gl_repository is invalid", http.StatusUnauthorized)
				return
			}
		}

		if !verifyJWT(r.Header.Get("Gitlab-Shell-Api-Request"), options.SecretToken) {
			http.Error(w, "jwt header is invalid", http.StatusUnauthorized)
			return
		}

		if params.Identifier == "" {
			http.Error(w, "identifier is empty", http.StatusUnauthorized)
			return
		}

		if options.GLRepository != "" {
			if params.Identifier != options.GLID {
				http.Error(w, "identifier is invalid", http.StatusUnauthorized)
				return
			}
		}

		if params.Changes == "" {
			http.Error(w, "changes is empty", http.StatusUnauthorized)
			return
		}

		if options.Changes != "" {
			if params.Changes != options.Changes {
				http.Error(w, "changes is invalid", http.StatusUnauthorized)
				return
			}
		}

		if len(options.GitPushOptions) != len(params.GitPushOptions) {
			http.Error(w, "invalid push options", http.StatusUnauthorized)
			return
		}

		for i, opt := range options.GitPushOptions {
			if opt != params.GitPushOptions[i] {
				http.Error(w, "invalid push options", http.StatusUnauthorized)
				return
			}
		}

		response := postReceiveResponse{
			ReferenceCounterDecreased: options.PostReceiveCounterDecreased,
		}

		for _, basicMessage := range options.PostReceiveMessages {
			response.Messages = append(response.Messages, PostReceiveMessage{
				Message: basicMessage,
				Type:    "basic",
			})
		}

		for _, alertMessage := range options.PostReceiveAlerts {
			response.Messages = append(response.Messages, PostReceiveMessage{
				Message: alertMessage,
				Type:    "alert",
			})
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(&response); err != nil {
			panic(err)
		}
	}
}

func handleCheck(tb testing.TB, options TestServerOptions) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != options.User || p != options.Password {
			w.WriteHeader(http.StatusUnauthorized)
			require.NoError(tb, json.NewEncoder(w).Encode(struct {
				Message string `json:"message"`
			}{Message: "authorization failed"}))
			return
		}

		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"redis": true}`)
	}
}

func handleLfs(tb testing.TB, options TestServerOptions) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "couldn't parse form", http.StatusBadRequest)
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, "method not GET", http.StatusMethodNotAllowed)
			return
		}

		if options.LfsOid != "" {
			if r.FormValue("oid") != options.LfsOid {
				http.Error(w, "oid parameter does not match", http.StatusBadRequest)
				return
			}
		}

		if options.GlRepository != "" {
			if r.FormValue("gl_repository") != options.GlRepository {
				http.Error(w, "gl_repository parameter does not match", http.StatusBadRequest)
				return
			}
		}

		w.Header().Set("Content-Type", "application/octet-stream")

		if options.LfsStatusCode != 0 {
			w.WriteHeader(options.LfsStatusCode)
		}

		if options.LfsBody != "" {
			_, err := w.Write([]byte(options.LfsBody))
			require.NoError(tb, err)
		}
	}
}

func verifyJWT(header, secretToken string) bool {
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(header, claims, func(token *jwt.Token) (interface{}, error) {
		return []byte(secretToken), nil
	})

	return err == nil && token.Valid && claims.Issuer == "gitlab-shell"
}

func startSocketHTTPServer(tb testing.TB, mux *http.ServeMux, tlsCfg *tls.Config) (string, func()) {
	tempDir := testhelper.TempDir(tb)

	filename := filepath.Join(tempDir, "http-test-server")
	socketListener, err := net.Listen("unix", filename)
	require.NoError(tb, err)

	server := http.Server{
		Handler:   mux,
		TLSConfig: tlsCfg,
	}

	go testhelper.MustServe(tb, &server, socketListener)

	url := "http+unix://" + filename
	cleanup := func() {
		require.NoError(tb, server.Close())
	}

	return url, cleanup
}

// SetupAndStartGitlabServer creates a new GitlabTestServer, starts it and sets
// up the gitlab-shell secret.
func SetupAndStartGitlabServer(tb testing.TB, shellDir string, c *TestServerOptions) (string, func()) {
	url, cleanup := NewTestServer(tb, *c)

	WriteShellSecretFile(tb, shellDir, c.SecretToken)

	return url, cleanup
}
