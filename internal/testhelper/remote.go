package testhelper

import (
	"strings"
	"testing"
)

// RemoteExists tests if the repository at repoPath has are Git remote named remote.
func RemoteExists(t *testing.T, repoPath string, remote string) bool {
	if len(remote) == 0 {
		t.Fatalf("empty remote name")
	}

	remotes := MustRunCommand(t, nil, "git", "-C", repoPath, "remote")

	for _, r := range strings.Split(string(remotes), "\n") {
		if r == remote {
			return true
		}
	}

	return false
}
