package gitpipe

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestDiffTree(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	for _, tc := range []struct {
		desc        string
		setup       func(t *testing.T, repoPath string) (git.Revision, git.Revision, []RevisionResult)
		options     []DiffTreeOption
		expectedErr error
	}{
		{
			desc: "single file",
			setup: func(t *testing.T, repoPath string) (git.Revision, git.Revision, []RevisionResult) {
				treeA := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "unchanged", Mode: "100644", Content: "unchanged"},
					{Path: "changed", Mode: "100644", Content: "a"},
				})

				changedBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("b"))
				treeB := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "unchanged", Mode: "100644", Content: "unchanged"},
					{Path: "changed", Mode: "100644", OID: changedBlob},
				})

				return treeA.Revision(), treeB.Revision(), []RevisionResult{
					{OID: changedBlob, ObjectName: []byte("changed")},
				}
			},
		},
		{
			desc: "single file in subtree without recursive",
			setup: func(t *testing.T, repoPath string) (git.Revision, git.Revision, []RevisionResult) {
				treeA := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{
						Path:    "unchanged",
						Mode:    "100644",
						Content: "unchanged",
					},
					{
						Path: "subtree",
						Mode: "040000",
						OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
							{Path: "changed", Mode: "100644", Content: "a"},
						}),
					},
				})

				changedSubtree := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "changed", Mode: "100644", Content: "b"},
				})
				treeB := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{
						Path:    "unchanged",
						Mode:    "100644",
						Content: "unchanged",
					},
					{
						Path: "subtree",
						Mode: "040000",
						OID:  changedSubtree,
					},
				})

				return treeA.Revision(), treeB.Revision(), []RevisionResult{
					{OID: changedSubtree, ObjectName: []byte("subtree")},
				}
			},
		},
		{
			desc: "single file in subtree with recursive",
			setup: func(t *testing.T, repoPath string) (git.Revision, git.Revision, []RevisionResult) {
				treeA := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{
						Path:    "unchanged",
						Mode:    "100644",
						Content: "unchanged",
					},
					{
						Path: "subtree",
						Mode: "040000",
						OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
							{Path: "changed", Mode: "100644", Content: "a"},
						}),
					},
				})

				changedBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("b"))
				treeB := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{
						Path:    "unchanged",
						Mode:    "100644",
						Content: "unchanged",
					},
					{
						Path: "subtree",
						Mode: "040000",
						OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
							{Path: "changed", Mode: "100644", OID: changedBlob},
						}),
					},
				})

				return treeA.Revision(), treeB.Revision(), []RevisionResult{
					{OID: changedBlob, ObjectName: []byte("subtree/changed")},
				}
			},
			options: []DiffTreeOption{
				DiffTreeWithRecursive(),
			},
		},
		{
			desc: "with submodules",
			setup: func(t *testing.T, repoPath string) (git.Revision, git.Revision, []RevisionResult) {
				submodule := gittest.WriteCommit(t, cfg, repoPath)

				treeA := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: ".gitmodules", Mode: "100644", Content: "a"},
				})

				changedGitmodules := gittest.WriteBlob(t, cfg, repoPath, []byte("b"))
				treeB := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: ".gitmodules", Mode: "100644", OID: changedGitmodules},
					{Path: "submodule", Mode: "160000", OID: submodule},
				})

				return treeA.Revision(), treeB.Revision(), []RevisionResult{
					{OID: changedGitmodules, ObjectName: []byte(".gitmodules")},
					{OID: submodule, ObjectName: []byte("submodule")},
				}
			},
		},
		{
			desc: "without submodules",
			setup: func(t *testing.T, repoPath string) (git.Revision, git.Revision, []RevisionResult) {
				submodule := gittest.WriteCommit(t, cfg, repoPath)

				treeA := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: ".gitmodules", Mode: "100644", Content: "a"},
				})

				changedGitmodules := gittest.WriteBlob(t, cfg, repoPath, []byte("b"))
				treeB := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: ".gitmodules", Mode: "100644", OID: changedGitmodules},
					{Path: "submodule", Mode: "160000", OID: submodule},
				})

				return treeA.Revision(), treeB.Revision(), []RevisionResult{
					{OID: changedGitmodules, ObjectName: []byte(".gitmodules")},
				}
			},
			options: []DiffTreeOption{
				DiffTreeWithIgnoreSubmodules(),
			},
		},
		{
			desc: "with skip function",
			setup: func(t *testing.T, repoPath string) (git.Revision, git.Revision, []RevisionResult) {
				treeA := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "a", Mode: "100644", Content: "1"},
					{Path: "b", Mode: "100644", Content: "2"},
				})

				changedBlobA := gittest.WriteBlob(t, cfg, repoPath, []byte("x"))
				treeB := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "a", Mode: "100644", OID: changedBlobA},
					{Path: "b", Mode: "100644", Content: "y"},
				})

				return treeA.Revision(), treeB.Revision(), []RevisionResult{
					{OID: changedBlobA, ObjectName: []byte("a")},
				}
			},
			options: []DiffTreeOption{
				DiffTreeWithSkip(func(r *RevisionResult) bool {
					return bytes.Equal(r.ObjectName, []byte("b"))
				}),
			},
		},
		{
			desc: "invalid revision",
			setup: func(t *testing.T, repoPath string) (git.Revision, git.Revision, []RevisionResult) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				return "refs/heads/main", "refs/heads/does-not-exist", nil
			},
			expectedErr: errors.New("diff-tree pipeline command: exit status 128, stderr: " +
				"\"fatal: ambiguous argument 'refs/heads/does-not-exist': unknown revision or path not in the working tree.\\n" +
				"Use '--' to separate paths from revisions, like this:\\n" +
				"'git <command> [<revision>...] -- [<file>...]'\\n\""),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			leftRevision, rightRevision, expectedResults := tc.setup(t, repoPath)

			it := DiffTree(ctx, repo, leftRevision.String(), rightRevision.String(), tc.options...)

			var results []RevisionResult
			for it.Next() {
				results = append(results, it.Result())
			}

			// We're converting the error here to a plain un-nested error such that we
			// don't have to replicate the complete error's structure.
			err := it.Err()
			if err != nil {
				err = errors.New(err.Error())
			}

			require.Equal(t, tc.expectedErr, err)
			require.Equal(t, expectedResults, results)
		})
	}
}
