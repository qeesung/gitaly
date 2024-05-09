package gittest

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
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

// BackendSpecificRepoHash is a helper function which can be
// used to check the voting hash of newly created repo's.
//
// Closely mimics the behavior for hashing in repoutil.Create.
// See the same for documentation and reasoning.
func BackendSpecificRepoHash(t *testing.T, ctx context.Context,
	cfg config.Cfg, hash voting.VoteHash, repoPath string,
) {
	t.Helper()

	if DefaultReferenceBackend == git.ReferenceBackendReftables {
		SkipIfGitVersionLessThan(t, ctx, cfg, git.NewVersion(2, 45, 0, 0), "reftable support is added in 2.45")
	}

	err := filepath.WalkDir(repoPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		switch path {
		case filepath.Join(repoPath, "objects"):
			return fs.SkipDir
		case filepath.Join(repoPath, "FETCH_HEAD"):
			return nil
		case filepath.Join(repoPath, "refs"):
			if DefaultReferenceBackend == git.ReferenceBackendReftables {
				return fs.SkipDir
			}
		case filepath.Join(repoPath, "reftable"):
			if DefaultReferenceBackend == git.ReferenceBackendReftables {
				refs := Exec(t, cfg, "-C", repoPath, "for-each-ref",
					"--format=%(refname) %(objectname) %(symref)",
					"--include-root-refs",
				)
				hash.Write(refs)

				return fs.SkipDir
			}
		}

		if entry.IsDir() {
			return nil
		}

		file, err := os.Open(path)
		require.NoError(t, err)
		defer file.Close()

		_, err = io.Copy(hash, file)
		require.NoError(t, err)

		return nil
	})
	require.NoError(t, err)
}
