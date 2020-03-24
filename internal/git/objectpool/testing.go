package objectpool

import (
	"path/filepath"

	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

// NewObjectPoolName returns a random pool repository name
// in format '@pools/[0-9a-z]{2}/[0-9a-z]{2}/[0-9a-z]{64}.git'.
func NewObjectPoolName(t testhelper.TB) string {
	return filepath.Join("@pools", testhelper.NewDiskHash(t))
}
