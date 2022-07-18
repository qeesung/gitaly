//go:build !gitaly_test_sha256

package gittest

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
)

func TestResolveRevision(t *testing.T) {
	cfg, _, repoPath := setup(t)

	require.Equal(t,
		git.ObjectID("1e292f8fedd741b75372e19097c76d327140c312"),
		ResolveRevision(t, cfg, repoPath, "refs/heads/master"),
	)
}
