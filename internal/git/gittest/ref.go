package gittest

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
)

// WriteRef writes a reference into the repository pointing to the given object ID.
func WriteRef(tb testing.TB, cfg config.Cfg, repoPath string, ref git.ReferenceName, oid git.ObjectID) {
	Exec(tb, cfg, "-C", repoPath, "update-ref", ref.String(), oid.String())
}
