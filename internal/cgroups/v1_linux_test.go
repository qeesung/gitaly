package cgroups

import (
	"bytes"
	"fmt"
	"hash/crc32"
	"os"
	"os/exec"
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
	"gitlab.com/gitlab-org/gitaly/internal/command"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config/cgroups"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func defaultCgroupsConfig() cgroups.Config {
	return cgroups.Config{
		Count:         3,
		HierarchyRoot: "gitaly",
		CPU: cgroups.CPU{
			Enabled: true,
			Shares:  256,
		},
		Memory: cgroups.Memory{
			Enabled: true,
			Limit:   1024000,
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
			mock.root, "memory", "gitaly", fmt.Sprintf("gitaly-%d", os.Getpid()), fmt.Sprintf("shard-%d", i), "memory.limit_in_bytes",
		)
		memoryContent := readCgroupFile(t, memoryPath)

		require.Equal(t, string(memoryContent), "1024000")

		cpuPath := filepath.Join(
			mock.root, "cpu", "gitaly", fmt.Sprintf("gitaly-%d", os.Getpid()), fmt.Sprintf("shard-%d", i), "cpu.shares",
		)
		cpuContent := readCgroupFile(t, cpuPath)

		require.Equal(t, string(cpuContent), "256")
	}
}

func TestAddCommand(t *testing.T) {
	mock := newMock(t)

	config := defaultCgroupsConfig()
	v1Manager1 := &CGroupV1Manager{
		cfg:       config,
		hierarchy: mock.hierarchy,
	}
	require.NoError(t, v1Manager1.Setup())
	ctx := testhelper.Context(t)

	cmd1 := exec.Command("ls", "-hal", ".")
	cmd2, err := command.New(ctx, cmd1, nil, nil, nil)
	require.NoError(t, err)
	require.NoError(t, cmd2.Wait())

	v1Manager2 := &CGroupV1Manager{
		cfg:       config,
		hierarchy: mock.hierarchy,
	}
	require.NoError(t, v1Manager2.AddCommand(cmd2))

	checksum := crc32.ChecksumIEEE([]byte(strings.Join(cmd2.Args(), "")))
	// nolint:staticcheck // we will deprecate the old cgroups config in 15.0
	groupID := uint(checksum) % config.Count

	for _, s := range mock.subsystems {
		path := filepath.Join(mock.root, string(s.Name()), "gitaly", fmt.Sprintf("gitaly-%d", os.Getpid()), fmt.Sprintf("shard-%d", groupID), "cgroup.procs")
		content := readCgroupFile(t, path)

		pid, err := strconv.Atoi(string(content))
		require.NoError(t, err)

		require.Equal(t, cmd2.Pid(), pid)
	}
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
		memoryPath := filepath.Join(mock.root, "memory", "gitaly", fmt.Sprintf("gitaly-%d", os.Getpid()), fmt.Sprintf("shard-%d", i))
		cpuPath := filepath.Join(mock.root, "cpu", "gitaly", fmt.Sprintf("gitaly-%d", os.Getpid()), fmt.Sprintf("shard-%d", i))

		require.NoDirExists(t, memoryPath)
		require.NoDirExists(t, cpuPath)
	}
}

func TestMetrics(t *testing.T) {
	mock := newMock(t)

	config := defaultCgroupsConfig()
	v1Manager1 := newV1Manager(config)
	v1Manager1.hierarchy = mock.hierarchy

	mock.setupMockCgroupFiles(t, v1Manager1, 2)

	require.NoError(t, v1Manager1.Setup())
	ctx := testhelper.Context(t)

	logger, hook := test.NewNullLogger()
	logger.SetLevel(logrus.DebugLevel)
	ctx = ctxlogrus.ToContext(ctx, logrus.NewEntry(logger))

	cmd1 := exec.Command("ls", "-hal", ".")
	cmd2, err := command.New(ctx, cmd1, nil, nil, nil)
	require.NoError(t, err)

	require.NoError(t, v1Manager1.AddCommand(cmd2))
	require.NoError(t, cmd2.Wait())

	processCgroupPath := v1Manager1.currentProcessCgroup()

	expected := bytes.NewBufferString(fmt.Sprintf(`# HELP gitaly_cgroup_cpu_usage CPU Usage of Cgroup
# TYPE gitaly_cgroup_cpu_usage gauge
gitaly_cgroup_cpu_usage{path="%s",type="kernel"} 0
gitaly_cgroup_cpu_usage{path="%s",type="user"} 0
# HELP gitaly_cgroup_memory_failed_total Number of memory usage hits limits
# TYPE gitaly_cgroup_memory_failed_total gauge
gitaly_cgroup_memory_failed_total{path="%s"} 2
# HELP gitaly_cgroup_procs_total Total number of procs
# TYPE gitaly_cgroup_procs_total gauge
gitaly_cgroup_procs_total{path="%s",subsystem="memory"} 1
gitaly_cgroup_procs_total{path="%s",subsystem="cpu"} 1
`, processCgroupPath, processCgroupPath, processCgroupPath, processCgroupPath, processCgroupPath))
	assert.NoError(t, testutil.CollectAndCompare(
		v1Manager1,
		expected,
		"gitaly_cgroup_memory_failed_total",
		"gitaly_cgroup_cpu_usage",
		"gitaly_cgroup_procs_total"))

	logEntry := hook.LastEntry()
	assert.Contains(
		t,
		logEntry.Data["command.cgroup_path"],
		processCgroupPath,
		"log field includes a cgroup path that is a subdirectory of the current process' cgroup path",
	)
}

func readCgroupFile(t *testing.T, path string) []byte {
	t.Helper()

	// The cgroups package defaults to permission 0 as it expects the file to be existing (the kernel creates the file)
	// and its testing override the permission private variable to something sensible, hence we have to chmod ourselves
	// so we can read the file.
	require.NoError(t, os.Chmod(path, 0o666))

	return testhelper.MustReadFile(t, path)
}
