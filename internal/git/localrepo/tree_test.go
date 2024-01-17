package localrepo

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestTreeEntry_Write(t *testing.T) {
	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := NewTestRepo(t, cfg, repoProto)

	differentContentBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("different content"))
	blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("foobar\n"))

	tree := &TreeEntry{
		Type: Tree,
		Mode: "040000",
		Entries: []*TreeEntry{
			{
				OID:  blobID,
				Mode: "100644",
				Path: "file",
			},
		},
	}
	require.NoError(t, tree.Write(ctx, repo))
	treeID := tree.OID

	nonExistentBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("content"))

	nonExistentBlobPath := filepath.Join(repoPath, "objects", string(nonExistentBlobID)[0:2], string(nonExistentBlobID)[2:])
	require.NoError(t, os.Remove(nonExistentBlobPath))

	for _, tc := range []struct {
		desc              string
		entries           []*TreeEntry
		expectedEntries   []TreeEntry
		expectedErrString string
	}{
		{
			desc: "entry with blob OID",
			entries: []*TreeEntry{
				{
					OID:  blobID,
					Mode: "100644",
					Path: "file",
				},
			},
			expectedEntries: []TreeEntry{
				{
					OID:  blobID,
					Mode: "100644",
					Path: "file",
				},
			},
		},
		{
			desc: "entry with tree OID",
			entries: []*TreeEntry{
				{
					OID:  treeID,
					Mode: "040000",
					Path: "dir",
				},
			},
			expectedEntries: []TreeEntry{
				{
					OID:  blobID,
					Mode: "100644",
					Path: "dir/file",
				},
			},
		},
		{
			desc: "mixed tree and blob entries",
			entries: []*TreeEntry{
				{
					OID:  treeID,
					Mode: "040000",
					Path: "dir",
				},
				{
					OID:  blobID,
					Mode: "100644",
					Path: "file1",
				},
				{
					OID:  differentContentBlobID,
					Mode: "100644",
					Path: "file2",
				},
			},
			expectedEntries: []TreeEntry{
				{
					OID:  blobID,
					Mode: "100644",
					Path: "dir/file",
				},
				{
					OID:  blobID,
					Mode: "100644",
					Path: "file1",
				},
				{
					OID:  differentContentBlobID,
					Mode: "100644",
					Path: "file2",
				},
			},
		},
		{
			desc: "entry with nonexistent object",
			entries: []*TreeEntry{
				{
					OID:  nonExistentBlobID,
					Mode: "100644",
					Path: "file",
				},
			},
			expectedEntries: []TreeEntry{
				{
					OID:  nonExistentBlobID,
					Mode: "100644",
					Path: "file",
				},
			},
		},
		{
			desc: "entry with duplicate file",
			entries: []*TreeEntry{
				{
					OID:  blobID,
					Mode: "100644",
					Path: "file",
				},
				{
					OID:  nonExistentBlobID,
					Mode: "100644",
					Path: "file",
				},
			},
			expectedErrString: "duplicateEntries: contains duplicate file entries",
		},
		{
			desc: "entry with malformed mode",
			entries: []*TreeEntry{
				{
					OID:  blobID,
					Mode: "1006442",
					Path: "file",
				},
			},
			expectedErrString: "badFilemode: contains bad file modes",
		},
		{
			desc: "tries to write .git file",
			entries: []*TreeEntry{
				{
					OID:  blobID,
					Mode: "040000",
					Path: ".git",
				},
			},
			expectedErrString: "hasDotgit: contains '.git'",
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()
			tree := &TreeEntry{
				Type:    Tree,
				Mode:    "040000",
				Entries: tc.entries,
			}

			err := tree.Write(ctx, repo)
			oid := tree.OID
			if tc.expectedErrString != "" {
				var structErr structerr.Error
				if errors.As(err, &structErr) {
					stderr := structErr.Metadata()["stderr"].(string)
					strings.Contains(stderr, tc.expectedErrString)
				} else {
					strings.Contains(err.Error(), tc.expectedErrString)
				}
				return
			}

			require.NoError(t, err)

			output := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "ls-tree", "-r", string(oid)))

			if len(output) > 0 {
				var actualEntries []TreeEntry
				for _, line := range bytes.Split([]byte(output), []byte("\n")) {
					// Format: <mode> SP <type> SP <object> TAB <file>
					tabSplit := bytes.Split(line, []byte("\t"))
					require.Len(t, tabSplit, 2)

					spaceSplit := bytes.Split(tabSplit[0], []byte(" "))
					require.Len(t, spaceSplit, 3)

					path := string(tabSplit[1])

					objectHash, err := repo.ObjectHash(ctx)
					require.NoError(t, err)

					objectID, err := objectHash.FromHex(text.ChompBytes(spaceSplit[2]))
					require.NoError(t, err)

					actualEntries = append(actualEntries, TreeEntry{
						OID:  objectID,
						Mode: string(spaceSplit[0]),
						Path: path,
					})
				}

				require.Equal(t, tc.expectedEntries, actualEntries)
				return
			}

			require.Nil(t, tc.expectedEntries)
		})
	}
}

func TestTreeEntryByPath(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		desc     string
		input    []*TreeEntry
		expected []*TreeEntry
	}{
		{
			desc: "all blobs",
			input: []*TreeEntry{
				{
					Type: Blob,
					Path: "abc",
				},
				{
					Type: Blob,
					Path: "ab",
				},
				{
					Type: Blob,
					Path: "a",
				},
			},
			expected: []*TreeEntry{
				{
					Type: Blob,
					Path: "a",
				},
				{
					Type: Blob,
					Path: "ab",
				},
				{
					Type: Blob,
					Path: "abc",
				},
			},
		},
		{
			desc: "blobs and trees",
			input: []*TreeEntry{
				{
					Type: Blob,
					Path: "abc",
				},
				{
					Type: Tree,
					Path: "ab",
				},
				{
					Type: Blob,
					Path: "a",
				},
			},
			expected: []*TreeEntry{
				{
					Type: Blob,
					Path: "a",
				},
				{
					Type: Tree,
					Path: "ab",
				},
				{
					Type: Blob,
					Path: "abc",
				},
			},
		},
		{
			desc: "trees get sorted with / appended",
			input: []*TreeEntry{
				{
					Type: Tree,
					Path: "a",
				},
				{
					Type: Blob,
					Path: "a+",
				},
			},
			expected: []*TreeEntry{
				{
					Type: Blob,
					Path: "a+",
				},
				{
					Type: Tree,
					Path: "a",
				},
			},
		},
		{
			desc: "blobs get sorted without / appended",
			input: []*TreeEntry{
				{
					Type: Blob,
					Path: "a",
				},
				{
					Type: Blob,
					Path: "a+",
				},
			},
			expected: []*TreeEntry{
				{
					Type: Blob,
					Path: "a",
				},
				{
					Type: Blob,
					Path: "a+",
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			sort.Stable(TreeEntriesByPath(tc.input))

			require.Equal(
				t,
				tc.expected,
				tc.input,
			)
		})
	}
}

func TestReadTree(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
	})
	repo := NewTestRepo(t, cfg, repoProto)

	blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("blob contents"))
	emptyTreeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{})
	treeWithBlob := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{OID: blobID, Mode: "100644", Path: "nonexecutable"},
		{OID: blobID, Mode: "100755", Path: "executable"},
	})
	treeWithSubtree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{OID: emptyTreeID, Mode: "040000", Path: "subdir"},
	})
	treeWithNestedSubtrees := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{OID: treeWithSubtree, Mode: "040000", Path: "nested-subdir"},
	})
	treeWithSubtreeAndBlob := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{OID: treeWithSubtree, Mode: "040000", Path: "subdir"},
		{OID: blobID, Mode: "100644", Path: "blob"},
	})
	treeWithSubtreeContainingBlob := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{OID: treeWithSubtreeAndBlob, Mode: "040000", Path: "subdir"},
	})

	for _, tc := range []struct {
		desc         string
		treeish      git.Revision
		options      []ReadTreeOption
		cfg          *readTreeConfig
		expectedTree TreeEntry
		expectedErr  error
	}{
		{
			desc:    "empty tree",
			treeish: emptyTreeID.Revision(),
			expectedTree: TreeEntry{
				OID:  emptyTreeID,
				Type: Tree,
				Mode: "040000",
			},
		},
		{
			desc:    "tree with blob",
			treeish: treeWithBlob.Revision(),
			expectedTree: TreeEntry{
				OID:  treeWithBlob,
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{Mode: "100755", Type: Blob, OID: blobID, Path: "executable"},
					{Mode: "100644", Type: Blob, OID: blobID, Path: "nonexecutable"},
				},
			},
		},
		{
			desc:    "tree with subtree",
			treeish: treeWithSubtree.Revision(),
			expectedTree: TreeEntry{
				OID:  treeWithSubtree,
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{Mode: "040000", Type: Tree, OID: emptyTreeID, Path: "subdir"},
				},
			},
		},
		{
			desc:    "nested trees",
			treeish: treeWithNestedSubtrees.Revision(),
			expectedTree: TreeEntry{
				OID:  treeWithNestedSubtrees,
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{Mode: "040000", Type: Tree, OID: treeWithSubtree, Path: "nested-subdir"},
				},
			},
		},
		{
			desc:    "recursive nested trees",
			treeish: treeWithNestedSubtrees.Revision(),
			options: []ReadTreeOption{WithRecursive()},
			expectedTree: TreeEntry{
				OID:  treeWithNestedSubtrees,
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						Mode: "040000",
						Type: Tree,
						OID:  treeWithSubtree,
						Path: "nested-subdir",
						Entries: []*TreeEntry{
							{
								Mode: "040000",
								Type: Tree,
								OID:  emptyTreeID,
								Path: "subdir",
							},
						},
					},
				},
			},
		},
		{
			desc:    "nested subtree",
			treeish: treeWithNestedSubtrees.Revision(),
			options: []ReadTreeOption{WithRelativePath("nested-subdir")},
			expectedTree: TreeEntry{
				OID:  treeWithSubtree,
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{Mode: "040000", Type: Tree, OID: emptyTreeID, Path: "subdir"},
				},
			},
		},
		{
			desc:    "nested recursive subtree",
			treeish: treeWithSubtreeContainingBlob.Revision(),
			options: []ReadTreeOption{
				WithRelativePath("subdir"),
				WithRecursive(),
			},
			expectedTree: TreeEntry{
				OID:  treeWithSubtreeAndBlob,
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{Mode: "100644", Type: Blob, OID: blobID, Path: "blob"},
					{
						Mode: "040000",
						Type: Tree,
						OID:  treeWithSubtree,
						Path: "subdir",
						Entries: []*TreeEntry{
							{
								Mode: "040000",
								Type: Tree,
								OID:  emptyTreeID,
								Path: "subdir",
							},
						},
					},
				},
			},
		},
		{
			desc:    "recursive nested trees and blobs",
			treeish: treeWithSubtreeAndBlob.Revision(),
			options: []ReadTreeOption{
				WithRecursive(),
			},
			expectedTree: TreeEntry{
				OID:  treeWithSubtreeAndBlob,
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{Mode: "100644", Type: Blob, OID: blobID, Path: "blob"},
					{
						Mode: "040000",
						Type: Tree,
						OID:  treeWithSubtree,
						Path: "subdir",
						Entries: []*TreeEntry{
							{
								Mode: "040000",
								Type: Tree,
								OID:  emptyTreeID,
								Path: "subdir",
							},
						},
					},
				},
			},
		},
		{
			desc:    "listing blob fails",
			treeish: blobID.Revision(),
			// We get a NotExist error here because it's invalid to suffix an object ID
			// which resolves to a blob with a colon (":") given that it's not possible
			// to resolve a subpath.
			expectedErr: git.ErrReferenceNotFound,
		},
		{
			desc:    "valid revision with invalid path",
			treeish: treeWithSubtree.Revision(),
			options: []ReadTreeOption{
				WithRelativePath("does-not-exist"),
			},
			expectedErr: git.ErrReferenceNotFound,
		},
		{
			desc:    "valid revision with path pointing to blob",
			treeish: treeWithSubtreeAndBlob.Revision(),
			options: []ReadTreeOption{
				WithRelativePath("blob"),
			},
			expectedErr: ErrNotTreeish,
		},
		{
			desc:        "listing nonexistent object fails",
			treeish:     "does-not-exist",
			expectedErr: git.ErrReferenceNotFound,
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			tree, err := repo.ReadTree(ctx, tc.treeish, tc.options...)
			if tc.expectedErr != nil {
				require.ErrorIs(t, err, tc.expectedErr)
				return
			}
			require.Equal(t, tc.expectedTree, *tree)
		})
	}
}

func TestWriteTreeRecursively(t *testing.T) {
	cfg := testcfg.Build(t)

	testCases := []struct {
		desc      string
		setupTree func(*testing.T, string) *TreeEntry
	}{
		{
			desc: "every level has a new tree",
			setupTree: func(t *testing.T, repoPath string) *TreeEntry {
				blobA := gittest.WriteBlob(t, cfg, repoPath, []byte("a"))

				return &TreeEntry{
					OID:  "",
					Type: Tree,
					Mode: "040000",
					Entries: []*TreeEntry{
						{
							OID:  "",
							Type: Tree,
							Mode: "040000",
							Path: "dirA",
							Entries: []*TreeEntry{
								{
									OID:  "",
									Mode: "040000",
									Type: Tree,
									Path: "dirB",
									Entries: []*TreeEntry{
										{
											OID:  blobA,
											Type: Blob,
											Mode: "100644",
											Path: "file1",
										},
									},
								},
							},
						},
					},
				}
			},
		},
		{
			desc: "only some new trees",
			setupTree: func(t *testing.T, repoPath string) *TreeEntry {
				blobA := gittest.WriteBlob(t, cfg, repoPath, []byte("a"))
				blobB := gittest.WriteBlob(t, cfg, repoPath, []byte("b"))
				blobC := gittest.WriteBlob(t, cfg, repoPath, []byte("c"))
				blobD := gittest.WriteBlob(t, cfg, repoPath, []byte("d"))
				dirDTree := gittest.WriteTree(
					t,
					cfg,
					repoPath,
					[]gittest.TreeEntry{
						{
							OID:  blobC,
							Mode: "100644",
							Path: "file3",
						},
						{
							OID:  blobD,
							Mode: "100644",
							Path: "file4",
						},
					})
				dirCTree := gittest.WriteTree(
					t,
					cfg,
					repoPath,
					[]gittest.TreeEntry{
						{
							OID:  dirDTree,
							Mode: "040000",
							Path: "dirD",
						},
					},
				)

				return &TreeEntry{
					OID:  "",
					Type: Tree,
					Mode: "040000",
					Entries: []*TreeEntry{
						{
							OID:  "",
							Type: Tree,
							Mode: "040000",
							Path: "dirA",
							Entries: []*TreeEntry{
								{
									OID:  "",
									Mode: "040000",
									Type: Tree,
									Path: "dirB",
									Entries: []*TreeEntry{
										{
											OID:  blobA,
											Type: Blob,
											Mode: "100644",
											Path: "file1",
										},
										{
											OID:  blobB,
											Type: Blob,
											Mode: "100644",
											Path: "file2",
										},
									},
								},
							},
						},
						{
							OID:  dirCTree,
							Type: Tree,
							Mode: "040000",
							Path: "dirC",
							Entries: []*TreeEntry{
								{
									OID:  dirDTree,
									Mode: "040000",
									Type: Tree,
									Path: "dirD",
									Entries: []*TreeEntry{
										{
											OID:  blobC,
											Type: Blob,
											Mode: "100644",
											Path: "file3",
										},
										{
											OID:  blobD,
											Type: Blob,
											Mode: "100644",
											Path: "file4",
										},
									},
								},
							},
						},
					},
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)
			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})
			repo := NewTestRepo(t, cfg, repoProto)

			treeEntry := tc.setupTree(t, repoPath)
			require.NoError(t, treeEntry.Write(
				ctx,
				repo,
			))

			requireTreeEquals(t, ctx, repo, treeEntry.OID, treeEntry)
		})
	}
}

func requireTreeEquals(
	t *testing.T,
	ctx context.Context,
	repo *Repo,
	treeOID git.ObjectID,
	expect *TreeEntry,
) {
	tree, err := repo.ReadTree(ctx, git.Revision(treeOID))
	require.NoError(t, err)

	for i, entry := range tree.Entries {
		if tree.Entries[i].OID != "" {
			require.Equal(t, expect.Entries[i].Mode, entry.Mode)
			require.Equal(t, expect.Entries[i].Type, entry.Type)
			require.Equal(t, expect.Entries[i].OID, entry.OID)
			require.Equal(t, expect.Entries[i].Path, entry.Path)
		} else {
			objectInfo, err := repo.ReadObjectInfo(
				ctx,
				git.Revision(entry.OID),
			)
			require.NoError(t, err)

			require.Equal(t, expect.Entries[i].Type, ObjectTypeFromString(objectInfo.Type))
		}

		if entry.Type == Tree {
			requireTreeEquals(t, ctx, repo, entry.OID, expect.Entries[i])
		}
	}
}

func TestReadTree_WithRecursive(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	ctx := testhelper.Context(t)

	testCases := []struct {
		desc        string
		setupTree   func(t *testing.T, repoPath string) (git.ObjectID, TreeEntry)
		expectedErr error
	}{
		{
			desc: "flat tree",
			setupTree: func(t *testing.T, repoPath string) (git.ObjectID, TreeEntry) {
				blobA := gittest.WriteBlob(t, cfg, repoPath, []byte("a"))
				blobB := gittest.WriteBlob(t, cfg, repoPath, []byte("b"))

				treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{
						Mode: "100644",
						Path: "fileA",
						OID:  blobA,
					},
					{
						Mode: "100644",
						Path: "fileB",
						OID:  blobB,
					},
				})
				return gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"),
						gittest.WithTree(treeID)),
					TreeEntry{
						OID:  treeID,
						Mode: "040000",
						Type: Tree,
						Entries: []*TreeEntry{
							{
								Mode: "100644",
								Path: "fileA",
								OID:  blobA,
								Type: Blob,
							},
							{
								Mode: "100644",
								Path: "fileB",
								OID:  blobB,
								Type: Blob,
							},
						},
					}
			},
		},
		{
			desc: "nested tree",
			setupTree: func(t *testing.T, repoPath string) (git.ObjectID, TreeEntry) {
				blobA := gittest.WriteBlob(t, cfg, repoPath, []byte("a"))
				blobB := gittest.WriteBlob(t, cfg, repoPath, []byte("b"))
				blobC := gittest.WriteBlob(t, cfg, repoPath, []byte("c"))
				dirATree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{
						Mode: "100644",
						Path: "file1InDirA",
						OID:  blobA,
					},
				})

				treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{
						Mode: "100644",
						Path: "fileB",
						OID:  blobB,
					},
					{
						Mode: "100644",
						Path: "fileC",
						OID:  blobC,
					},
					{
						Mode: "040000",
						Path: "dirA",
						OID:  dirATree,
					},
				})

				return gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"),
						gittest.WithTree(treeID)),
					TreeEntry{
						OID:  treeID,
						Mode: "040000",
						Type: Tree,
						Entries: []*TreeEntry{
							{
								Mode: "040000",
								Path: "dirA",
								Type: Tree,
								OID:  dirATree,
								Entries: []*TreeEntry{
									{
										Mode: "100644",
										Path: "file1InDirA",
										OID:  blobA,
										Type: Blob,
									},
								},
							},
							{
								Mode: "100644",
								Path: "fileB",
								OID:  blobB,
								Type: Blob,
							},
							{
								Mode: "100644",
								Path: "fileC",
								OID:  blobC,
								Type: Blob,
							},
						},
					}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			repoProto, repoPath := gittest.CreateRepository(
				t,
				ctx,
				cfg,
				gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
			repo := NewTestRepo(t, cfg, repoProto)

			commitID, expectedTree := tc.setupTree(t, repoPath)

			tree, err := repo.ReadTree(
				ctx,
				git.Revision(commitID),
				WithRecursive(),
			)
			require.NoError(t, err)
			require.Equal(t, expectedTree, *tree)
		})
	}
}

func TestTreeEntry_Modify(t *testing.T) {
	testCases := []struct {
		desc            string
		tree            *TreeEntry
		pathToModify    string
		modifyEntryFunc func(*TreeEntry) error
		expectedTree    TreeEntry
		expectedErr     error
	}{
		{
			desc: "flat tree",
			tree: &TreeEntry{
				OID:  "abcd",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
					{
						OID:  "cba",
						Type: Blob,
						Mode: "100644",
						Path: "file2",
					},
				},
			},
			pathToModify: "file2",
			modifyEntryFunc: func(t *TreeEntry) error {
				t.OID = "deadbeef"
				t.Mode = "100755"
				t.Path = "new-file-name"
				return nil
			},
			expectedTree: TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
					{
						OID:  "deadbeef",
						Type: Blob,
						Mode: "100755",
						Path: "new-file-name",
					},
				},
			},
		},
		{
			desc: "nested tree",
			tree: &TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "def",
								Type: Tree,
								Mode: "040000",
								Path: "dirB",
								Entries: []*TreeEntry{
									{
										OID:  "",
										Type: Blob,
										Mode: "100644",
										Path: "file1",
									},
								},
							},
							{
								OID:  "aa123",
								Type: Tree,
								Mode: "040000",
								Path: "dirC",
								Entries: []*TreeEntry{
									{
										OID:  "abcd",
										Type: Blob,
										Mode: "100644",
										Path: "file2",
									},
								},
							},
						},
					},
				},
			},
			pathToModify: "dirA/dirB/file1",
			modifyEntryFunc: func(t *TreeEntry) error {
				t.OID = "deadbeef"
				t.Mode = "100755"
				t.Path = "new-file-name"
				return nil
			},
			expectedTree: TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "",
								Type: Tree,
								Mode: "040000",
								Path: "dirB",
								Entries: []*TreeEntry{
									{
										OID:  "deadbeef",
										Type: Blob,
										Mode: "100755",
										Path: "new-file-name",
									},
								},
							},
							{
								OID:  "aa123",
								Type: Tree,
								Mode: "040000",
								Path: "dirC",
								Entries: []*TreeEntry{
									{
										OID:  "abcd",
										Type: Blob,
										Mode: "100644",
										Path: "file2",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			desc: "nested tree with duplicate file",
			tree: &TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "def",
								Type: Tree,
								Mode: "040000",
								Path: "dirB",
								Entries: []*TreeEntry{
									{
										OID:  "",
										Type: Blob,
										Mode: "100644",
										Path: "file1",
									},
								},
							},
							{
								OID:  "aa123",
								Type: Tree,
								Mode: "040000",
								Path: "dirC",
								Entries: []*TreeEntry{
									{
										OID:  "abcd",
										Type: Blob,
										Mode: "100644",
										Path: "file2",
									},
								},
							},
							{
								OID:  "feedface",
								Type: Blob,
								Mode: "100644",
								Path: "file1",
							},
						},
					},
				},
			},
			pathToModify: "dirA/dirB/file1",
			modifyEntryFunc: func(t *TreeEntry) error {
				t.OID = "deadbeef"
				t.Mode = "100755"
				t.Path = "new-file-name"
				return nil
			},
			expectedTree: TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "",
								Type: Tree,
								Mode: "040000",
								Path: "dirB",
								Entries: []*TreeEntry{
									{
										OID:  "deadbeef",
										Type: Blob,
										Mode: "100755",
										Path: "new-file-name",
									},
								},
							},
							{
								OID:  "aa123",
								Type: Tree,
								Mode: "040000",
								Path: "dirC",
								Entries: []*TreeEntry{
									{
										OID:  "abcd",
										Type: Blob,
										Mode: "100644",
										Path: "file2",
									},
								},
							},
							{
								OID:  "feedface",
								Type: Blob,
								Mode: "100644",
								Path: "file1",
							},
						},
					},
				},
			},
		},
		{
			desc: "entry does not exist",
			tree: &TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
					{
						OID:  "cba",
						Type: Blob,
						Mode: "100644",
						Path: "file2",
					},
				},
			},
			pathToModify: "file3",
			modifyEntryFunc: func(t *TreeEntry) error {
				t.OID = "deadbeef"
				t.Mode = "100755"
				t.Path = "new-file-name"
				return nil
			},
			expectedErr: ErrEntryNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			err := tc.tree.Modify(
				tc.pathToModify,
				tc.modifyEntryFunc,
			)
			require.Equal(t, tc.expectedErr, err)
			if tc.expectedErr != nil {
				return
			}

			require.Equal(t, tc.expectedTree, *tc.tree)
		})
	}
}

func TestTreeEntry_Delete(t *testing.T) {
	testCases := []struct {
		desc         string
		tree         *TreeEntry
		pathToDelete string
		expectedTree TreeEntry
		expectedErr  error
	}{
		{
			desc: "flat tree",
			tree: &TreeEntry{
				OID:  "def",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
					{
						OID:  "cba",
						Type: Blob,
						Mode: "100644",
						Path: "file2",
					},
				},
			},
			pathToDelete: "file2",
			expectedTree: TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
				},
			},
		},
		{
			desc: "flat tree, single entry",
			tree: &TreeEntry{
				OID:  "def",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
				},
			},
			pathToDelete: "file1",
			expectedTree: TreeEntry{
				Type:    Tree,
				Mode:    "040000",
				Entries: []*TreeEntry{},
			},
		},
		{
			desc: "nested tree",
			tree: &TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "",
								Type: Tree,
								Mode: "040000",
								Path: "dirB",
								Entries: []*TreeEntry{
									{
										OID:  "defg",
										Type: Blob,
										Mode: "100644",
										Path: "file1",
									},
								},
							},
							{
								OID:  "aa123",
								Type: Tree,
								Mode: "040000",
								Path: "dirC",
								Entries: []*TreeEntry{
									{
										OID:  "abcd",
										Type: Blob,
										Mode: "100644",
										Path: "file2",
									},
								},
							},
						},
					},
				},
			},
			pathToDelete: "dirA/dirB/file1",
			expectedTree: TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "aa123",
								Type: Tree,
								Mode: "040000",
								Path: "dirC",
								Entries: []*TreeEntry{
									{
										OID:  "abcd",
										Type: Blob,
										Mode: "100644",
										Path: "file2",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			desc: "nested tree with only single child",
			tree: &TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "",
								Type: Tree,
								Mode: "040000",
								Path: "dirB",
								Entries: []*TreeEntry{
									{
										OID:  "aa123",
										Type: Tree,
										Mode: "040000",
										Path: "dirC",
										Entries: []*TreeEntry{
											{
												OID:  "abcd",
												Type: Blob,
												Mode: "100644",
												Path: "file1",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			pathToDelete: "dirA/dirB/dirC/file1",
			expectedTree: TreeEntry{
				Type:    Tree,
				Mode:    "040000",
				Entries: []*TreeEntry{},
			},
		},
		{
			desc: "entry does not exist",
			tree: &TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
					{
						OID:  "cba",
						Type: Blob,
						Mode: "100644",
						Path: "file2",
					},
				},
			},
			pathToDelete: "file3",
			expectedErr:  ErrEntryNotFound,
		},
		{
			desc: "path contains traversal",
			tree: &TreeEntry{
				OID:  "def",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
					{
						OID:  "cba",
						Type: Blob,
						Mode: "100644",
						Path: "file2",
					},
				},
			},
			pathToDelete: "a/../../something",
			expectedErr:  ErrPathTraversal,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			err := tc.tree.Delete(
				tc.pathToDelete,
			)

			require.Equal(t, tc.expectedErr, err)
			if tc.expectedErr != nil {
				return
			}
			require.Equal(t, tc.expectedTree, *tc.tree)
		})
	}
}

func TestTreeEntry_Add(t *testing.T) {
	testCases := []struct {
		desc           string
		tree           TreeEntry
		pathToAdd      string
		entryToAdd     TreeEntry
		expectedTree   TreeEntry
		addTreeOptions []AddTreeEntryOption
		expectedErr    error
	}{
		{
			desc: "empty tree",
			tree: TreeEntry{
				OID:  "abc",
				Type: Tree,
				Mode: "040000",
			},
			pathToAdd: "dirA/dirB/file1",
			entryToAdd: TreeEntry{
				OID:  "d1",
				Type: Blob,
				Mode: "100644",
				Path: "file1",
			},
			expectedTree: TreeEntry{
				OID:  "",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "",
								Mode: "040000",
								Type: Tree,
								Path: "dirB",
								Entries: []*TreeEntry{
									{
										OID:  "d1",
										Type: Blob,
										Mode: "100644",
										Path: "file1",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			desc: "cannot add entry into existing tree",
			tree: TreeEntry{
				OID:  "abc",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "def",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "gab",
								Mode: "040000",
								Type: Tree,
								Path: "dirB",
								Entries: []*TreeEntry{
									{
										OID:  "d1",
										Type: Blob,
										Mode: "100644",
										Path: "file1",
									},
								},
							},
						},
					},
				},
			},
			pathToAdd: "dirA/dirB/file1",
			entryToAdd: TreeEntry{
				OID:  "e1",
				Type: Blob,
				Mode: "100644",
				Path: "file2",
			},
			expectedErr: ErrEntryExists,
		},
		{
			desc: "add entry into existing tree",
			tree: TreeEntry{
				OID:  "abc",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "def",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "gab",
								Mode: "040000",
								Type: Tree,
								Path: "dirB",
								Entries: []*TreeEntry{
									{
										OID:  "d1",
										Type: Blob,
										Mode: "100644",
										Path: "file1",
									},
								},
							},
						},
					},
				},
			},
			pathToAdd: "dirA/dirB/file2",
			entryToAdd: TreeEntry{
				OID:  "e1",
				Type: Blob,
				Mode: "100644",
				Path: "file2",
			},
			expectedTree: TreeEntry{
				OID:  "",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "",
								Mode: "040000",
								Type: Tree,
								Path: "dirB",
								Entries: []*TreeEntry{
									{
										OID:  "d1",
										Type: Blob,
										Mode: "100644",
										Path: "file1",
									},
									{
										OID:  "e1",
										Type: Blob,
										Mode: "100644",
										Path: "file2",
									},
								},
							},
						},
					},
				},
			},
		},
		{
			desc: "overwrite file",
			tree: TreeEntry{
				OID:  "abc",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "def",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
				},
			},
			pathToAdd: "file1",
			entryToAdd: TreeEntry{
				OID:  "e1",
				Type: Blob,
				Mode: "100644",
				Path: "file2",
			},
			expectedTree: TreeEntry{
				OID:  "",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "e1",
						Type: Blob,
						Mode: "100644",
						Path: "file2",
					},
				},
			},
			addTreeOptions: []AddTreeEntryOption{
				WithOverwriteFile(),
			},
		},
		{
			desc: "cannot overwrite file",
			tree: TreeEntry{
				OID:  "abc",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "def",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
				},
			},
			pathToAdd: "file1",
			entryToAdd: TreeEntry{
				OID:  "e1",
				Type: Blob,
				Mode: "100644",
				Path: "file2",
			},
			expectedErr: ErrEntryExists,
		},
		{
			desc: "overwrite directory",
			tree: TreeEntry{
				OID:  "abc",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "def",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
					},
				},
			},
			pathToAdd: "dirA",
			entryToAdd: TreeEntry{
				OID:  "e1",
				Type: Blob,
				Mode: "100644",
				Path: "file2",
			},
			expectedTree: TreeEntry{
				OID:  "",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "e1",
						Type: Blob,
						Mode: "100644",
						Path: "file2",
					},
				},
			},
			addTreeOptions: []AddTreeEntryOption{
				WithOverwriteDirectory(),
			},
		},
		{
			desc: "cannot overwrite directory",
			tree: TreeEntry{
				OID:  "abc",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "def",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
					},
				},
			},
			pathToAdd: "dirA",
			entryToAdd: TreeEntry{
				OID:  "e1",
				Type: Blob,
				Mode: "100644",
				Path: "file2",
			},
			expectedErr: ErrEntryExists,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			err := tc.tree.Add(
				tc.pathToAdd,
				tc.entryToAdd,
				tc.addTreeOptions...,
			)

			require.Equal(t, tc.expectedErr, err)
			if tc.expectedErr != nil {
				return
			}
			require.Equal(t, tc.expectedTree, tc.tree)
		})
	}
}

func TestTreeEntry_Get(t *testing.T) {
	testCases := []struct {
		desc          string
		tree          *TreeEntry
		path          string
		expectedEntry TreeEntry
		expectedErr   error
	}{
		{
			desc: "flat tree",
			tree: &TreeEntry{
				OID:  "def",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
					{
						OID:  "cba",
						Type: Blob,
						Mode: "100644",
						Path: "file2",
					},
				},
			},
			path: "file2",
			expectedEntry: TreeEntry{
				OID:  "cba",
				Type: Blob,
				Mode: "100644",
				Path: "file2",
			},
		},
		{
			desc: "get root",
			tree: &TreeEntry{
				OID:  "def",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
					{
						OID:  "cba",
						Type: Blob,
						Mode: "100644",
						Path: "file2",
					},
				},
			},
			path: "",
			expectedEntry: TreeEntry{
				OID:  "def",
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
					{
						OID:  "cba",
						Type: Blob,
						Mode: "100644",
						Path: "file2",
					},
				},
			},
		},
		{
			desc: "nested tree",
			tree: &TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "",
						Type: Tree,
						Mode: "040000",
						Path: "dirA",
						Entries: []*TreeEntry{
							{
								OID:  "",
								Type: Tree,
								Mode: "040000",
								Path: "dirB",
								Entries: []*TreeEntry{
									{
										OID:  "defg",
										Type: Blob,
										Mode: "100644",
										Path: "file1",
									},
								},
							},
							{
								OID:  "aa123",
								Type: Tree,
								Mode: "040000",
								Path: "dirC",
								Entries: []*TreeEntry{
									{
										OID:  "abcd",
										Type: Blob,
										Mode: "100644",
										Path: "file2",
									},
								},
							},
						},
					},
				},
			},
			path: "dirA/dirB/file1",
			expectedEntry: TreeEntry{
				OID:  "defg",
				Type: Blob,
				Mode: "100644",
				Path: "file1",
			},
		},
		{
			desc: "entry does not exist",
			tree: &TreeEntry{
				Type: Tree,
				Mode: "040000",
				Entries: []*TreeEntry{
					{
						OID:  "abc",
						Type: Blob,
						Mode: "100644",
						Path: "file1",
					},
					{
						OID:  "cba",
						Type: Blob,
						Mode: "100644",
						Path: "file2",
					},
				},
			},
			path:        "file3",
			expectedErr: ErrEntryNotFound,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			entry, err := tc.tree.Get(
				tc.path,
			)

			require.Equal(t, tc.expectedErr, err)
			if tc.expectedErr != nil {
				return
			}
			require.Equal(t, tc.expectedEntry, *entry)
		})
	}
}

func TestValidatePath(t *testing.T) {
	testCases := []struct {
		desc         string
		path         string
		expectedPath string
		expectedErr  error
	}{
		{
			desc:         "normal path",
			path:         "a/b/c/d/e/f/g",
			expectedPath: "a/b/c/d/e/f/g",
			expectedErr:  nil,
		},
		{
			desc:         "weird path",
			path:         "a/b..c/d..e/f/g",
			expectedPath: "a/b..c/d..e/f/g",
			expectedErr:  nil,
		},
		{
			desc:        "starts with traversal",
			path:        "../../a",
			expectedErr: ErrPathTraversal,
		},
		{
			desc:        "contains traversal",
			path:        "a/../../../b",
			expectedErr: ErrPathTraversal,
		},
		{
			desc:         "does not contain traversal",
			path:         "a/b/../c",
			expectedPath: "a/c",
			expectedErr:  nil,
		},
		{
			desc:        "absolute path",
			path:        "/a/b/c",
			expectedErr: ErrAbsolutePath,
		},
		{
			desc:        "empty path",
			path:        "",
			expectedErr: ErrEmptyPath,
		},
		{
			desc:        "empty path",
			path:        ".",
			expectedErr: ErrEmptyPath,
		},
		{
			desc:        "contains .git",
			path:        ".git/objects/info",
			expectedErr: ErrDisallowedPath,
		},
		{
			desc:         "contains .git but not the git directory",
			path:         ".gitsomefile",
			expectedPath: ".gitsomefile",
			expectedErr:  nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			path, err := validateFileCreationPath(tc.path)
			require.Equal(
				t,
				tc.expectedPath,
				path,
			)
			require.Equal(
				t,
				tc.expectedErr,
				err,
			)
		})
	}
}
