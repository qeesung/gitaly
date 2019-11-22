package main

import (
	"os"
	"path"
	"testing"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

var gitalySSHPath string

func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	defer testhelper.MustHaveNoChildProcess()

	testhelper.ConfigureGitalySSH()
	gitalySSHPath = path.Join(config.Config.BinDir, "gitaly-ssh")

	return m.Run()
}
