//go:build linux

/*
   Adapted from https://github.com/containerd/cgroups/blob/f1d9380fd3c028194db9582825512fdf3f39ab2a/mock_test.go

   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package cgroups

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	cgrps "github.com/containerd/cgroups/v3"
	"github.com/containerd/cgroups/v3/cgroup1"
	"github.com/stretchr/testify/require"
	cgroupscfg "gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config/cgroups"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

type mockCgroup interface {
	setupMockCgroupFiles(
		t *testing.T,
		manager *CGroupManager,
		shards []uint,
		inputContent ...mockCgroupFile,
	)
	newCgroupManager(cfg cgroupscfg.Config, logger log.Logger, pid int) *CGroupManager
	pruneOldCgroups(cfg cgroupscfg.Config, logger log.Logger)
	// rootPath returns the mock's root directory.
	rootPath() string
	// repoPaths returns the full disk path for each subcomponent, e.g. memory, cpu, of
	// a repository cgroup. On v2 this is a single path.
	repoPaths(pid int, repoID uint) []string
	// version returns the cgroup version number, 1 or 2.
	version() int
}

func newMock(t *testing.T, version int) mockCgroup {
	var mock mockCgroup
	if version == 1 {
		mock = newMockV1(t)
	} else {
		mock = newMockV2(t)
	}

	return mock
}

type mockCgroupV1 struct {
	root       string
	subsystems []cgroup1.Subsystem
}

func newMockV1(t *testing.T) *mockCgroupV1 {
	t.Helper()

	root := testhelper.TempDir(t)

	subsystems, err := defaultSubsystems(root)
	require.NoError(t, err)

	for _, s := range subsystems {
		require.NoError(t, os.MkdirAll(filepath.Join(root, string(s.Name())), perm.PrivateDir))
	}

	return &mockCgroupV1{
		root:       root,
		subsystems: subsystems,
	}
}

type mockCgroupFile struct {
	name    string
	content string
}

var defaultV1MockMemoryFiles = map[string]string{
	"cgroup.procs":                       "",
	"memory.stat":                        "",
	"memory.oom_control":                 "",
	"memory.usage_in_bytes":              "0",
	"memory.max_usage_in_bytes":          "0",
	"memory.limit_in_bytes":              "0",
	"memory.failcnt":                     "0",
	"memory.memsw.failcnt":               "0",
	"memory.memsw.usage_in_bytes":        "0",
	"memory.memsw.max_usage_in_bytes":    "0",
	"memory.memsw.limit_in_bytes":        "0",
	"memory.kmem.usage_in_bytes":         "0",
	"memory.kmem.max_usage_in_bytes":     "0",
	"memory.kmem.failcnt":                "0",
	"memory.kmem.limit_in_bytes":         "0",
	"memory.kmem.tcp.usage_in_bytes":     "0",
	"memory.kmem.tcp.max_usage_in_bytes": "0",
	"memory.kmem.tcp.failcnt":            "0",
	"memory.kmem.tcp.limit_in_bytes":     "0",
}

var defaultV1MockCPUFiles = map[string]string{
	"cgroup.procs": "",
	"cpu.shares":   "0",
	"cpu.stat": `nr_periods 10
nr_throttled 20
throttled_time 1000000`,
}

func (m *mockCgroupV1) setupMockCgroupFiles(
	t *testing.T,
	manager *CGroupManager,
	shards []uint,
	inputContent ...mockCgroupFile,
) {
	for _, s := range m.subsystems {
		cgroupPath := filepath.Join(m.root, string(s.Name()), manager.currentProcessCgroup())
		require.NoError(t, os.MkdirAll(cgroupPath, perm.PrivateDir))

		content := map[string]string{}
		for _, ic := range inputContent {
			content[ic.name] = ic.content
		}

		switch s.Name() {
		case "memory":
			for key, value := range defaultV1MockMemoryFiles {
				if _, exist := content[key]; !exist {
					content[key] = value
				}
			}
		case "cpu":
			for key, value := range defaultV1MockCPUFiles {
				if _, exist := content[key]; !exist {
					content[key] = value
				}
			}
		default:
			require.FailNow(t, "cannot set up subsystem", "unknown subsystem %q", s.Name())
		}

		for filename, content := range content {
			controlFilePath := filepath.Join(cgroupPath, filename)
			require.NoError(t, os.WriteFile(controlFilePath, []byte(content), perm.SharedFile))
		}

		for _, shard := range shards {
			shardPath := filepath.Join(cgroupPath, fmt.Sprintf("repos-%d", shard))
			require.NoError(t, os.MkdirAll(shardPath, perm.PrivateDir))

			for filename, content := range content {
				shardControlFilePath := filepath.Join(shardPath, filename)
				require.NoError(t, os.WriteFile(shardControlFilePath, []byte(content), perm.SharedFile))
			}
		}
	}
}

func (m *mockCgroupV1) newCgroupManager(cfg cgroupscfg.Config, logger log.Logger, pid int) *CGroupManager {
	return newCgroupManagerWithMode(cfg, logger, pid, cgrps.Legacy)
}

func (m *mockCgroupV1) pruneOldCgroups(cfg cgroupscfg.Config, logger log.Logger) {
	pruneOldCgroupsWithMode(cfg, logger, cgrps.Legacy)
}

func (m *mockCgroupV1) rootPath() string {
	return m.root
}

func (m *mockCgroupV1) repoPaths(pid int, repoID uint) []string {
	paths := make([]string, 0, len(m.subsystems))

	for _, s := range m.subsystems {
		path := filepath.Join(m.root, string(s.Name()), "gitaly",
			fmt.Sprintf("gitaly-%d", pid), fmt.Sprintf("repos-%d", repoID))
		paths = append(paths, path)
	}

	return paths
}

func (m *mockCgroupV1) version() int {
	return 1
}

type mockCgroupV2 struct {
	root string
}

func newMockV2(t *testing.T) *mockCgroupV2 {
	t.Helper()

	return &mockCgroupV2{
		root: testhelper.TempDir(t),
	}
}

var defaultV2MockFiles = map[string]string{
	"cgroup.procs":           "",
	"cgroup.subtree_control": "cpu cpuset memory",
	"cgroup.controllers":     "cpu cpuset memory",
	"cpu.max":                "max 100000",
	"cpu.weight":             "10",
	"memory.max":             "max",
	"cpu.stat": `nr_periods 10
	nr_throttled 20
	throttled_usec 1000000`,
}

func (m *mockCgroupV2) setupMockCgroupFiles(
	t *testing.T,
	manager *CGroupManager,
	shards []uint,
	inputContent ...mockCgroupFile,
) {
	content := map[string]string{}
	for _, ic := range inputContent {
		content[ic.name] = ic.content
	}
	for key, value := range defaultV2MockFiles {
		if _, exist := content[key]; !exist {
			content[key] = value
		}
	}

	cgroupPath := filepath.Join(m.root, manager.currentProcessCgroup())
	require.NoError(t, os.MkdirAll(cgroupPath, perm.PrivateDir))

	for filename, content := range content {
		controlFilePath := filepath.Join(m.root, manager.cfg.HierarchyRoot, filename)
		require.NoError(t, os.WriteFile(controlFilePath, []byte(content), perm.SharedFile))
	}

	for filename, content := range content {
		controlFilePath := filepath.Join(cgroupPath, filename)
		require.NoError(t, os.WriteFile(controlFilePath, []byte(content), perm.SharedFile))
	}

	for _, shard := range shards {
		shardPath := filepath.Join(cgroupPath, fmt.Sprintf("repos-%d", shard))
		require.NoError(t, os.MkdirAll(shardPath, perm.PrivateDir))

		for filename, content := range content {
			shardControlFilePath := filepath.Join(shardPath, filename)
			require.NoError(t, os.WriteFile(shardControlFilePath, []byte(content), perm.SharedFile))
		}
	}
}

func (m *mockCgroupV2) newCgroupManager(cfg cgroupscfg.Config, logger log.Logger, pid int) *CGroupManager {
	return newCgroupManagerWithMode(cfg, logger, pid, cgrps.Unified)
}

func (m *mockCgroupV2) pruneOldCgroups(cfg cgroupscfg.Config, logger log.Logger) {
	pruneOldCgroupsWithMode(cfg, logger, cgrps.Unified)
}

func (m *mockCgroupV2) rootPath() string {
	return m.root
}

func (m *mockCgroupV2) repoPaths(pid int, repoID uint) []string {
	path := filepath.Join(m.root, "gitaly",
		fmt.Sprintf("gitaly-%d", pid), fmt.Sprintf("repos-%d", repoID))

	return []string{path}
}

func (m *mockCgroupV2) version() int {
	return 2
}
