//go:build static && system_libgit2

package git2go

import (
	git "github.com/libgit2/git2go/v33"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
)

var defaultAuthor = git.Signature{
	Name:  gittest.DefaultCommitterName,
	Email: gittest.DefaultCommitterMail,
	When:  gittest.DefaultCommitTime,
}
