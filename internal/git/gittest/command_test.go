//go:build !gitaly_test_sha256

package gittest

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
)

func TestExecOpts(t *testing.T) {
	cfg, _, repoPath := setup(t)

	t.Run("default config", func(t *testing.T) {
		toplevel := ExecOpts(t, cfg, ExecConfig{}, "-C", repoPath, "rev-parse", "--path-format=absolute", "--git-dir")
		require.Equal(t, repoPath, text.ChompBytes(toplevel))
	})

	t.Run("env", func(t *testing.T) {
		configValue := ExecOpts(t, cfg, ExecConfig{
			Env: []string{
				"GIT_CONFIG_COUNT=1",
				"GIT_CONFIG_KEY_0=injected.env-config",
				"GIT_CONFIG_VALUE_0=some value",
			},
		}, "-C", repoPath, "config", "injected.env-config")

		require.Equal(t, "some value", text.ChompBytes(configValue))
	})

	t.Run("stdin", func(t *testing.T) {
		blobID := ExecOpts(t, cfg, ExecConfig{
			Stdin: strings.NewReader("blob contents"),
		}, "-C", repoPath, "hash-object", "-w", "--stdin")

		require.Equal(t,
			"blob contents",
			string(Exec(t, cfg, "-C", repoPath, "cat-file", "-p", text.ChompBytes(blobID))),
		)
	})

	t.Run("stdout", func(t *testing.T) {
		var buf bytes.Buffer
		ExecOpts(t, cfg, ExecConfig{
			Stdout: &buf,
		}, "-C", repoPath, "rev-parse", "--path-format=absolute", "--git-dir")

		require.Equal(t, repoPath, text.ChompBytes(buf.Bytes()))
	})
}
