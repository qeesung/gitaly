package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/archive"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func TestGetSnapshot(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryService(t)

	getSnapshot := func(tb testing.TB, ctx context.Context, client gitalypb.RepositoryServiceClient, req *gitalypb.GetSnapshotRequest) ([]byte, error) {
		stream, err := client.GetSnapshot(ctx, req)
		if err != nil {
			return nil, err
		}

		reader := streamio.NewReader(func() ([]byte, error) {
			response, err := stream.Recv()
			return response.GetData(), err
		})

		buf := bytes.NewBuffer(nil)
		_, err = io.Copy(buf, reader)

		return buf.Bytes(), err
	}

	equalError := func(tb testing.TB, expected error) func(error) {
		return func(actual error) {
			tb.Helper()
			testhelper.RequireGrpcError(tb, expected, actual)
		}
	}

	type setupData struct {
		repo            *gitalypb.Repository
		expectedEntries []string
		requireError    func(error)
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "repository does not exist",
			setup: func(t *testing.T) setupData {
				// If the requested repository does not exist, the RPC should return an error.
				return setupData{
					repo: &gitalypb.Repository{
						StorageName:  cfg.Storages[0].Name,
						RelativePath: "does-not-exist.git",
					},
					requireError: equalError(t, testhelper.WithInterceptedMetadataItems(
						structerr.NewNotFound("repository not found"),
						structerr.MetadataItem{Key: "relative_path", Value: "does-not-exist.git"},
						structerr.MetadataItem{Key: "storage_name", Value: cfg.Storages[0].Name},
					)),
				}
			},
		},
		{
			desc: "storage not set in request",
			setup: func(t *testing.T) setupData {
				// If the request does not contain a storage, the RPC should return an error.
				return setupData{
					repo: &gitalypb.Repository{
						StorageName:  "",
						RelativePath: "some-relative-path",
					},
					requireError: equalError(t, structerr.NewInvalidArgument("%w", storage.ErrStorageNotSet)),
				}
			},
		},
		{
			desc: "relative path not set in request",
			setup: func(t *testing.T) setupData {
				// If the request does not contain a relative path, the RPC should return an error.
				return setupData{
					repo: &gitalypb.Repository{
						StorageName:  cfg.Storages[0].Name,
						RelativePath: "",
					},
					requireError: equalError(t, structerr.NewInvalidArgument("%w", storage.ErrRepositoryPathNotSet)),
				}
			},
		},
		{
			desc: "repository snapshot success",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

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
					repo: repoProto,
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
			desc: "alternate file with bad permissions",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				repo := localrepo.NewTestRepo(t, cfg, repoProto)
				altFile, err := repo.InfoAlternatesPath(ctx)
				require.NoError(t, err)

				// Write an object database with bad permissions to the repository's alternates
				// file. The RPC should skip over the alternates database and continue generating
				// the snapshot.
				altObjectDir := filepath.Join(repoPath, "alt-object-dir")
				require.NoError(t, os.WriteFile(altFile, []byte(fmt.Sprintf("%s\n", altObjectDir)), 0o000))

				refs := gittest.FilesOrReftables(
					[]string{"refs/", "refs/heads/", "refs/tags/"},
					append([]string{"refs/", "refs/heads", "reftable/", "reftable/tables.list"}, reftableFiles(t, repoPath)...),
				)

				setupData := setupData{
					repo:            repoProto,
					expectedEntries: append([]string{"HEAD"}, refs...),
				}

				if testhelper.IsWALEnabled() {
					setupData.expectedEntries = nil
					setupData.requireError = func(actual error) {
						// Skipping an alternate due to bad permissions could lead to corrupted snapshots. It would be better
						// to fix the problem, so we don't strive to match the behavior here with transactions.
						require.Regexp(t, "begin transaction: get snapshot: new shared snapshot: create repository snapshots: get alternate path: read alternates file: open: open .+/objects/info/alternates: permission denied$", actual.Error())
					}
				}

				return setupData
			},
		},
		{
			desc: "alternate object database escapes storage root",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				repo := localrepo.NewTestRepo(t, cfg, repoProto)
				altFile, err := repo.InfoAlternatesPath(ctx)
				require.NoError(t, err)

				altObjectDir := filepath.Join(cfg.Storages[0].Path, "../alt-dir")
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
					repo: repoProto,
					expectedEntries: append(
						[]string{
							"HEAD",
							// The commit ID is not included because it exists in an alternate object
							// database that is outside the storage root.
							fmt.Sprintf("objects/%s/%s", treeID[0:2], treeID[2:]),
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

			data, err := getSnapshot(t, ctx, client, &gitalypb.GetSnapshotRequest{Repository: setup.repo})
			if setup.requireError != nil {
				setup.requireError(err)
				return
			}
			require.NoError(t, err)

			entries, err := archive.TarEntries(bytes.NewReader(data))
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
