//go:build static && system_libgit2
// +build static,system_libgit2

package main

import (
	"testing"
	"time"

	git "github.com/libgit2/git2go/v33"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
)

// DefaultAuthor is the author used by BuildCommit
var DefaultAuthor = git.Signature{
	Name:  "Scrooge McDuck",
	Email: "scrooge@mcduck.com",
	When:  time.Date(2019, 11, 3, 11, 27, 59, 0, time.FixedZone("", 60*60)),
}

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

func buildExecutor(tb testing.TB, cfg config.Cfg) *git2go.Executor {
	return git2go.NewExecutor(cfg, gittest.NewCommandFactory(tb, cfg), config.NewLocator(cfg))
}
