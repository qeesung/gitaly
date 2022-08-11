package gittest

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
)

// WriteRef writes a reference into the repository pointing to the given object ID.
func WriteRef(tb testing.TB, cfg config.Cfg, repoPath string, ref git.ReferenceName, oid git.ObjectID) {
	Exec(tb, cfg, "-C", repoPath, "update-ref", ref.String(), oid.String())
}

// ResolveRevision resolves the revision to an object ID.
func ResolveRevision(tb testing.TB, cfg config.Cfg, repoPath string, revision string) git.ObjectID {
	tb.Helper()
	output := Exec(tb, cfg, "-C", repoPath, "rev-parse", "--verify", revision)
	objectID, err := DefaultObjectHash.FromHex(text.ChompBytes(output))
	require.NoError(tb, err)
	return objectID
}
