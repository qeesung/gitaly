//go:build static && system_libgit2
// +build static,system_libgit2

package main

import (
	"testing"

	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}
