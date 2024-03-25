package main

import (
	"bytes"
	"errors"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/version"
)

func TestGitalyCLI(t *testing.T) {
	cfg := testcfg.Build(t)
	binaryPath := testcfg.BuildGitaly(t, cfg)

	for _, tc := range []struct {
		desc     string
		args     []string
		exitCode int
		stdout   string
		stderr   string
	}{
		{
			desc:   "version invocation",
			args:   []string{"-version"},
			stdout: version.GetVersionString("Gitaly") + "\n",
		},
		{
			desc:     "without arguments",
			exitCode: 2,
			stdout:   "NAME:\n   gitaly - a Git RPC service\n\nUSAGE:\n   gitaly command [command options] \n\nDESCRIPTION:\n   Gitaly is a Git RPC service for handling Git calls.\n\nCOMMANDS:\n   serve          launch the server daemon\n   check          verify internal API is accessible\n   configuration  run configuration-related commands\n   hooks          manage Git hooks\n\nOPTIONS:\n   --help, -h     show help\n   --version, -v  print the version\n",
		},
		{
			desc:     "with non-existent config",
			args:     []string{"non-existent-file"},
			exitCode: 1,
			stderr:   `load config: config_path "non-existent-file"`,
		},
		{
			desc:     "check without config",
			args:     []string{"check"},
			exitCode: 2,
			stdout:   "NAME:\n   gitaly check - verify internal API is accessible\n\nUSAGE:\n   gitaly check <gitaly_config_file>\n\n   Example: gitaly check gitaly.config.toml\n\nDESCRIPTION:\n   Check that the internal Gitaly API is accessible.\n\nOPTIONS:\n   --help, -h  show help\n",
			stderr:   "invalid argument(s)",
		},
		{
			desc:     "check with non-existent config",
			args:     []string{"check", "non-existent-file"},
			exitCode: 1,
			stderr:   "loading configuration \"non-existent-file\": open non-existent-file: no such file or directory\n",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			var stdout, stderr bytes.Buffer
			cmd := exec.CommandContext(ctx, binaryPath, tc.args...)
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err := cmd.Run()

			exitCode := 0
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			}

			assert.Equal(t, tc.exitCode, exitCode)
			if tc.stdout == "" {
				assert.Empty(t, stdout.String())
			}
			assert.Contains(t, stdout.String(), tc.stdout)

			if tc.stderr == "" {
				assert.Empty(t, stderr.String())
			}
			assert.Contains(t, stderr.String(), tc.stderr)
		})
	}
}
