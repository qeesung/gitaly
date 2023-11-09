//go:build linux

package cgroups

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	cgrps "github.com/containerd/cgroups/v3"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/cgroups"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"golang.org/x/exp/slices"
)

func TestNewManager(t *testing.T) {
	cfg := cgroups.Config{Repositories: cgroups.Repositories{Count: 10}}

	manager := newCgroupManagerWithMode(cfg, testhelper.SharedLogger(t), 1, cgrps.Legacy)
	require.IsType(t, &cgroupV1Handler{}, manager.handler)
	manager = newCgroupManagerWithMode(cfg, testhelper.SharedLogger(t), 1, cgrps.Hybrid)
	require.IsType(t, &cgroupV1Handler{}, manager.handler)
	manager = newCgroupManagerWithMode(cfg, testhelper.SharedLogger(t), 1, cgrps.Unified)
	require.IsType(t, &cgroupV2Handler{}, manager.handler)
	manager = newCgroupManagerWithMode(cfg, testhelper.SharedLogger(t), 1, cgrps.Unavailable)
	require.Nil(t, manager)
}

type expectedCgroup struct {
	wantMemoryBytes int
	wantCPUShares   int
	wantCPUQuotaUs  int
	wantCFSPeriod   int
	wantCPUWeight   int
	wantCPUMax      string
}

func TestSetup_ParentCgroups(t *testing.T) {
	tests := []struct {
		name       string
		cfg        cgroups.Config
		expectedV1 expectedCgroup
		expectedV2 expectedCgroup
	}{
		{
			name: "all config specified",
			cfg: cgroups.Config{
				MemoryBytes: 102400,
				CPUShares:   256,
				CPUQuotaUs:  200,
			},
			expectedV1: expectedCgroup{
				wantMemoryBytes: 102400,
				wantCPUShares:   256,
				wantCPUQuotaUs:  200,
				wantCFSPeriod:   int(cfsPeriodUs),
			},
			expectedV2: expectedCgroup{
				wantMemoryBytes: 102400,
				wantCPUWeight:   256,
				wantCPUMax:      "200 100000",
			},
		},
		{
			name: "only memory limit set",
			cfg: cgroups.Config{
				MemoryBytes: 102400,
			},
			expectedV1: expectedCgroup{
				wantMemoryBytes: 102400,
			},
			expectedV2: expectedCgroup{
				wantMemoryBytes: 102400,
			},
		},
		{
			name: "only cpu shares set",
			cfg: cgroups.Config{
				CPUShares: 512,
			},
			expectedV1: expectedCgroup{
				wantCPUShares: 512,
			},
			expectedV2: expectedCgroup{
				wantCPUWeight: 512,
			},
		},
		{
			name: "only cpu quota set",
			cfg: cgroups.Config{
				CPUQuotaUs: 200,
			},
			expectedV1: expectedCgroup{
				wantCPUQuotaUs: 200,
				wantCFSPeriod:  int(cfsPeriodUs),
			},
			expectedV2: expectedCgroup{
				wantCPUMax: "200 100000",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			for _, version := range []int{1, 2} {
				version := version
				t.Run("cgroups-v"+strconv.Itoa(version), func(t *testing.T) {
					t.Parallel()

					mock := newMock(t, version)

					pid := 1

					cfg := tt.cfg
					cfg.HierarchyRoot = "gitaly"
					cfg.Mountpoint = mock.rootPath()

					manager := mock.newCgroupManager(cfg, testhelper.SharedLogger(t), pid)
					mock.setupMockCgroupFiles(t, manager, []uint{})

					require.False(t, manager.Ready())
					require.NoError(t, manager.Setup())
					require.True(t, manager.Ready())

					cgroupPath := filepath.Join("gitaly", fmt.Sprintf("gitaly-%d", pid))
					if version == 1 {
						requireCgroupComponents(t, version, mock.rootPath(), cgroupPath, tt.expectedV1)
					} else {
						requireCgroupComponents(t, version, mock.rootPath(), cgroupPath, tt.expectedV2)
					}
				})
			}
		})
	}
}

func TestRepoCgroups(t *testing.T) {
	tests := []struct {
		name       string
		cfg        cgroups.Repositories
		expectedV1 expectedCgroup
		expectedV2 expectedCgroup
	}{
		{
			name: "all config specified",
			cfg:  defaultCgroupsConfig().Repositories,
			expectedV1: expectedCgroup{
				wantMemoryBytes: 1024000,
				wantCPUShares:   256,
				wantCPUQuotaUs:  200,
				wantCFSPeriod:   int(cfsPeriodUs),
			},
			expectedV2: expectedCgroup{
				wantMemoryBytes: 1024000,
				wantCPUWeight:   256,
				wantCPUMax:      "200 100000",
			},
		},
		{
			name: "only memory limit set",
			cfg: cgroups.Repositories{
				MemoryBytes: 1024000,
			},
			expectedV1: expectedCgroup{
				wantMemoryBytes: 1024000,
			},
			expectedV2: expectedCgroup{
				wantMemoryBytes: 1024000,
			},
		},
		{
			name: "only cpu shares set",
			cfg: cgroups.Repositories{
				CPUShares: 512,
			},
			expectedV1: expectedCgroup{
				wantCPUShares: 512,
			},
			expectedV2: expectedCgroup{
				wantCPUWeight: 512,
			},
		},
		{
			name: "only cpu quota set",
			cfg: cgroups.Repositories{
				CPUQuotaUs: 100,
			},
			expectedV1: expectedCgroup{
				wantCPUQuotaUs: 100,
				wantCFSPeriod:  int(cfsPeriodUs),
			},
			expectedV2: expectedCgroup{
				wantCPUMax: "100 100000",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			for _, version := range []int{1, 2} {
				version := version
				t.Run("cgroups-v"+strconv.Itoa(version), func(t *testing.T) {
					t.Parallel()

					mock := newMock(t, version)

					pid := 1
					cfg := defaultCgroupsConfig()
					cfg.Repositories = tt.cfg
					cfg.Repositories.Count = 3
					cfg.HierarchyRoot = "gitaly"
					cfg.Mountpoint = mock.rootPath()

					manager := mock.newCgroupManager(cfg, testhelper.SharedLogger(t), pid)

					// Validate no shards have been created. We deliberately do not call
					// `setupMockCgroupFiles()` here to confirm that the cgroup controller
					// is creating repository directories in the correct location.
					requireShards(t, version, mock, manager, pid)

					groupID := calcGroupID(cmdArgs, cfg.Repositories.Count)

					mock.setupMockCgroupFiles(t, manager, []uint{groupID})

					require.False(t, manager.Ready())
					require.NoError(t, manager.Setup())
					require.True(t, manager.Ready())

					ctx := testhelper.Context(t)

					// Create a command to force Gitaly to create the repo cgroup.
					cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
					require.NoError(t, cmd.Run())
					_, err := manager.AddCommand(cmd)
					require.NoError(t, err)

					requireShards(t, version, mock, manager, pid, groupID)

					var expected expectedCgroup
					if version == 1 {
						expected = tt.expectedV1
					} else {
						expected = tt.expectedV2
					}

					for shard := uint(0); shard < cfg.Repositories.Count; shard++ {
						// The negative case where no directory should exist is asserted
						// by `requireShards()`.
						if shard == groupID {
							cgRelPath := filepath.Join(
								"gitaly", fmt.Sprintf("gitaly-%d", pid), fmt.Sprintf("repos-%d", shard),
							)

							requireCgroupComponents(t, version, mock.rootPath(), cgRelPath, expected)
						}
					}
				})
			}
		})
	}
}

func TestAddCommand(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run("cgroups-v"+strconv.Itoa(version), func(t *testing.T) {
			mock := newMock(t, version)

			config := defaultCgroupsConfig()
			config.Repositories.Count = 10
			config.Repositories.MemoryBytes = 1024
			config.Repositories.CPUShares = 16
			config.HierarchyRoot = "gitaly"
			config.Mountpoint = mock.rootPath()

			pid := 1
			manager1 := mock.newCgroupManager(config, testhelper.SharedLogger(t), pid)
			mock.setupMockCgroupFiles(t, manager1, []uint{})
			require.NoError(t, manager1.Setup())

			ctx := testhelper.Context(t)

			cmd2 := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
			require.NoError(t, cmd2.Run())

			groupID := calcGroupID(cmd2.Args, config.Repositories.Count)

			manager2 := mock.newCgroupManager(config, testhelper.SharedLogger(t), pid)

			t.Run("without overridden key", func(t *testing.T) {
				_, err := manager2.AddCommand(cmd2)
				require.NoError(t, err)
				requireShards(t, version, mock, manager2, pid, groupID)

				for _, path := range mock.repoPaths(pid, groupID) {
					procsPath := filepath.Join(path, "cgroup.procs")
					content := readCgroupFile(t, procsPath)

					cmdPid, err := strconv.Atoi(string(content))
					require.NoError(t, err)

					require.Equal(t, cmd2.Process.Pid, cmdPid)
				}
			})

			t.Run("with overridden key", func(t *testing.T) {
				overriddenGroupID := calcGroupID([]string{"foobar"}, config.Repositories.Count)

				_, err := manager2.AddCommand(cmd2, WithCgroupKey("foobar"))
				require.NoError(t, err)
				requireShards(t, version, mock, manager2, pid, groupID, overriddenGroupID)

				for _, path := range mock.repoPaths(pid, overriddenGroupID) {
					procsPath := filepath.Join(path, "cgroup.procs")
					content := readCgroupFile(t, procsPath)

					cmdPid, err := strconv.Atoi(string(content))
					require.NoError(t, err)

					require.Equal(t, cmd2.Process.Pid, cmdPid)
				}
			})
		})
	}
}

func TestCleanup(t *testing.T) {
	for _, version := range []int{1, 2} {
		t.Run("cgroups-v"+strconv.Itoa(version), func(t *testing.T) {
			mock := newMock(t, version)

			pid := 1
			cfg := defaultCgroupsConfig()
			cfg.Mountpoint = mock.rootPath()

			manager := mock.newCgroupManager(cfg, testhelper.SharedLogger(t), pid)
			mock.setupMockCgroupFiles(t, manager, []uint{0, 1, 2})

			require.NoError(t, manager.Setup())
			require.NoError(t, manager.Cleanup())

			for i := uint(0); i < 3; i++ {
				for _, path := range mock.repoPaths(pid, i) {
					require.NoDirExists(t, path)
				}
			}
		})
	}
}

func TestMetrics(t *testing.T) {
	tests := []struct {
		name           string
		metricsEnabled bool
		pid            int
		expectV1       string
		expectV2       string
	}{
		{
			name:           "metrics enabled: true",
			metricsEnabled: true,
			pid:            1,
			expectV1: `# HELP gitaly_cgroup_cpu_usage_total CPU Usage of Cgroup
# TYPE gitaly_cgroup_cpu_usage_total gauge
gitaly_cgroup_cpu_usage_total{path="%s",type="kernel"} 0
gitaly_cgroup_cpu_usage_total{path="%s",type="user"} 0
# HELP gitaly_cgroup_memory_reclaim_attempts_total Number of memory usage hits limits
# TYPE gitaly_cgroup_memory_reclaim_attempts_total gauge
gitaly_cgroup_memory_reclaim_attempts_total{path="%s"} 2
# HELP gitaly_cgroup_procs_total Total number of procs
# TYPE gitaly_cgroup_procs_total gauge
gitaly_cgroup_procs_total{path="%s",subsystem="cpu"} 1
gitaly_cgroup_procs_total{path="%s",subsystem="memory"} 1
# HELP gitaly_cgroup_cpu_cfs_periods_total Number of elapsed enforcement period intervals
# TYPE gitaly_cgroup_cpu_cfs_periods_total counter
gitaly_cgroup_cpu_cfs_periods_total{path="%s"} 10
# HELP gitaly_cgroup_cpu_cfs_throttled_periods_total Number of throttled period intervals
# TYPE gitaly_cgroup_cpu_cfs_throttled_periods_total counter
gitaly_cgroup_cpu_cfs_throttled_periods_total{path="%s"} 20
# HELP gitaly_cgroup_cpu_cfs_throttled_seconds_total Total time duration the Cgroup has been throttled
# TYPE gitaly_cgroup_cpu_cfs_throttled_seconds_total counter
gitaly_cgroup_cpu_cfs_throttled_seconds_total{path="%s"} 0.001
`,
			expectV2: `# HELP gitaly_cgroup_cpu_cfs_periods_total Number of elapsed enforcement period intervals
# TYPE gitaly_cgroup_cpu_cfs_periods_total counter
gitaly_cgroup_cpu_cfs_periods_total{path="%s"} 10
# HELP gitaly_cgroup_cpu_cfs_throttled_periods_total Number of throttled period intervals
# TYPE gitaly_cgroup_cpu_cfs_throttled_periods_total counter
gitaly_cgroup_cpu_cfs_throttled_periods_total{path="%s"} 20
# HELP gitaly_cgroup_cpu_cfs_throttled_seconds_total Total time duration the Cgroup has been throttled
# TYPE gitaly_cgroup_cpu_cfs_throttled_seconds_total counter
gitaly_cgroup_cpu_cfs_throttled_seconds_total{path="%s"} 0.001
# HELP gitaly_cgroup_cpu_usage_total CPU Usage of Cgroup
# TYPE gitaly_cgroup_cpu_usage_total gauge
gitaly_cgroup_cpu_usage_total{path="%s",type="kernel"} 0
gitaly_cgroup_cpu_usage_total{path="%s",type="user"} 0
# HELP gitaly_cgroup_procs_total Total number of procs
# TYPE gitaly_cgroup_procs_total gauge
gitaly_cgroup_procs_total{path="%s",subsystem="cpu"} 1
gitaly_cgroup_procs_total{path="%s",subsystem="cpuset"} 1
gitaly_cgroup_procs_total{path="%s",subsystem="memory"} 1
`,
		},
		{
			name:           "metrics enabled: false",
			metricsEnabled: false,
			pid:            2,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			for _, version := range []int{1, 2} {
				version := version
				t.Run("cgroups-v"+strconv.Itoa(version), func(t *testing.T) {
					t.Parallel()
					mock := newMock(t, version)

					config := defaultCgroupsConfig()
					config.Repositories.Count = 1
					config.Repositories.MemoryBytes = 1048576
					config.Repositories.CPUShares = 16
					config.Mountpoint = mock.rootPath()
					config.MetricsEnabled = tt.metricsEnabled

					manager1 := mock.newCgroupManager(config, testhelper.SharedLogger(t), tt.pid)

					var mockFiles []mockCgroupFile
					if version == 1 {
						mockFiles = append(mockFiles, mockCgroupFile{"memory.failcnt", "2"})
					}
					mock.setupMockCgroupFiles(t, manager1, []uint{0}, mockFiles...)
					require.NoError(t, manager1.Setup())

					ctx := testhelper.Context(t)

					cmd := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
					require.NoError(t, cmd.Start())
					_, err := manager1.AddCommand(cmd)
					require.NoError(t, err)

					gitCmd1 := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
					require.NoError(t, gitCmd1.Start())
					_, err = manager1.AddCommand(gitCmd1)
					require.NoError(t, err)

					gitCmd2 := exec.CommandContext(ctx, cmdArgs[0], cmdArgs[1:]...)
					require.NoError(t, gitCmd2.Start())
					_, err = manager1.AddCommand(gitCmd2)
					require.NoError(t, err)
					defer func() {
						require.NoError(t, gitCmd2.Wait())
					}()

					require.NoError(t, cmd.Wait())
					require.NoError(t, gitCmd1.Wait())

					repoCgroupPath := filepath.Join(manager1.currentProcessCgroup(), "repos-0")

					var expected *strings.Reader
					if version == 1 {
						expected = strings.NewReader(strings.ReplaceAll(tt.expectV1, "%s", repoCgroupPath))
					} else {
						expected = strings.NewReader(strings.ReplaceAll(tt.expectV2, "%s", repoCgroupPath))
					}
					assert.NoError(t, testutil.CollectAndCompare(manager1, expected))
				})
			}
		})
	}
}

func TestPruneOldCgroups(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		desc           string
		cfg            cgroups.Config
		expectedPruned bool
		// setup returns a pid
		setup func(t *testing.T, cfg cgroups.Config, mock mockCgroup) int
	}{
		{
			desc: "process belongs to another user",
			cfg: cgroups.Config{
				HierarchyRoot: "gitaly",
				Repositories: cgroups.Repositories{
					Count:       10,
					MemoryBytes: 10 * 1024 * 1024,
					CPUShares:   1024,
				},
			},
			setup: func(t *testing.T, cfg cgroups.Config, mock mockCgroup) int {
				pid := 1
				cgroupManager := mock.newCgroupManager(cfg, testhelper.SharedLogger(t), pid)
				mock.setupMockCgroupFiles(t, cgroupManager, []uint{0, 1, 2})
				require.NoError(t, cgroupManager.Setup())

				return pid
			},
			expectedPruned: true,
		},
		{
			desc: "no hierarchy root",
			cfg: cgroups.Config{
				HierarchyRoot: "",
				Repositories: cgroups.Repositories{
					Count:       10,
					MemoryBytes: 10 * 1024 * 1024,
					CPUShares:   1024,
				},
			},
			setup: func(t *testing.T, cfg cgroups.Config, mock mockCgroup) int {
				pid := 1
				cgroupManager := mock.newCgroupManager(cfg, testhelper.SharedLogger(t), pid)
				mock.setupMockCgroupFiles(t, cgroupManager, []uint{0, 1, 2})
				require.NoError(t, cgroupManager.Setup())
				return 1
			},
			expectedPruned: false,
		},
		{
			desc: "pid of finished process",
			cfg: cgroups.Config{
				HierarchyRoot: "gitaly",
				Repositories: cgroups.Repositories{
					Count:       10,
					MemoryBytes: 10 * 1024 * 1024,
					CPUShares:   1024,
				},
			},
			setup: func(t *testing.T, cfg cgroups.Config, mock mockCgroup) int {
				cmd := exec.Command("ls")
				require.NoError(t, cmd.Run())
				pid := cmd.Process.Pid

				cgroupManager := mock.newCgroupManager(cfg, testhelper.SharedLogger(t), pid)
				mock.setupMockCgroupFiles(t, cgroupManager, []uint{0, 1, 2})

				if mock.version() == 1 {
					memoryRoot := filepath.Join(
						cfg.Mountpoint,
						"memory",
						cfg.HierarchyRoot,
						"memory.limit_in_bytes",
					)
					require.NoError(t, os.WriteFile(memoryRoot, []byte{}, fs.ModeAppend))
				} else {
					require.NoError(t, cgroupManager.Setup())

					memoryFile := filepath.Join(
						cfg.Mountpoint,
						cfg.HierarchyRoot,
						"memory.limit_in_bytes",
					)
					require.NoError(t, os.WriteFile(memoryFile, []byte{}, fs.ModeAppend))
				}

				return pid
			},
			expectedPruned: true,
		},
		{
			desc: "pid of running process",
			cfg: cgroups.Config{
				HierarchyRoot: "gitaly",
				Repositories: cgroups.Repositories{
					Count:       10,
					MemoryBytes: 10 * 1024 * 1024,
					CPUShares:   1024,
				},
			},
			setup: func(t *testing.T, cfg cgroups.Config, mock mockCgroup) int {
				pid := os.Getpid()

				cgroupManager := mock.newCgroupManager(cfg, testhelper.SharedLogger(t), pid)
				mock.setupMockCgroupFiles(t, cgroupManager, []uint{0, 1, 2})
				require.NoError(t, cgroupManager.Setup())

				return pid
			},
			expectedPruned: false,
		},
		{
			desc: "gitaly-0 directory is deleted",
			cfg: cgroups.Config{
				HierarchyRoot: "gitaly",
				Repositories: cgroups.Repositories{
					Count:       10,
					MemoryBytes: 10 * 1024 * 1024,
					CPUShares:   1024,
				},
			},
			setup: func(t *testing.T, cfg cgroups.Config, mock mockCgroup) int {
				cgroupManager := mock.newCgroupManager(cfg, testhelper.SharedLogger(t), 0)
				mock.setupMockCgroupFiles(t, cgroupManager, []uint{0, 1, 2})
				require.NoError(t, cgroupManager.Setup())

				return 0
			},
			expectedPruned: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			t.Run("cgroups-v1", func(t *testing.T) {
				mock := newMock(t, 1)
				tc.cfg.Mountpoint = mock.rootPath()

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

				require.NoError(t, os.MkdirAll(cpuRoot, perm.PublicDir))
				require.NoError(t, os.MkdirAll(memoryRoot, perm.PublicDir))

				pid := tc.setup(t, tc.cfg, mock)

				logger := testhelper.NewLogger(t)
				hook := testhelper.AddLoggerHook(logger)

				mock.pruneOldCgroups(tc.cfg, logger)

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
					require.Empty(t, hook.AllEntries())
				}
			})

			t.Run("cgroups-v2", func(t *testing.T) {
				mock := newMock(t, 2)
				tc.cfg.Mountpoint = mock.rootPath()

				root := filepath.Join(
					tc.cfg.Mountpoint,
					tc.cfg.HierarchyRoot,
				)
				require.NoError(t, os.MkdirAll(root, perm.PublicDir))

				pid := tc.setup(t, tc.cfg, mock)

				logger := testhelper.NewLogger(t)
				mock.pruneOldCgroups(tc.cfg, logger)

				// create cgroups directories with a different pid
				oldGitalyProcessDir := filepath.Join(
					root,
					fmt.Sprintf("gitaly-%d", pid),
				)

				if tc.expectedPruned {
					require.NoDirExists(t, oldGitalyProcessDir)
				} else {
					require.DirExists(t, oldGitalyProcessDir)
				}
			})
		})
	}
}

func TestStats(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		desc          string
		version       int
		mockFiles     []mockCgroupFile
		expectedStats Stats
	}{
		{
			desc:    "empty statistics",
			version: 1,
			mockFiles: []mockCgroupFile{
				{"memory.limit_in_bytes", "0"},
				{"memory.usage_in_bytes", "0"},
				{"memory.oom_control", ""},
				{"cpu.stat", ""},
			},
			expectedStats: Stats{},
		},
		{
			desc:    "cgroupfs recorded some stats",
			version: 1,
			mockFiles: []mockCgroupFile{
				{"memory.limit_in_bytes", "2000000000"},
				{"memory.usage_in_bytes", "1234000000"},
				{"memory.oom_control", `oom_kill_disable 1
under_oom 1
oom_kill 3`},
				{"cpu.stat", `nr_periods 10
nr_throttled 50
throttled_time 1000000`}, // 0.001 seconds
			},
			expectedStats: Stats{
				ParentStats: CgroupStats{
					CPUThrottledCount:    50,
					CPUThrottledDuration: 0.001,
					MemoryUsage:          1234000000,
					MemoryLimit:          2000000000,
					OOMKills:             3,
					UnderOOM:             true,
				},
			},
		},
		{
			desc:    "empty statistics",
			version: 2,
			mockFiles: []mockCgroupFile{
				{"memory.current", "0"},
				{"memory.max", "0"},
				{"cpu.stat", ""},
			},
			expectedStats: Stats{},
		},
		{
			desc:    "cgroupfs recorded some stats",
			version: 2,
			mockFiles: []mockCgroupFile{
				{"memory.max", "2000000000"},
				{"memory.current", "1234000000"},
				{"memory.events", `low 1
high 2
max 3
oom 4
oom_kill 5`},
				{"nr_throttled", "50"},
				{"throttled_usec", "1000000"},
				{"cpu.stat", `nr_periods 10
nr_throttled 50
throttled_usec 1000000`}, // 0.001 seconds
			},
			expectedStats: Stats{
				ParentStats: CgroupStats{
					CPUThrottledCount:    50,
					CPUThrottledDuration: 0.001,
					MemoryUsage:          1234000000,
					MemoryLimit:          2000000000,
					OOMKills:             5,
				},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			mock := newMock(t, tc.version)

			config := defaultCgroupsConfig()
			config.Repositories.Count = 1
			config.Repositories.MemoryBytes = 2000000000
			config.Repositories.CPUShares = 16
			config.Mountpoint = mock.rootPath()

			manager := mock.newCgroupManager(config, testhelper.SharedLogger(t), 1)

			mock.setupMockCgroupFiles(t, manager, []uint{0}, tc.mockFiles...)
			require.NoError(t, manager.Setup())

			stats, err := manager.Stats()
			require.NoError(t, err)
			require.Equal(t, tc.expectedStats, stats)
		})
	}
}

func requireCgroupComponents(t *testing.T, version int, root string, cgroupPath string, expected expectedCgroup) {
	t.Helper()

	if version == 1 {
		memoryLimitPath := filepath.Join(root, "memory", cgroupPath, "memory.limit_in_bytes")
		requireCgroupWithInt(t, memoryLimitPath, expected.wantMemoryBytes)

		cpuSharesPath := filepath.Join(root, "cpu", cgroupPath, "cpu.shares")
		requireCgroupWithInt(t, cpuSharesPath, expected.wantCPUShares)

		cpuCFSQuotaPath := filepath.Join(root, "cpu", cgroupPath, "cpu.cfs_quota_us")
		requireCgroupWithInt(t, cpuCFSQuotaPath, expected.wantCPUQuotaUs)

		cpuCFSPeriodPath := filepath.Join(root, "cpu", cgroupPath, "cpu.cfs_period_us")
		requireCgroupWithInt(t, cpuCFSPeriodPath, expected.wantCFSPeriod)
	} else {
		memoryMaxPath := filepath.Join(root, cgroupPath, "memory.max")
		requireCgroupWithInt(t, memoryMaxPath, expected.wantMemoryBytes)

		cpuWeightPath := filepath.Join(root, cgroupPath, "cpu.weight")
		requireCgroupWithInt(t, cpuWeightPath, calculateWantCPUWeight(expected.wantCPUWeight))

		cpuMaxPath := filepath.Join(root, cgroupPath, "cpu.max")
		requireCgroupWithString(t, cpuMaxPath, expected.wantCPUMax)
	}
}

func requireShards(t *testing.T, version int, mock mockCgroup, mgr *CGroupManager, pid int, expectedShards ...uint) {
	for shard := uint(0); shard < mgr.cfg.Repositories.Count; shard++ {
		shouldExist := slices.Contains(expectedShards, shard)

		cgroupPath := filepath.Join("gitaly",
			fmt.Sprintf("gitaly-%d", pid), fmt.Sprintf("repos-%d", shard))
		cgLock := mgr.status.getLock(cgroupPath)
		require.Equal(t, shouldExist, cgLock.isCreated())

		for _, diskPath := range mock.repoPaths(pid, shard) {
			if shouldExist {
				require.DirExists(t, diskPath)
			} else {
				require.NoDirExists(t, diskPath)
			}
		}
	}
}
