package gittest

import (
	"strings"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
)

// RemoteExists tests if the repository at repoPath has a Git remote named remoteName.
func RemoteExists(tb testing.TB, cfg config.Cfg, repoPath string, remoteName string) bool {
	if remoteName == "" {
		tb.Fatal("empty remote name")
	}

	remotes := Exec(tb, cfg, "-C", repoPath, "remote")
	for _, r := range strings.Split(string(remotes), "\n") {
		if r == remoteName {
			return true
		}
	}

	return false
}
