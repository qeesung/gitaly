//go:build !gitaly_test_sha256

package main

import (
	"path/filepath"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitlab"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func runTestServer(t *testing.T, options gitlab.TestServerOptions) (config.Gitlab, func()) {
	tempDir := testhelper.TempDir(t)

	gitlab.WriteShellSecretFile(t, tempDir, secretToken)
	secretFilePath := filepath.Join(tempDir, ".gitlab_shell_secret")

	serverURL, serverCleanup := gitlab.NewTestServer(t, options)

	c := config.Gitlab{URL: serverURL, SecretFile: secretFilePath, HTTPSettings: config.HTTPSettings{CAFile: certPath}}

	return c, func() {
		serverCleanup()
	}
}
