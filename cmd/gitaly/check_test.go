package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func TestCheckOK(t *testing.T) {
	user, password := "user123", "password321"

	c := gitlab.TestServerOptions{
		User:                        user,
		Password:                    password,
		SecretToken:                 "",
		GLRepository:                "",
		Changes:                     "",
		PostReceiveCounterDecreased: false,
		Protocol:                    "ssh",
	}
	serverURL, cleanup := gitlab.NewTestServer(t, c)
	defer cleanup()

	cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{
		Gitlab: config.Gitlab{
			URL: serverURL,
			HTTPSettings: config.HTTPSettings{
				User:     user,
				Password: password,
			},
			SecretFile: gitlab.WriteShellSecretFile(t, testhelper.TempDir(t), "the secret"),
		},
	}))
	testcfg.BuildGitaly(t, cfg)

	configPath := writeTemporaryGitalyConfigFile(t, cfg)

	cmd := exec.Command(cfg.BinaryPath("gitaly"), "check", configPath)

	var stderr, stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	err := cmd.Run()
	require.NoError(t, err)
	require.Empty(t, stderr.String())

	output := stdout.String()
	require.Contains(t, output, "Checking GitLab API access: OK")
	require.Contains(t, output, "Redis reachable for GitLab: true")
}

func TestCheckBadCreds(t *testing.T) {
	user, password := "user123", "password321"

	c := gitlab.TestServerOptions{
		User:                        user,
		Password:                    password,
		SecretToken:                 "",
		GLRepository:                "",
		Changes:                     "",
		PostReceiveCounterDecreased: false,
		Protocol:                    "ssh",
		GitPushOptions:              nil,
	}
	serverURL, cleanup := gitlab.NewTestServer(t, c)
	defer cleanup()

	cfg := testcfg.Build(t, testcfg.WithBase(config.Cfg{
		Gitlab: config.Gitlab{
			URL: serverURL,
			HTTPSettings: config.HTTPSettings{
				User:     "wrong",
				Password: password,
			},
			SecretFile: gitlab.WriteShellSecretFile(t, testhelper.TempDir(t), "the secret"),
		},
	}))
	testcfg.BuildGitaly(t, cfg)

	configPath := writeTemporaryGitalyConfigFile(t, cfg)

	cmd := exec.Command(cfg.BinaryPath("gitaly"), "check", configPath)

	var stderr, stdout bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdout = &stdout

	require.Error(t, cmd.Run())
	require.Contains(t, stderr.String(), "HTTP GET to GitLab endpoint /check failed: authorization failed")
	require.Regexp(t, `Checking GitLab API access: .* level=error msg="Internal API error" .* error="authorization failed" method=GET status=401 url="http://127.0.0.1:[0-9]+/api/v4/internal/check"\nFAIL`, stdout.String())
}

// writeTemporaryGitalyConfigFile writes the given Gitaly configuration into a temporary file and
// returns its path.
func writeTemporaryGitalyConfigFile(tb testing.TB, cfg config.Cfg) string {
	tb.Helper()

	path := filepath.Join(testhelper.TempDir(tb), "config.toml")

	contents, err := toml.Marshal(cfg)
	require.NoError(tb, err)
	require.NoError(tb, os.WriteFile(path, contents, 0o644))

	return path
}
