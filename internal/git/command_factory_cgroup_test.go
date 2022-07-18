//go:build !gitaly_test_sha256

package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/cgroups"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

type mockCgroupsManager struct {
	commands []*command.Command
}

func (m *mockCgroupsManager) Setup() error {
	return nil
}

func (m *mockCgroupsManager) AddCommand(c *command.Command, repo repository.GitRepo) (string, error) {
	m.commands = append(m.commands, c)
	return "", nil
}

func (m *mockCgroupsManager) Cleanup() error {
	return nil
}

func (m *mockCgroupsManager) Collect(ch chan<- prometheus.Metric) {}
func (m *mockCgroupsManager) Describe(ch chan<- *prometheus.Desc) {}

func TestNewCommandAddsToCgroup(t *testing.T) {
	root := testhelper.TempDir(t)

	cfg := config.Cfg{
		BinDir:     filepath.Join(root, "bin.d"),
		SocketPath: "/path/to/socket",
		Git: config.Git{
			IgnoreGitconfig: true,
		},
		Cgroups: cgroups.Config{
			Repositories: cgroups.Repositories{
				Count: 1,
			},
		},
		Storages: []config.Storage{{
			Name: "storage-1",
			Path: root,
		}},
	}

	require.NoError(t, os.MkdirAll(cfg.BinDir, 0o755))

	gitCmdFactory := newCommandFactory(t, cfg, WithSkipHooks())

	testCases := []struct {
		desc      string
		cgroupsFF bool
	}{
		{
			desc:      "cgroups feature flag on",
			cgroupsFF: true,
		},
		{
			desc:      "cgroups feature flag off",
			cgroupsFF: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			dir := testhelper.TempDir(t)

			var manager mockCgroupsManager
			gitCmdFactory.cgroupsManager = &manager
			ctx := testhelper.Context(t)

			ctx = featureflag.IncomingCtxWithFeatureFlag(ctx, featureflag.RunCommandsInCGroup, tc.cgroupsFF)

			cmd := SubCmd{
				Name: "hash-object",
			}

			_, err := gitCmdFactory.NewWithDir(ctx, dir, &cmd)
			require.NoError(t, err)

			if tc.cgroupsFF {
				require.Len(t, manager.commands, 1)
				assert.Contains(t, manager.commands[0].Args(), "hash-object")
				return
			}

			require.Len(t, manager.commands, 0)
		})
	}
}
