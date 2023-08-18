package diff

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type commitRequest struct {
	commit  string
	parents []string
}

func TestFindChangedPathsRequest_success(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupDiffService(t)

	type treeRequest struct {
		left, right string
	}

	type setupData struct {
		repo          *gitalypb.Repository
		diffMode      gitalypb.FindChangedPathsRequest_MergeCommitDiffMode
		commits       []commitRequest
		trees         []treeRequest
		expectedPaths []*gitalypb.ChangedPaths
	}

	testCases := []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "Returns the expected results without a merge commit",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "after"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("added.txt"),
						OldMode: 0o000000,
						NewMode: 0o100755,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("modified.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}
				return setupData{
					repo:          repo,
					commits:       []commitRequest{{commit: newCommit.String()}},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results with a merge commit",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				leftCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "left.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "after"},
					),
				)
				rightCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "right.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				mergeCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(leftCommit, rightCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "right.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "left.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "after"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("right.txt"),
						OldMode: 0o000000,
						NewMode: 0o100755,
					},
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("left.txt"),
						OldMode: 0o000000,
						NewMode: 0o100755,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("modified.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}

				return setupData{
					repo:          repo,
					commits:       []commitRequest{{commit: mergeCommit.String()}},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results between distant commits",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "first.txt", Mode: "100644", Content: "before"},
						gittest.TreeEntry{Path: "second.txt", Mode: "100644", Content: "before"},
					),
				)
				betweenCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "first.txt", Mode: "100644", Content: "after"},
						gittest.TreeEntry{Path: "second.txt", Mode: "100644", Content: "before"},
					),
				)
				lastCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(betweenCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "first.txt", Mode: "100644", Content: "after"},
						gittest.TreeEntry{Path: "second.txt", Mode: "100644", Content: "after"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("first.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("second.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}

				return setupData{
					repo:          repo,
					commits:       []commitRequest{{commit: lastCommit.String(), parents: []string{oldCommit.String()}}},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results when a file is renamed",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "rename-me.txt", Mode: "100644", Content: "hello"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "rename-you.txt", Mode: "100644", Content: "hello"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "after"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("modified.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_DELETED,
						Path:    []byte("rename-me.txt"),
						OldMode: 0o100644,
						NewMode: 0o000000,
					},
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("rename-you.txt"),
						OldMode: 0o000000,
						NewMode: 0o100644,
					},
				}

				return setupData{
					repo:          repo,
					commits:       []commitRequest{{commit: newCommit.String()}},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results with diverging commits",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "left.txt", Mode: "100644", Content: "before"},
						gittest.TreeEntry{Path: "right.txt", Mode: "100644", Content: "before"},
					),
				)
				leftCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "left.txt", Mode: "100644", Content: "after"},
						gittest.TreeEntry{Path: "right.txt", Mode: "100644", Content: "before"},
					),
				)
				rightCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "left.txt", Mode: "100644", Content: "before"},
						gittest.TreeEntry{Path: "right.txt", Mode: "100644", Content: "after"},
						gittest.TreeEntry{Path: "added.txt", Mode: "100644", Content: "new"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("added.txt"),
						OldMode: 0o000000,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("left.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("right.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}

				return setupData{
					repo:          repo,
					commits:       []commitRequest{{commit: rightCommit.String(), parents: []string{leftCommit.String()}}},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results with trees",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				firstTree := gittest.WriteTree(t, cfg, repoPath,
					[]gittest.TreeEntry{
						{Path: "README.md", Mode: "100644", Content: "hello"},
					})
				secondTree := gittest.WriteTree(t, cfg, repoPath,
					[]gittest.TreeEntry{
						{Path: "README.md", Mode: "100644", Content: "hi"},
						{Path: "CONTRIBUTING.md", Mode: "100644", Content: "welcome"},
					})

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("CONTRIBUTING.md"),
						OldMode: 0o000000,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("README.md"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}
				return setupData{
					repo:          repo,
					trees:         []treeRequest{{left: firstTree.String(), right: secondTree.String()}},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results when multiple parent commits are specified",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "old README.md"},
						gittest.TreeEntry{Path: "CONTRIBUTING.md", Mode: "100644", Content: "old CONTRIBUTING.md"},
					),
				)
				leftCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "old README.md"},
						gittest.TreeEntry{Path: "NEW_FILE.md", Mode: "100644", Content: "left NEW_FILE.md"},
					),
				)
				betweenCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "old README.md"},
					),
				)
				rightCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(betweenCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "right README.md"},
						gittest.TreeEntry{Path: "CONTRIBUTING.md", Mode: "100644", Content: "left CONTRIBUTING.md"},
						gittest.TreeEntry{Path: "NEW_FILE.md", Mode: "100644", Content: "right NEW_FILE.md"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("NEW_FILE.md"),
						OldMode: 0o000000,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_DELETED,
						Path:    []byte("CONTRIBUTING.md"),
						OldMode: 0o100644,
						NewMode: 0o000000,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("NEW_FILE.md"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("README.md"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}

				return setupData{
					repo:          repo,
					commits:       []commitRequest{{commit: leftCommit.String(), parents: []string{betweenCommit.String(), rightCommit.String()}}},
					expectedPaths: expectedPaths,
				}
			},
		},

		{
			desc: "Returns the expected results with multiple requests",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "after"},
					),
				)

				firstTree := gittest.WriteTree(t, cfg, repoPath,
					[]gittest.TreeEntry{
						{Path: "README.md", Mode: "100644", Content: "hello"},
					})
				secondTree := gittest.WriteTree(t, cfg, repoPath,
					[]gittest.TreeEntry{
						{Path: "README.md", Mode: "100644", Content: "hi"},
						{Path: "CONTRIBUTING.md", Mode: "100644", Content: "welcome"},
					})

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("added.txt"),
						OldMode: 0o000000,
						NewMode: 0o100755,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("modified.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("CONTRIBUTING.md"),
						OldMode: 0o000000,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("README.md"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}
				return setupData{
					repo:          repo,
					commits:       []commitRequest{{commit: newCommit.String()}},
					trees:         []treeRequest{{left: firstTree.String(), right: secondTree.String()}},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results with refs and tags as commits",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				betweenCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(betweenCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "after"},
					),
				)
				gittest.WriteTag(t, cfg, repoPath, "v1.0.0", newCommit.Revision())

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("added.txt"),
						OldMode: 0o000000,
						NewMode: 0o100755,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("modified.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}
				return setupData{
					repo:          repo,
					commits:       []commitRequest{{commit: "v1.0.0", parents: []string{"v1.0.0^^"}}},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results with commits as trees",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "after"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("added.txt"),
						OldMode: 0o000000,
						NewMode: 0o100755,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("modified.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}
				return setupData{
					repo:          repo,
					trees:         []treeRequest{{left: oldCommit.String(), right: newCommit.String()}},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results when using ALL_PARENTS diff mode",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "common.txt", Mode: "100644", Content: "before"},
					),
				)
				leftCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "common.txt", Mode: "100644", Content: "before"},
						gittest.TreeEntry{Path: "conflicted.txt", Mode: "100644", Content: "left"},
					),
				)
				rightCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "common.txt", Mode: "100644", Content: "after"},
						gittest.TreeEntry{Path: "conflicted.txt", Mode: "100644", Content: "right"},
					),
				)
				mergeCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(leftCommit, rightCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "common.txt", Mode: "100644", Content: "after"},
						gittest.TreeEntry{Path: "conflicted.txt", Mode: "100644", Content: "left\nright"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("conflicted.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("conflicted.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}

				return setupData{
					repo:          repo,
					diffMode:      gitalypb.FindChangedPathsRequest_MERGE_COMMIT_DIFF_MODE_ALL_PARENTS,
					commits:       []commitRequest{{commit: mergeCommit.String()}},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results on octopus merge when using diff mode ALL_PARENTS",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "# Hello"},
					),
				)
				commit1 := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "# Hello\nWelcome"},
					),
				)
				commit2 := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "# Hello\nto"},
					),
				)
				commit3 := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "# Hello\nthis"},
					),
				)
				commit4 := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "# Hello\nproject"},
					),
				)
				mergeCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit, commit1, commit2, commit3, commit4),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", Content: "# Hello\nWelcome to this project"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("README.md"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("README.md"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("README.md"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("README.md"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("README.md"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}

				return setupData{
					repo:          repo,
					diffMode:      gitalypb.FindChangedPathsRequest_MERGE_COMMIT_DIFF_MODE_ALL_PARENTS,
					commits:       []commitRequest{{commit: mergeCommit.String()}},
					expectedPaths: expectedPaths,
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			setupData := tc.setup(t)

			rpcRequest := &gitalypb.FindChangedPathsRequest{
				Repository:          setupData.repo,
				MergeCommitDiffMode: setupData.diffMode,
			}

			for _, commitReq := range setupData.commits {
				req := &gitalypb.FindChangedPathsRequest_Request{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision:        commitReq.commit,
							ParentCommitRevisions: commitReq.parents,
						},
					},
				}
				rpcRequest.Requests = append(rpcRequest.Requests, req)
			}
			for _, treeReq := range setupData.trees {
				req := &gitalypb.FindChangedPathsRequest_Request{
					Type: &gitalypb.FindChangedPathsRequest_Request_TreeRequest_{
						TreeRequest: &gitalypb.FindChangedPathsRequest_Request_TreeRequest{
							LeftTreeRevision:  treeReq.left,
							RightTreeRevision: treeReq.right,
						},
					},
				}
				rpcRequest.Requests = append(rpcRequest.Requests, req)
			}

			stream, err := client.FindChangedPaths(ctx, rpcRequest)
			require.NoError(t, err)

			var paths []*gitalypb.ChangedPaths
			for {
				fetchedPaths, err := stream.Recv()
				if err == io.EOF {
					break
				}

				require.NoError(t, err)

				paths = append(paths, fetchedPaths.GetPaths()...)
			}
			require.Equal(t, setupData.expectedPaths, paths)
		})
	}
}

func TestFindChangedPathsRequest_deprecated(t *testing.T) {
	t.Parallel()

	cfg, client := setupDiffService(t)
	ctx := testhelper.Context(t)

	type setupData struct {
		repo          *gitalypb.Repository
		commits       []string
		expectedPaths []*gitalypb.ChangedPaths
	}

	testCases := []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "Returns the expected results without a merge commit",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "after"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("added.txt"),
						OldMode: 0o000000,
						NewMode: 0o100755,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("modified.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}
				return setupData{
					repo:          repo,
					commits:       []string{newCommit.String()},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results with a merge commit",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				leftCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "left.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "after"},
					),
				)
				rightCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "right.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				mergeCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(leftCommit, rightCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "right.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "left.txt", Mode: "100755", Content: "new"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "after"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("right.txt"),
						OldMode: 0o000000,
						NewMode: 0o100755,
					},
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("left.txt"),
						OldMode: 0o000000,
						NewMode: 0o100755,
					},
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("modified.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
				}

				return setupData{
					repo:          repo,
					commits:       []string{mergeCommit.String()},
					expectedPaths: expectedPaths,
				}
			},
		},
		{
			desc: "Returns the expected results when a file is renamed",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "rename-me.txt", Mode: "100644", Content: "hello"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "rename-you.txt", Mode: "100644", Content: "hello"},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "after"},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:  gitalypb.ChangedPaths_MODIFIED,
						Path:    []byte("modified.txt"),
						OldMode: 0o100644,
						NewMode: 0o100644,
					},
					{
						Status:  gitalypb.ChangedPaths_DELETED,
						Path:    []byte("rename-me.txt"),
						OldMode: 0o100644,
						NewMode: 0o000000,
					},
					{
						Status:  gitalypb.ChangedPaths_ADDED,
						Path:    []byte("rename-you.txt"),
						OldMode: 0o000000,
						NewMode: 0o100644,
					},
				}

				return setupData{
					repo:          repo,
					commits:       []string{newCommit.String()},
					expectedPaths: expectedPaths,
				}
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			setupData := tc.setup(t)

			rpcRequest := &gitalypb.FindChangedPathsRequest{
				Repository: setupData.repo,
				Commits:    setupData.commits,
			}

			stream, err := client.FindChangedPaths(ctx, rpcRequest)
			require.NoError(t, err)

			var paths []*gitalypb.ChangedPaths
			for {
				fetchedPaths, err := stream.Recv()
				if err == io.EOF {
					break
				}

				require.NoError(t, err)

				paths = append(paths, fetchedPaths.GetPaths()...)
			}
			require.Equal(t, setupData.expectedPaths, paths)
		})
	}
}

func TestFindChangedPathsRequest_failing(t *testing.T) {
	t.Parallel()

	cfg, client := setupDiffService(t)
	ctx := testhelper.Context(t)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	oldCommit := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "modified.txt", Mode: "100644", Content: "before"},
		),
	)
	addedBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("new"))
	modifiedBlob := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))
	newTree := gittest.WriteTree(t, cfg, repoPath,
		[]gittest.TreeEntry{
			{Path: "added.txt", Mode: "100755", OID: addedBlob},
			{Path: "modified.txt", Mode: "100644", OID: modifiedBlob},
		},
	)
	newCommit := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(oldCommit),
		gittest.WithTree(newTree),
	)

	tests := []struct {
		desc     string
		repo     *gitalypb.Repository
		commits  []string
		requests []*gitalypb.FindChangedPathsRequest_Request
		err      error
	}{
		{
			desc:    "Repository not provided",
			repo:    nil,
			commits: []string{newCommit.String(), oldCommit.String()},
			err:     structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc:    "Repo not found",
			repo:    &gitalypb.Repository{StorageName: cfg.Storages[0].Name, RelativePath: "bar.git"},
			commits: []string{newCommit.String(), oldCommit.String()},
			err: testhelper.ToInterceptedMetadata(
				structerr.New("%w", storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, "bar.git")),
			),
		},
		{
			desc:    "Storage not found",
			repo:    &gitalypb.Repository{StorageName: "foo", RelativePath: "bar.git"},
			commits: []string{newCommit.String(), oldCommit.String()},
			err: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
				"%w", storage.NewStorageNotFoundError("foo"),
			)),
		},
		{
			desc:    "Commits cannot contain an empty commit",
			repo:    repo,
			commits: []string{""},
			err:     structerr.NewInvalidArgument("resolving commit: revision cannot be empty"),
		},
		{
			desc:    "Specifying both commits and requests",
			repo:    repo,
			commits: []string{newCommit.String()},
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: newCommit.String(),
						},
					},
				},
			},
			err: structerr.NewInvalidArgument("cannot specify both commits and requests"),
		},
		{
			desc:    "Commit not found",
			repo:    repo,
			commits: []string{"notfound", oldCommit.String()},
			err:     structerr.NewNotFound(`resolving commit: revision can not be found: "notfound"`),
		},
		{
			desc: "Tree object as commit",
			repo: repo,
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: newTree.String(),
						},
					},
				},
			},
			err: structerr.NewNotFound("resolving commit: revision can not be found: %q", newTree),
		},
		{
			desc: "Tree object as parent commit",
			repo: repo,
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: newCommit.String(),
							ParentCommitRevisions: []string{
								newTree.String(),
							},
						},
					},
				},
			},
			err: structerr.NewNotFound("resolving commit parent: revision can not be found: %q", newTree),
		},
		{
			desc: "Blob object as left tree",
			repo: repo,
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_TreeRequest_{
						TreeRequest: &gitalypb.FindChangedPathsRequest_Request_TreeRequest{
							LeftTreeRevision:  addedBlob.String(),
							RightTreeRevision: newTree.String(),
						},
					},
				},
			},
			err: structerr.NewNotFound("resolving left tree: revision can not be found: %q", addedBlob),
		},
		{
			desc: "Blob object as right tree",
			repo: repo,
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_TreeRequest_{
						TreeRequest: &gitalypb.FindChangedPathsRequest_Request_TreeRequest{
							LeftTreeRevision:  newTree.String(),
							RightTreeRevision: addedBlob.String(),
						},
					},
				},
			},
			err: structerr.NewNotFound("resolving right tree: revision can not be found: %q", addedBlob),
		},
	}

	for _, tc := range tests {
		rpcRequest := &gitalypb.FindChangedPathsRequest{Repository: tc.repo, Commits: tc.commits, Requests: tc.requests}
		stream, err := client.FindChangedPaths(ctx, rpcRequest)
		require.NoError(t, err)

		t.Run(tc.desc, func(t *testing.T) {
			_, err := stream.Recv()
			testhelper.RequireGrpcError(t, tc.err, err)
		})
	}
}
