package housekeeping

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func testRepoAndPool(t *testing.T, desc string, testFunc func(t *testing.T, relativePath string)) {
	t.Helper()
	t.Run(desc, func(t *testing.T) {
		t.Run("normal repository", func(t *testing.T) {
			testFunc(t, gittest.NewRepositoryName(t, true))
		})

		t.Run("object pool", func(t *testing.T) {
			testFunc(t, gittest.NewObjectPoolName(t))
		})
	})
}
