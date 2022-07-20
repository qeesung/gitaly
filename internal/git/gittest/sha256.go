//go:build gitaly_test_sha256

package gittest

import (
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
)

var (
	// DefaultObjectHash is the default hash used for running tests.
	DefaultObjectHash = git.ObjectHashSHA256

	initRepoExtraArgs = []string{"--object-format=sha256"}
)
