package gittest

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
)

func TestResolveRevision(t *testing.T) {
	cfg, _, repoPath := setup(t)

	commitID := WriteCommit(t, cfg, repoPath, WithBranch(git.DefaultBranch))

	require.Equal(t, commitID, ResolveRevision(t, cfg, repoPath, git.DefaultBranch))
}
