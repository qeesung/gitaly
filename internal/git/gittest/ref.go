package gittest

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
)

// WriteRef writes a reference into the repository pointing to the given object ID.
func WriteRef(t testing.TB, cfg config.Cfg, repoPath string, ref git.ReferenceName, oid git.ObjectID) {
	Exec(t, cfg, "-C", repoPath, "update-ref", ref.String(), oid.String())
}

// ResolveRevision resolves the revision to an object ID.
func ResolveRevision(t testing.TB, cfg config.Cfg, repoPath string, revision string) git.ObjectID {
	t.Helper()
	output := Exec(t, cfg, "-C", repoPath, "rev-parse", "--verify", revision)
	objectID, err := DefaultObjectHash.FromHex(text.ChompBytes(output))
	require.NoError(t, err)
	return objectID
}
