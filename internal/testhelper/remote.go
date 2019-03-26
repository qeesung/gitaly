package testhelper

import (
	"strings"
	"testing"
)

// RemoteExists tests if the repository at repoPath has are Git remote named remoteName.
func RemoteExists(t *testing.T, repoPath string, remoteName string) bool {
	if len(remoteName) == 0 {
		t.Fatal("empty remote name")
	}

	remotes := MustRunCommand(t, nil, "git", "-C", repoPath, "remote")
	for _, r := range strings.Split(string(remotes), "\n") {
		if r == remoteName {
			return true
		}
	}

	return false
}
