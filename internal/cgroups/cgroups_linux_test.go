//go:build !gitaly_test_sha256

package cgroups

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/cgroups"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func TestNewManager(t *testing.T) {
	cfg := cgroups.Config{Repositories: cgroups.Repositories{Count: 10}}

	require.IsType(t, &CGroupV1Manager{}, &CGroupV1Manager{cfg: cfg})
	require.IsType(t, &NoopManager{}, NewManager(cgroups.Config{}, 1))
}

func TestPruneOldCgroups(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		desc           string
		cfg            cgroups.Config
		expectedPruned bool
		// setup returns a pid
		setup func(*testing.T, cgroups.Config) int
	}{
		{
			desc: "process belongs to another user",
			cfg: cgroups.Config{
				Mountpoint:    testhelper.TempDir(t),
				HierarchyRoot: "gitaly",
				Repositories: cgroups.Repositories{
					Count:       10,
					MemoryBytes: 10 * 1024 * 1024,
					CPUShares:   1024,
				},
			},
			setup: func(t *testing.T, cfg cgroups.Config) int {
				pid := 1
				cgroupManager := NewManager(cfg, pid)
				require.NoError(t, cgroupManager.Setup())

				return pid
			},
			expectedPruned: true,
		},
		{
			desc: "no hierarchy root",
			cfg: cgroups.Config{
				Mountpoint:    testhelper.TempDir(t),
				HierarchyRoot: "",
				Repositories: cgroups.Repositories{
					Count:       10,
					MemoryBytes: 10 * 1024 * 1024,
					CPUShares:   1024,
				},
			},
			setup: func(t *testing.T, cfg cgroups.Config) int {
				pid := 1
				cgroupManager := NewManager(cfg, pid)
				require.NoError(t, cgroupManager.Setup())

				return 1
			},
			expectedPruned: false,
		},
		{
			desc: "pid of finished process",
			cfg: cgroups.Config{
				Mountpoint:    testhelper.TempDir(t),
				HierarchyRoot: "gitaly",
				Repositories: cgroups.Repositories{
					Count:       10,
					MemoryBytes: 10 * 1024 * 1024,
					CPUShares:   1024,
				},
			},
			setup: func(t *testing.T, cfg cgroups.Config) int {
				cmd := exec.Command("ls")
				require.NoError(t, cmd.Run())
				pid := cmd.Process.Pid

				cgroupManager := NewManager(cfg, pid)
				require.NoError(t, cgroupManager.Setup())

				return pid
			},
			expectedPruned: true,
		},
		{
			desc: "pid of running process",
			cfg: cgroups.Config{
				Mountpoint:    testhelper.TempDir(t),
				HierarchyRoot: "gitaly",
				Repositories: cgroups.Repositories{
					Count:       10,
					MemoryBytes: 10 * 1024 * 1024,
					CPUShares:   1024,
				},
			},
			setup: func(t *testing.T, cfg cgroups.Config) int {
				pid := os.Getpid()

				cgroupManager := NewManager(cfg, pid)
				require.NoError(t, cgroupManager.Setup())

				return pid
			},
			expectedPruned: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			memoryRoot := filepath.Join(
				tc.cfg.Mountpoint,
				"memory",
				tc.cfg.HierarchyRoot,
			)
			cpuRoot := filepath.Join(
				tc.cfg.Mountpoint,
				"cpu",
				tc.cfg.HierarchyRoot,
			)

			require.NoError(t, os.MkdirAll(cpuRoot, os.ModePerm))
			require.NoError(t, os.MkdirAll(memoryRoot, os.ModePerm))

			pid := tc.setup(t, tc.cfg)

			logger, hook := test.NewNullLogger()
			PruneOldCgroups(tc.cfg, logger)

			// create cgroups directories with a different pid
			oldGitalyProcessMemoryDir := filepath.Join(
				memoryRoot,
				fmt.Sprintf("gitaly-%d", pid),
			)
			oldGitalyProcesssCPUDir := filepath.Join(
				cpuRoot,
				fmt.Sprintf("gitaly-%d", pid),
			)

			if tc.expectedPruned {
				require.NoDirExists(t, oldGitalyProcessMemoryDir)
				require.NoDirExists(t, oldGitalyProcesssCPUDir)
			} else {
				require.DirExists(t, oldGitalyProcessMemoryDir)
				require.DirExists(t, oldGitalyProcesssCPUDir)
				require.Len(t, hook.Entries, 0)
			}
		})
	}
}
