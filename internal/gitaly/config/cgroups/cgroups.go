package cgroups

// Config is a struct for cgroups config
type Config struct {
	// Mountpoint is where the cgroup filesystem is mounted, usually under /sys/fs/cgroup/
	Mountpoint string `toml:"mountpoint"`
	// HierarchyRoot is the parent cgroup under which Gitaly creates <Count> of cgroups.
	// A system administrator is expected to create such cgroup/directory under <Mountpoint>/memory
	// and/or <Mountpoint>/cpu depending on which resource is enabled. HierarchyRoot is expected to
	// be owned by the user and group Gitaly runs as.
	HierarchyRoot string       `toml:"hierarchy_root"`
	Repositories  Repositories `toml:"repositories"`
	// MemoryBytes is the memory limit for the parent cgroup. 0 implies no memory limit.
	MemoryBytes int64 `toml:"memory_bytes"`
	// CPUShares are the shares of CPU the parent cgroup is allowed to utilize. A value of 1024
	// is full utilization of the CPU. 0 implies no CPU limit.
	CPUShares      uint64 `toml:"cpu_shares"`
	MetricsEnabled bool   `toml:"metrics_enabled"`

	// Deprecated: No longer supported after 15.0
	Count  uint   `toml:"count"`
	CPU    CPU    `toml:"cpu"`
	Memory Memory `toml:"memory"`
}

// FallbackToOldVersion translates the old format of cgroups into the new
// format.
func (c *Config) FallbackToOldVersion() {
	if c.Repositories.Count == 0 {
		c.Repositories.Count = c.Count

		if c.Repositories.MemoryBytes == 0 && c.Memory.Enabled {
			c.Repositories.MemoryBytes = c.Memory.Limit
		}

		if c.Repositories.CPUShares == 0 && c.CPU.Enabled {
			c.Repositories.CPUShares = c.CPU.Shares
		}
	}
}

// Repositories configures cgroups to be created that are isolated by repository.
type Repositories struct {
	// Count is the number of cgroups that will be created for repository-level isolation
	// of git commands.
	Count uint `toml:"count"`
	// MemoryBytes is the memory limit for each cgroup. 0 implies no memory limit.
	MemoryBytes int64 `toml:"memory_bytes"`
	// CPUShares are the shares of CPU that each cgroup is allowed to utilize. A value of 1024
	// is full utilization of the CPU. 0 implies no CPU limit.
	CPUShares uint64 `toml:"cpu_shares"`
}

// Memory is a struct storing cgroups memory config
// Deprecated: Not in use after 15.0.
type Memory struct {
	Enabled bool `toml:"enabled"`
	// Limit is the memory limit in bytes. Could be -1 to indicate unlimited memory.
	Limit int64 `toml:"limit"`
}

// CPU is a struct storing cgroups CPU config
// Deprecated: Not in use after 15.0.
type CPU struct {
	Enabled bool `toml:"enabled"`
	// Shares is the number of CPU shares (relative weight (ratio) vs. other cgroups with CPU shares).
	Shares uint64 `toml:"shares"`
}
