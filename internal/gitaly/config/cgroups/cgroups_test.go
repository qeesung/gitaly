//go:build !gitaly_test_sha256

package cgroups

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFallbackToOldVersion(t *testing.T) {
	testCases := []struct {
		desc         string
		configBefore Config
		configAfter  Config
	}{
		{
			desc:         "empty config",
			configBefore: Config{},
			configAfter:  Config{},
		},
		{
			desc: "new format",
			configBefore: Config{
				Mountpoint:    "some/mountpoint",
				HierarchyRoot: "gitaly",
				Repositories: Repositories{
					Count:       100,
					MemoryBytes: 1024,
					CPUShares:   16,
				},
			},
			configAfter: Config{
				Mountpoint:    "some/mountpoint",
				HierarchyRoot: "gitaly",
				Repositories: Repositories{
					Count:       100,
					MemoryBytes: 1024,
					CPUShares:   16,
				},
			},
		},
		{
			desc: "old format",
			configBefore: Config{
				Mountpoint:    "some/mountpoint",
				HierarchyRoot: "gitaly",
				Count:         100,
				Memory: Memory{
					Enabled: true,
					Limit:   1024,
				},
				CPU: CPU{
					Enabled: true,
					Shares:  16,
				},
			},
			configAfter: Config{
				Mountpoint:    "some/mountpoint",
				HierarchyRoot: "gitaly",
				Count:         100,
				Memory: Memory{
					Enabled: true,
					Limit:   1024,
				},
				CPU: CPU{
					Enabled: true,
					Shares:  16,
				},
				Repositories: Repositories{
					Count:       100,
					MemoryBytes: 1024,
					CPUShares:   16,
				},
			},
		},
		{
			desc: "old format, memory only",
			configBefore: Config{
				Mountpoint:    "some/mountpoint",
				HierarchyRoot: "gitaly",
				Count:         100,
				Memory: Memory{
					Enabled: true,
					Limit:   1024,
				},
				CPU: CPU{
					Enabled: false,
					Shares:  16,
				},
			},
			configAfter: Config{
				Mountpoint:    "some/mountpoint",
				HierarchyRoot: "gitaly",
				Count:         100,
				Memory: Memory{
					Enabled: true,
					Limit:   1024,
				},
				CPU: CPU{
					Enabled: false,
					Shares:  16,
				},

				Repositories: Repositories{
					Count:       100,
					MemoryBytes: 1024,
				},
			},
		},
		{
			desc: "old format, cpu only",
			configBefore: Config{
				Mountpoint:    "some/mountpoint",
				HierarchyRoot: "gitaly",
				Count:         100,
				Memory: Memory{
					Enabled: false,
					Limit:   1024,
				},
				CPU: CPU{
					Enabled: true,
					Shares:  16,
				},
			},
			configAfter: Config{
				Mountpoint:    "some/mountpoint",
				HierarchyRoot: "gitaly",
				Count:         100,
				Memory: Memory{
					Enabled: false,
					Limit:   1024,
				},
				CPU: CPU{
					Enabled: true,
					Shares:  16,
				},
				Repositories: Repositories{
					Count:     100,
					CPUShares: 16,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			tc.configBefore.FallbackToOldVersion()
			assert.Equal(t, tc.configAfter, tc.configBefore)
		})
	}
}
