package gittest

import (
	"os"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
)

// DefaultReferenceBackend is the default reftable backend used for running tests.
var DefaultReferenceBackend = func() git.ReferenceBackend {
	if val, enabled := os.LookupEnv("GIT_DEFAULT_REF_FORMAT"); enabled && val == "reftable" {
		return git.ReferenceBackendReftables
	}

	return git.ReferenceBackendFiles
}()
