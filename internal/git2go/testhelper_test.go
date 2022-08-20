//go:build static && system_libgit2

package git2go

import (
	"testing"

	git "github.com/libgit2/git2go/v33"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

func TestMain(m *testing.M) {
	testhelper.Run(m, testhelper.WithSetup(func() error {
		// Ignore gitconfig while use libgit2's git_repository_open
		return git.SetSearchPath(git.ConfigLevelGlobal, "/dev/null")
	}))
}

var defaultAuthor = git.Signature{
	Name:  gittest.DefaultCommitterName,
	Email: gittest.DefaultCommitterMail,
	When:  gittest.DefaultCommitTime,
}
