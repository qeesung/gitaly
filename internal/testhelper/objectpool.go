package testhelper

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly-proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/internal/git/objectpool"
)

// NewTestObjectPool creates a new object pool
func NewTestObjectPool(t *testing.T) (*objectpool.ObjectPool, *gitalypb.Repository) {
	repo, _, relativePath := CreateRepo(t, GitlabTestStoragePath())

	pool, err := objectpool.NewObjectPool("default", relativePath)
	require.NoError(t, err)

	return pool, repo
}
