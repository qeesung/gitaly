package testhelper

import (
	"strings"
	"testing"
)

// CreateCommit makes a new empty commit and updates the named branch to point to it.
func CreateCommit(t *testing.T, repoPath string, branchName string) string {
	// The ID of an arbitrary commit known to exist in the test repository.
	parentID := "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863"

	// Use 'commit-tree' instead of 'commit' because we are in a bare
	// repository. What we do here is the same as "commit -m message
	// --allow-empty".
	commitArgs := []string{"-C", repoPath, "commit-tree", "-m", "message", "-p", parentID, parentID + "^{tree}"}
	newCommit := MustRunCommand(t, nil, "git", commitArgs...)
	newCommitID := strings.TrimSpace(string(newCommit))

	MustRunCommand(t, nil, "git", "-C", repoPath, "update-ref", "refs/heads/"+branchName, newCommitID)
	return newCommitID
}
