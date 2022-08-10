//go:build !gitaly_test_sha256

package cgroups

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus/ctxlogrus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/command"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config/cgroups"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func defaultCgroupsConfig() cgroups.Config {
	return cgroups.Config{
		HierarchyRoot: "gitaly",
		Repositories: cgroups.Repositories{
			Count:       3,
			MemoryBytes: 1024000,
			CPUShares:   256,
		},
	}
}

func TestSetup(t *testing.T) {
	mock := newMock(t)

	v1Manager := &CGroupV1Manager{
		cfg:       defaultCgroupsConfig(),
		hierarchy: mock.hierarchy,
	}
	require.NoError(t, v1Manager.Setup())

	for i := 0; i < 3; i++ {
		memoryPath := filepath.Join(
			mock.root, "memory", "gitaly", fmt.Sprintf("repos-%d", i), "memory.limit_in_bytes",
		)
		memoryContent := readCgroupFile(t, memoryPath)

		require.Equal(t, string(memoryContent), "1024000")

		cpuPath := filepath.Join(
			mock.root, "cpu", "gitaly", fmt.Sprintf("repos-%d", i), "cpu.shares",
		)
		cpuContent := readCgroupFile(t, cpuPath)

		require.Equal(t, string(cpuContent), "256")
	}
}

func TestSetup_multiple(t *testing.T) {
	mock := newMock(t)
	cfg := defaultCgroupsConfig()

	v1Manager := &CGroupV1Manager{
		cfg:       cfg,
		hierarchy: mock.hierarchy,
	}
	mock.setupMockCgroupFiles(t, v1Manager, 0)
	require.NoError(t, v1Manager.Setup())

	// Set up a set of cgroups. This is meant to emulate a left over group of cgroups of a
	// Gitaly process during a graceful restart.

	for i := 0; i < 3; i++ {
		memoryPath := filepath.Join(
			mock.root, "memory", "gitaly", fmt.Sprintf("repos-%d", i), "memory.limit_in_bytes",
		)
		memoryContent := readCgroupFile(t, memoryPath)

		require.Equal(t, string(memoryContent), "1024000")

		cpuPath := filepath.Join(
			mock.root, "cpu", "gitaly", fmt.Sprintf("repos-%d", i), "cpu.shares",
		)
		cpuContent := readCgroupFile(t, cpuPath)

		require.Equal(t, string(cpuContent), "256")
	}

	updatedCfg := cfg
	updatedCfg.Repositories.Count = cfg.Repositories.Count + 5
	updatedCfg.Repositories.MemoryBytes = cfg.Repositories.MemoryBytes + 10
	updatedCfg.Repositories.CPUShares = cfg.Repositories.CPUShares + 10

	secondManager := &CGroupV1Manager{
		cfg:       updatedCfg,
		hierarchy: mock.hierarchy,
	}

	mock.setupMockCgroupFiles(t, secondManager, 0)

	// By starting cgroups with an updated config that has more cgroups with higher limits,
	// ensure that it successfully adds more cgroups as well as update the limits of the
	// existing cgroups.
	require.NoError(t, secondManager.Setup())

	// Verify that it updates all the cgroups.
	for i := 0; i < int(updatedCfg.Repositories.Count); i++ {
		memoryPath := filepath.Join(
			mock.root, "memory", "gitaly", fmt.Sprintf("repos-%d", i), "memory.limit_in_bytes",
		)
		memoryContent := readCgroupFile(t, memoryPath)

		require.Equal(t, string(memoryContent), strconv.Itoa(int(updatedCfg.Repositories.MemoryBytes)))

		cpuPath := filepath.Join(
			mock.root, "cpu", "gitaly", fmt.Sprintf("repos-%d", i), "cpu.shares",
		)
		cpuContent := readCgroupFile(t, cpuPath)

		require.Equal(t, string(cpuContent), strconv.Itoa(int(updatedCfg.Repositories.CPUShares)))
	}
}

func TestAddCommand(t *testing.T) {
	mock := newMock(t)

	repo := &gitalypb.Repository{
		StorageName:  "default",
		RelativePath: "path/to/repo.git",
	}

	config := defaultCgroupsConfig()
	config.Repositories.Count = 10
	config.Repositories.MemoryBytes = 1024
	config.Repositories.CPUShares = 16

	v1Manager1 := &CGroupV1Manager{
		cfg:       config,
		hierarchy: mock.hierarchy,
	}
	require.NoError(t, v1Manager1.Setup())
	ctx := testhelper.Context(t)

	cmd2, err := command.New(ctx, []string{"ls", "-hal", "."})
	require.NoError(t, err)
	require.NoError(t, cmd2.Wait())

	v1Manager2 := &CGroupV1Manager{
		cfg:       config,
		hierarchy: mock.hierarchy,
	}

	t.Run("without a repository", func(t *testing.T) {
		_, err := v1Manager2.AddCommand(cmd2, nil)
		require.NoError(t, err)

		checksum := crc32.ChecksumIEEE([]byte(strings.Join(cmd2.Args(), "/")))
		groupID := uint(checksum) % config.Repositories.Count

		for _, s := range mock.subsystems {
			path := filepath.Join(mock.root, string(s.Name()), "gitaly",
				fmt.Sprintf("repos-%d", groupID), "cgroup.procs")
			content := readCgroupFile(t, path)

			pid, err := strconv.Atoi(string(content))
			require.NoError(t, err)

			require.Equal(t, cmd2.Pid(), pid)
		}
	})

	t.Run("with a repository", func(t *testing.T) {
		_, err := v1Manager2.AddCommand(cmd2, repo)
		require.NoError(t, err)

		checksum := crc32.ChecksumIEEE([]byte(strings.Join([]string{
			"default",
			"path/to/repo.git",
		}, "/")))
		groupID := uint(checksum) % config.Repositories.Count

		for _, s := range mock.subsystems {
			path := filepath.Join(mock.root, string(s.Name()), "gitaly",
				fmt.Sprintf("repos-%d", groupID), "cgroup.procs")
			content := readCgroupFile(t, path)

			pid, err := strconv.Atoi(string(content))
			require.NoError(t, err)

			require.Equal(t, cmd2.Pid(), pid)
		}
	})
}

func TestCleanup(t *testing.T) {
	mock := newMock(t)

	v1Manager := &CGroupV1Manager{
		cfg:       defaultCgroupsConfig(),
		hierarchy: mock.hierarchy,
	}
	require.NoError(t, v1Manager.Setup())
	require.NoError(t, v1Manager.Cleanup())

	for i := 0; i < 3; i++ {
		memoryPath := filepath.Join(mock.root, "memory", "gitaly", fmt.Sprintf("gitaly-%d", os.Getpid()), fmt.Sprintf("repos-%d", i))
		cpuPath := filepath.Join(mock.root, "cpu", "gitaly", fmt.Sprintf("gitaly-%d", os.Getpid()), fmt.Sprintf("repos-%d", i))

		require.NoDirExists(t, memoryPath)
		require.NoDirExists(t, cpuPath)
	}
}

func TestCleanup_processesExist(t *testing.T) {
	mock := newMock(t)

	config := defaultCgroupsConfig()
	config.Repositories.Count = 10
	config.Repositories.MemoryBytes = 1024
	config.Repositories.CPUShares = 16
	config.Mountpoint = mock.root

	v1Manager1 := &CGroupV1Manager{
		cfg:       config,
		hierarchy: mock.hierarchy,
	}
	mock.setupMockCgroupFiles(t, v1Manager1, 2)
	require.NoError(t, v1Manager1.Setup())

	ctx := testhelper.Context(t)
	cmd1, err := command.New(ctx, []string{"ls", "-hal", "."})
	require.NoError(t, err)
	require.NoError(t, cmd1.Wait())

	_, err = v1Manager1.AddCommand(cmd1, nil)
	require.NoError(t, err)

	assert.Equal(t, ErrProcessesExist, v1Manager1.Cleanup())
}

func TestMetrics(t *testing.T) {
	t.Parallel()

	mock := newMock(t)
	repo := &gitalypb.Repository{
		StorageName:  "default",
		RelativePath: "path/to/repo.git",
	}

	config := defaultCgroupsConfig()
	config.Repositories.Count = 1
	config.Repositories.MemoryBytes = 1048576
	config.Repositories.CPUShares = 16

	v1Manager1 := newV1Manager(config)
	v1Manager1.hierarchy = mock.hierarchy

	mock.setupMockCgroupFiles(t, v1Manager1, 2)

	require.NoError(t, v1Manager1.Setup())

	ctx := testhelper.Context(t)
	logger, hook := test.NewNullLogger()
	logger.SetLevel(logrus.DebugLevel)
	ctx = ctxlogrus.ToContext(ctx, logrus.NewEntry(logger))

	cmd, err := command.New(ctx, []string{"ls", "-hal", "."}, command.WithCgroup(v1Manager1, repo))
	require.NoError(t, err)
	gitCmd1, err := command.New(ctx, []string{"ls", "-hal", "."}, command.WithCgroup(v1Manager1, repo))
	require.NoError(t, err)
	gitCmd2, err := command.New(ctx, []string{"ls", "-hal", "."}, command.WithCgroup(v1Manager1, repo))
	require.NoError(t, err)
	defer func() {
		require.NoError(t, gitCmd2.Wait())
	}()

	require.NoError(t, err)
	require.NoError(t, cmd.Wait())
	require.NoError(t, gitCmd1.Wait())

	repoCgroupPath := filepath.Join(config.HierarchyRoot, "repos-0")

	expected := bytes.NewBufferString(fmt.Sprintf(`# HELP gitaly_cgroup_cpu_usage_total CPU Usage of Cgroup
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
`, repoCgroupPath, repoCgroupPath, repoCgroupPath, repoCgroupPath, repoCgroupPath))

	for _, metricsEnabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("metrics enabled: %v", metricsEnabled), func(t *testing.T) {
			v1Manager1.cfg.MetricsEnabled = metricsEnabled

			if metricsEnabled {
				assert.NoError(t, testutil.CollectAndCompare(
					v1Manager1,
					expected))
			} else {
				assert.NoError(t, testutil.CollectAndCompare(
					v1Manager1,
					bytes.NewBufferString("")))
			}

			logEntry := hook.LastEntry()
			assert.Contains(
				t,
				logEntry.Data["command.cgroup_path"],
				repoCgroupPath,
				"log field includes a cgroup path that is a subdirectory of the current process' cgroup path",
			)
		})
	}
}

func readCgroupFile(t *testing.T, path string) []byte {
	t.Helper()

	// The cgroups package defaults to permission 0 as it expects the file to be existing (the kernel creates the file)
	// and its testing override the permission private variable to something sensible, hence we have to chmod ourselves
	// so we can read the file.
	require.NoError(t, os.Chmod(path, 0o666))

	return testhelper.MustReadFile(t, path)
}
