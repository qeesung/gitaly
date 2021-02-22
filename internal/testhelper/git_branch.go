package testhelper

import "testing"

// CreateRemoteBranch creates a new remote branch.
func (gs GitSetup) CreateRemoteBranch(t testing.TB, remoteName, branchName, ref string) {
	gs.requireRepoPath(t)
	gs.Exec(t, "update-ref", "refs/remotes/"+remoteName+"/"+branchName, ref)
}
