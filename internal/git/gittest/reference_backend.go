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

// FilesOrReftables returns the files or reftable value based on which reference
// backend is currently being used.
func FilesOrReftables[T any](files, reftable T) T {
	if DefaultReferenceBackend == git.ReferenceBackendFiles {
		return files
	}
	return reftable
}
