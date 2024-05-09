package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFindPodCgroup(t *testing.T) {
	tests := []struct {
		name               string
		uid                string
		expectedCgroupPath string
		additionalCgroups  []string
	}{
		{
			name:               "no matching cgroup path",
			uid:                "test-uid",
			expectedCgroupPath: "",
		},
		{
			name:               "matching cgroup path exists",
			uid:                "123456",
			expectedCgroupPath: "kubepods-besteffort.slice/kubepods-besteffort-pod123456.slice",
		},
		{
			name:               "multiple cgroup paths",
			uid:                "123456",
			expectedCgroupPath: "kubepods-besteffort.slice/kubepods-besteffort-pod123456.slice",
			additionalCgroups:  []string{"kubepods-besteffort.slice/kubepods-besteffort-pod789012.slice"},
		},
		{
			name:               "multiple matching cgroup paths",
			uid:                "123456",
			expectedCgroupPath: "kubepods-besteffort.slice/kubepods-besteffort-pod123456.slice",
			additionalCgroups:  []string{"kubepods-besteffort.slice/kubepods-besteffort-pod123456.slice/cri-containerd-a69be83419aceb5000a9925552d67dc949e150c251b101357c242292579ed5d7.scope"},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tempDir, err := os.MkdirTemp("", "kubepods.slice")
			require.NoError(t, err)

			t.Cleanup(func() {
				require.NoError(t, os.RemoveAll(tempDir))
			})
			expectedPath := tt.expectedCgroupPath
			if tt.expectedCgroupPath != "" {
				expectedPath = filepath.Join(tempDir, tt.expectedCgroupPath)
				require.NoError(t, os.MkdirAll(expectedPath, 0o755))
			}

			for _, additionalCgroup := range tt.additionalCgroups {
				additionalPath := filepath.Join(tempDir, additionalCgroup)
				require.NoError(t, os.MkdirAll(additionalPath, 0o755))
			}

			cgroupPath := findPodCgroup(tempDir, tt.uid)
			require.Equal(t, expectedPath, cgroupPath)
		})
	}
}
