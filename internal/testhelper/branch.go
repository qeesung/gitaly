package testhelper

// CreateRemoteBranch creates a new remote branch
func CreateRemoteBranch(t TB, repoPath, remoteName, branchName, ref string) {
	MustRunCommand(t, nil, "git", "-C", repoPath, "update-ref",
		"refs/remotes/"+remoteName+"/"+branchName, ref)
}
