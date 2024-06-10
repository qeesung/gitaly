package localrepo

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/archive"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestCreateSnapshot(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	equalError := func(tb testing.TB, expected error) func(error) {
		return func(actual error) {
			tb.Helper()
			testhelper.RequireGrpcError(tb, expected, actual)
		}
	}

	type setupData struct {
		repo            *Repo
		expectedEntries []string
		requireError    func(error)
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "repository contains symlink",
			setup: func(t *testing.T) setupData {
				testhelper.SkipWithWAL(t, `
The repositories generally shouldn't have symlinks in them and the TransactionManager never writes any
symlinks. Symlinks are not supported when creating a snapshot of the repository. Disable the test as it
doesn't seem to test a realistic scenario.`)

				_, repo, repoPath := setupRepo(t)

				// Make packed-refs into a symlink so the RPC returns an error.
				packedRefsFile := filepath.Join(repoPath, "packed-refs")
				require.NoError(t, os.Symlink("HEAD", packedRefsFile))

				return setupData{
					repo: repo,
					requireError: equalError(t, structerr.NewInternal(
						"building snapshot failed: open %s: too many levels of symbolic links",
						packedRefsFile,
					)),
				}
			},
		},
		{
			desc: "repository snapshot success",
			setup: func(t *testing.T) setupData {
				cfg, repo, repoPath := setupRepo(t)

				// Write commit and perform `git-repack` to generate a packfile and index in the
				// repository.
				treeID := gittest.WriteTree(t, cfg, repoPath, nil)
				commitID := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithBranch("master"),
					gittest.WithTree(treeID),
				)
				gittest.Exec(t, cfg, "-C", repoPath, "repack")

				// The only entries in the pack directory should be the generated packfile and its
				// corresponding index.
				packEntries, err := os.ReadDir(filepath.Join(repoPath, "objects/pack"))
				require.NoError(t, err)
				index := packEntries[0].Name()
				packfile := packEntries[1].Name()

				// Unreachable objects should also be included in the snapshot.
				unreachableCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("unreachable"))

				// Generate packed-refs file, but also keep around the loose reference.
				gittest.Exec(t, cfg, "-C", repoPath, "pack-refs", "--all", "--no-prune")
				refs := gittest.FilesOrReftables(
					[]string{"packed-refs", "refs/", "refs/heads/", "refs/heads/master", "refs/tags/"},
					append([]string{"refs/", "refs/heads", "reftable/", "reftable/tables.list"}, reftableFiles(t, repoPath)...),
				)

				// The shallow file, used if the repository is a shallow clone, is also included in snapshots.
				require.NoError(t, os.WriteFile(filepath.Join(repoPath, "shallow"), nil, perm.SharedFile))

				// Custom Git hooks are not included in snapshots.
				require.NoError(t, os.MkdirAll(filepath.Join(repoPath, "hooks"), perm.SharedDir))

				// Create a file in the objects directory that does not match the regex.
				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "objects/this-should-not-be-included"),
					nil,
					perm.SharedFile,
				))

				return setupData{
					repo: repo,
					expectedEntries: append(
						[]string{
							"HEAD",
							fmt.Sprintf("objects/%s/%s", treeID[0:2], treeID[2:]),
							fmt.Sprintf("objects/%s/%s", commitID[0:2], commitID[2:]),
							fmt.Sprintf("objects/%s/%s", unreachableCommitID[0:2], unreachableCommitID[2:]),
							filepath.Join("objects/pack", index),
							filepath.Join("objects/pack", packfile),
							"shallow",
						}, refs...),
				}
			},
		},
		{
			desc: "alternate object database does not exist",
			setup: func(t *testing.T) setupData {
				_, repo, repoPath := setupRepo(t)

				altFile, err := repo.InfoAlternatesPath(ctx)
				require.NoError(t, err)

				// Write a non-existent object database to the repository's alternates file. The RPC
				// should skip over the alternates database and continue generating the snapshot.
				altObjectDir := filepath.Join(repoPath, "does-not-exist")
				require.NoError(t, os.WriteFile(
					altFile,
					[]byte(fmt.Sprintf("%s\n", altObjectDir)),
					perm.SharedFile,
				))

				refs := gittest.FilesOrReftables(
					[]string{"refs/", "refs/heads/", "refs/tags/"},
					append([]string{"refs/", "refs/heads", "reftable/", "reftable/tables.list"}, reftableFiles(t, repoPath)...),
				)

				return setupData{
					repo:            repo,
					expectedEntries: append([]string{"HEAD"}, refs...),
				}
			},
		},
		{
			desc: "alternate file with bad permissions",
			setup: func(t *testing.T) setupData {
				_, repo, repoPath := setupRepo(t)

				altFile, err := repo.InfoAlternatesPath(ctx)
				require.NoError(t, err)

				// Write an object database with bad permissions to the repository's alternates
				// file. The RPC should skip over the alternates database and continue generating
				// the snapshot.
				altObjectDir := filepath.Join(repoPath, "alt-object-dir")
				require.NoError(t, os.WriteFile(altFile, []byte(fmt.Sprintf("%s\n", altObjectDir)), 0o000))

				setupData := setupData{
					repo: repo,
					requireError: func(actual error) {
						require.ErrorIs(t, actual, ErrSnapshotAlternates)
						require.Regexp(t, "add alternates: error getting alternate object directories: open .+/objects/info/alternates: permission denied$", actual.Error())
					},
				}

				return setupData
			},
		},
		{
			desc: "alternate object database is absolute path",
			setup: func(t *testing.T) setupData {
				cfg, repo, repoPath := setupRepo(t)

				altFile, err := repo.InfoAlternatesPath(ctx)
				require.NoError(t, err)

				altObjectDir := filepath.Join(cfg.Storages[0].Path, gittest.NewObjectPoolName(t), "objects")
				treeID := gittest.WriteTree(t, cfg, repoPath, nil)
				commitID := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTree(treeID),
					gittest.WithAlternateObjectDirectory(altObjectDir),
				)

				// We haven't yet written the alternates file, and thus we shouldn't be able to find
				// this commit yet.
				gittest.RequireObjectNotExists(t, cfg, repoPath, commitID)

				// Write the alternates file and validate that the object is now reachable.
				require.NoError(t, os.WriteFile(
					altFile,
					[]byte(fmt.Sprintf("%s\n", altObjectDir)),
					perm.SharedFile,
				))
				gittest.RequireObjectExists(t, cfg, repoPath, commitID)

				refs := gittest.FilesOrReftables(
					[]string{"refs/", "refs/heads/", "refs/tags/"},
					append([]string{"refs/", "refs/heads", "reftable/", "reftable/tables.list"}, reftableFiles(t, repoPath)...),
				)

				return setupData{
					repo: repo,
					expectedEntries: append(
						[]string{
							"HEAD",
							fmt.Sprintf("objects/%s/%s", treeID[0:2], treeID[2:]),
							fmt.Sprintf("objects/%s/%s", commitID[0:2], commitID[2:]),
						},
						refs...,
					),
				}
			},
		},
		{
			desc: "alternate object database is relative path",
			setup: func(t *testing.T) setupData {
				cfg, repo, repoPath := setupRepo(t)

				altFile, err := repo.InfoAlternatesPath(ctx)
				require.NoError(t, err)

				altObjectDir := filepath.Join(cfg.Storages[0].Path, gittest.NewObjectPoolName(t), "objects")
				treeID := gittest.WriteTree(t, cfg, repoPath, nil)
				commitID := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTree(treeID),
					gittest.WithAlternateObjectDirectory(altObjectDir),
				)

				// We haven't yet written the alternates file, and thus we shouldn't be able to find
				// this commit yet.
				gittest.RequireObjectNotExists(t, cfg, repoPath, commitID)

				// Write the alternates file and validate that the object is now reachable.
				relAltObjectDir, err := filepath.Rel(filepath.Join(repoPath, "objects"), altObjectDir)
				require.NoError(t, err)
				require.NoError(t, os.WriteFile(
					altFile,
					[]byte(fmt.Sprintf("%s\n", relAltObjectDir)),
					perm.SharedFile,
				))
				gittest.RequireObjectExists(t, cfg, repoPath, commitID)

				refs := gittest.FilesOrReftables(
					[]string{"refs/", "refs/heads/", "refs/tags/"},
					append([]string{"refs/", "refs/heads", "reftable/", "reftable/tables.list"}, reftableFiles(t, repoPath)...),
				)

				return setupData{
					repo: repo,
					expectedEntries: append(
						[]string{
							"HEAD",
							fmt.Sprintf("objects/%s/%s", treeID[0:2], treeID[2:]),
							fmt.Sprintf("objects/%s/%s", commitID[0:2], commitID[2:]),
						},
						refs...,
					),
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			var data bytes.Buffer
			err := setup.repo.CreateSnapshot(ctx, &data)
			if setup.requireError != nil {
				setup.requireError(err)
				return
			}
			require.NoError(t, err)

			entries, err := archive.TarEntries(&data)
			require.NoError(t, err)

			require.ElementsMatch(t, entries, setup.expectedEntries)
		})
	}
}

func reftableFiles(t *testing.T, repoPath string) []string {
	var files []string

	root := filepath.Join(repoPath, "reftable")

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		if d.IsDir() {
			return nil
		}

		if strings.HasSuffix(path, ".ref") {
			files = append(files, filepath.Join("reftable", filepath.Base(path)))
		}

		return nil
	})
	require.NoError(t, err)

	return files
}
