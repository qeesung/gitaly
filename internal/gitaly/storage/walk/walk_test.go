package walk

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestWalkStorage(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		repos      []string
		breakRepos bool
		files      []string
	}{
		{
			name:  "repos found",
			repos: []string{"@hashed/aa/bb/repo-1.git", "@hashed/01/23/repo-2.git"},
		},
		{
			name:       "invalid repos skipped",
			repos:      []string{"@hashed/aa/bb/repo-1.git", "@hashed/01/23/repo-2.git"},
			breakRepos: true,
		},
		{
			name:  "files ignored",
			repos: []string{"@hashed/aa/bb/aabb.git", "@cluster/baz/45/67/89ab"},
			files: []string{"@cluster/bar/01/23/a_file"},
		},
	} {
		tc := tc

		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)
			cfg := testcfg.Build(t)
			storage := cfg.Storages[0]

			var repoPaths []string
			for _, path := range tc.repos {
				_, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
					RelativePath:           path,
				})

				if tc.breakRepos {
					// Repositories without an 'objects' directory are invalid.
					objPath := filepath.Join(storage.Path, path, "objects")
					require.NoError(t, os.RemoveAll(objPath))

					// Don't expect repo path to be found.
					continue
				}

				relPath, err := filepath.Rel(storage.Path, repoPath)
				require.NoError(t, err)
				repoPaths = append(repoPaths, relPath)
			}

			for _, file := range tc.files {
				fullPath := filepath.Join(storage.Path, file)

				dir := filepath.Dir(fullPath)
				err := os.MkdirAll(dir, perm.PrivateDir)
				require.NoError(t, err)

				f, err := os.OpenFile(fullPath, os.O_WRONLY|os.O_CREATE, perm.PrivateWriteOnceFile)
				require.NoError(t, err)
				require.NoError(t, f.Close())
			}

			var foundPaths []string
			repoAction := func(relPath string, gitDirInfo fs.FileInfo) error {
				foundPaths = append(foundPaths, relPath)
				return nil
			}

			locator := config.NewLocator(cfg)
			require.NoError(t, FindRepositories(ctx, locator, storage.Name, repoAction))

			require.ElementsMatch(t, foundPaths, repoPaths)
		})
	}
}
