package testhelper

import "testing"

// CreateRemoteBranch creates a new remote branch
func CreateRemoteBranch(t testing.TB, repoPath, remoteName, branchName, ref string) {
	MustRunCommand(t, nil, "git", "-C", repoPath, "update-ref",
		"refs/remotes/"+remoteName+"/"+branchName, ref)
}
