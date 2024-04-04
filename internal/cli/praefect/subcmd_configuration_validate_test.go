package praefect

import (
	"bytes"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/command"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestConfiguration_validate(t *testing.T) {
	t.Parallel()

	praefectBin := testcfg.BuildBinary(t, testhelper.TempDir(t), "gitlab.com/gitlab-org/gitaly/v16/cmd/praefect")

	for _, tc := range []struct {
		name     string
		exitCode int
		args     []string
		stdin    func(t *testing.T) io.Reader
		stderr   string
		stdout   string
	}{
		{
			name:     "ok",
			exitCode: 0,
			stdin: func(*testing.T) io.Reader {
				return strings.NewReader(`listen_addr="localhost:1000"
[[virtual_storage]]
name = "praefect"
[[virtual_storage.node]]
address = "tcp://gitaly-internal-1.example.com"
storage = "praefect-internal-1"`)
			},
		},
		{
			name:     "bad toml format",
			exitCode: 2,
			stdin: func(*testing.T) io.Reader {
				return strings.NewReader(`listen_addr="localhost:1000"
[failover]
enabled = true
election_strategy = invalid`)
			},
			stdout: `{
  "errors": [
    {
      "message": "line 4 column 21: toml: expected 'inf'"
    }
  ]
}
`,
		},
		{
			name:     "additional arguments are not accepted",
			exitCode: 1,
			args:     []string{"arg1", "arg2"},
			stdin:    func(t *testing.T) io.Reader { return nil },
			stdout: `NAME:
   praefect configuration validate - validates configuration

USAGE:
   praefect configuration validate command [command options] ` + `

DESCRIPTION:
   Validate Praefect configuration.

   Applies validation rules to Praefect configuration provided on stdin and returns validation
   errors in JSON format on stdout.

   Example: praefect configuration validate < praefect.config.toml

OPTIONS:
   --help, -h  show help
`,
			stderr: "invalid argument(s)\n",
		},
		{
			name:     "validation failures",
			exitCode: 2,
			stdin: func(t *testing.T) io.Reader {
				return strings.NewReader("")
			},
			stdout: `{
  "errors": [
    {
      "message": "none of \"socket_path\", \"listen_addr\" or \"tls_listen_addr\" is set"
    },
    {
      "key": [
        "virtual_storage"
      ],
      "message": "not set"
    }
  ]
}
`,
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmd := exec.Command(praefectBin, append([]string{"configuration", "validate"}, tc.args...)...)

			var stdout, stderr bytes.Buffer
			cmd.Stdin = tc.stdin(t)
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr

			err := cmd.Run()
			if tc.exitCode != 0 {
				status, ok := command.ExitStatus(err)
				require.Truef(t, ok, "%T: %v", err, err)
				assert.Equal(t, tc.exitCode, status)
			}
			assert.Equal(t, tc.stderr, stderr.String())
			assert.Equal(t, tc.stdout, stdout.String())
		})
	}
}
