package diff

import (
	"errors"
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

				newBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("new"))
				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: afterBlobID},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("added.txt"),
						OldMode:   0o000000,
						NewMode:   0o100755,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: newBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("modified.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
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

				newBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("new"))
				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				leftCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "left.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: afterBlobID},
					),
				)
				rightCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "right.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				mergeCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(leftCommit, rightCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "right.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "left.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: afterBlobID},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("right.txt"),
						OldMode:   0o000000,
						NewMode:   0o100755,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: newBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("left.txt"),
						OldMode:   0o000000,
						NewMode:   0o100755,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: newBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("modified.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
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

				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "first.txt", Mode: "100644", OID: beforeBlobID},
						gittest.TreeEntry{Path: "second.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				betweenCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "first.txt", Mode: "100644", OID: afterBlobID},
						gittest.TreeEntry{Path: "second.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				lastCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(betweenCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "first.txt", Mode: "100644", OID: afterBlobID},
						gittest.TreeEntry{Path: "second.txt", Mode: "100644", OID: afterBlobID},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("first.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("second.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
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

				renameBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("hello"))
				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "rename-me.txt", Mode: "100644", OID: renameBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "rename-you.txt", Mode: "100644", OID: renameBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: afterBlobID},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("modified.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_DELETED,
						Path:      []byte("rename-me.txt"),
						OldMode:   0o100644,
						NewMode:   0o000000,
						OldBlobId: renameBlobID.String(),
						NewBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("rename-you.txt"),
						OldMode:   0o000000,
						NewMode:   0o100644,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: renameBlobID.String(),
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

				newBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("new"))
				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "left.txt", Mode: "100644", OID: beforeBlobID},
						gittest.TreeEntry{Path: "right.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				leftCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "left.txt", Mode: "100644", OID: afterBlobID},
						gittest.TreeEntry{Path: "right.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				rightCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "left.txt", Mode: "100644", OID: beforeBlobID},
						gittest.TreeEntry{Path: "right.txt", Mode: "100644", OID: afterBlobID},
						gittest.TreeEntry{Path: "added.txt", Mode: "100644", OID: newBlobID},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("added.txt"),
						OldMode:   0o000000,
						NewMode:   0o100644,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: newBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("left.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: afterBlobID.String(),
						NewBlobId: beforeBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("right.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
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

				newBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("new"))
				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))

				firstTree := gittest.WriteTree(t, cfg, repoPath,
					[]gittest.TreeEntry{
						{Path: "README.md", Mode: "100644", OID: beforeBlobID},
					})
				secondTree := gittest.WriteTree(t, cfg, repoPath,
					[]gittest.TreeEntry{
						{Path: "README.md", Mode: "100644", OID: afterBlobID},
						{Path: "CONTRIBUTING.md", Mode: "100644", OID: newBlobID},
					})

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("CONTRIBUTING.md"),
						OldMode:   0o000000,
						NewMode:   0o100644,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: newBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("README.md"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
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

				oldReadmeOid := gittest.WriteBlob(t, cfg, repoPath, []byte("old README.md"))
				oldContributingOid := gittest.WriteBlob(t, cfg, repoPath, []byte("old CONTRIBUTING.md"))
				leftContributingOid := gittest.WriteBlob(t, cfg, repoPath, []byte("left CONTRIBUTING.md"))
				leftNewFileOid := gittest.WriteBlob(t, cfg, repoPath, []byte("left NEW_FILE.md"))
				rightNewFileOid := gittest.WriteBlob(t, cfg, repoPath, []byte("right NEW_FILE.md"))
				rightReadmeOid := gittest.WriteBlob(t, cfg, repoPath, []byte("right README.md"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", OID: oldReadmeOid},
						gittest.TreeEntry{Path: "CONTRIBUTING.md", Mode: "100644", OID: oldContributingOid},
					),
				)
				leftCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", OID: oldReadmeOid},
						gittest.TreeEntry{Path: "NEW_FILE.md", Mode: "100644", OID: leftNewFileOid},
					),
				)
				betweenCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", OID: oldReadmeOid},
					),
				)
				rightCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(betweenCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", OID: rightReadmeOid},
						gittest.TreeEntry{Path: "CONTRIBUTING.md", Mode: "100644", OID: leftContributingOid},
						gittest.TreeEntry{Path: "NEW_FILE.md", Mode: "100644", OID: rightNewFileOid},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("NEW_FILE.md"),
						OldMode:   0o000000,
						NewMode:   0o100644,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: leftNewFileOid.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_DELETED,
						Path:      []byte("CONTRIBUTING.md"),
						OldMode:   0o100644,
						NewMode:   0o000000,
						OldBlobId: leftContributingOid.String(),
						NewBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("NEW_FILE.md"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: rightNewFileOid.String(),
						NewBlobId: leftNewFileOid.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("README.md"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: rightReadmeOid.String(),
						NewBlobId: oldReadmeOid.String(),
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

				newBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("new"))
				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))
				hiBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("hi"))
				helloBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("hello"))
				welcomeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("welcome"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: afterBlobID},
					),
				)

				firstTree := gittest.WriteTree(t, cfg, repoPath,
					[]gittest.TreeEntry{
						{Path: "README.md", Mode: "100644", OID: helloBlobID},
					})
				secondTree := gittest.WriteTree(t, cfg, repoPath,
					[]gittest.TreeEntry{
						{Path: "README.md", Mode: "100644", OID: hiBlobID},
						{Path: "CONTRIBUTING.md", Mode: "100644", OID: welcomeBlobID},
					})

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("added.txt"),
						OldMode:   0o000000,
						NewMode:   0o100755,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: newBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("modified.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("CONTRIBUTING.md"),
						OldMode:   0o000000,
						NewMode:   0o100644,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: welcomeBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("README.md"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: helloBlobID.String(),
						NewBlobId: hiBlobID.String(),
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

				newBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("new"))
				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				betweenCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(betweenCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: afterBlobID},
					),
				)
				gittest.WriteTag(t, cfg, repoPath, "v1.0.0", newCommit.Revision())

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("added.txt"),
						OldMode:   0o000000,
						NewMode:   0o100755,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: newBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("modified.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
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

				newBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("new"))
				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: afterBlobID},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("added.txt"),
						OldMode:   0o000000,
						NewMode:   0o100755,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: newBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("modified.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
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

				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))
				leftBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("left"))
				rightBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("right"))
				leftRightBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("left\nright"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "common.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				leftCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "common.txt", Mode: "100644", OID: beforeBlobID},
						gittest.TreeEntry{Path: "conflicted.txt", Mode: "100644", OID: leftBlobID},
					),
				)
				rightCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "common.txt", Mode: "100644", OID: afterBlobID},
						gittest.TreeEntry{Path: "conflicted.txt", Mode: "100644", OID: rightBlobID},
					),
				)
				mergeCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(leftCommit, rightCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "common.txt", Mode: "100644", OID: afterBlobID},
						gittest.TreeEntry{Path: "conflicted.txt", Mode: "100644", OID: leftRightBlobID},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("conflicted.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: leftBlobID.String(),
						NewBlobId: leftRightBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("conflicted.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: rightBlobID.String(),
						NewBlobId: leftRightBlobID.String(),
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

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("# Hello"))
				blobID1 := gittest.WriteBlob(t, cfg, repoPath, []byte("# Hello\nWelcome"))
				blobID2 := gittest.WriteBlob(t, cfg, repoPath, []byte("# Hello\nto"))
				blobID3 := gittest.WriteBlob(t, cfg, repoPath, []byte("# Hello\nthis"))
				blobID4 := gittest.WriteBlob(t, cfg, repoPath, []byte("# Hello\nproject"))
				mergeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("# Hello\nWelcome to this project"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", OID: blobID},
					),
				)
				commit1 := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", OID: blobID1},
					),
				)
				commit2 := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", OID: blobID2},
					),
				)
				commit3 := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", OID: blobID3},
					),
				)
				commit4 := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", OID: blobID4},
					),
				)
				mergeCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit, commit1, commit2, commit3, commit4),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "README.md", Mode: "100644", OID: mergeBlobID},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("README.md"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: blobID.String(),
						NewBlobId: mergeBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("README.md"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: blobID1.String(),
						NewBlobId: mergeBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("README.md"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: blobID2.String(),
						NewBlobId: mergeBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("README.md"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: blobID3.String(),
						NewBlobId: mergeBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("README.md"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: blobID4.String(),
						NewBlobId: mergeBlobID.String(),
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
				if errors.Is(err, io.EOF) {
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

				newBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("new"))
				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "added.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: afterBlobID},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("added.txt"),
						OldMode:   0o000000,
						NewMode:   0o100755,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: newBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("modified.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
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

				newBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("new"))
				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				leftCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "left.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: afterBlobID},
					),
				)
				rightCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "right.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				mergeCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(leftCommit, rightCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "right.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "left.txt", Mode: "100755", OID: newBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: afterBlobID},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("right.txt"),
						OldMode:   0o000000,
						NewMode:   0o100755,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: newBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("left.txt"),
						OldMode:   0o000000,
						NewMode:   0o100755,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: newBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("modified.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
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

				renameBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("hello"))
				beforeBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("before"))
				afterBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("after"))

				oldCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "rename-me.txt", Mode: "100644", OID: renameBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: beforeBlobID},
					),
				)
				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithParents(oldCommit),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Path: "rename-you.txt", Mode: "100644", OID: renameBlobID},
						gittest.TreeEntry{Path: "modified.txt", Mode: "100644", OID: afterBlobID},
					),
				)

				expectedPaths := []*gitalypb.ChangedPaths{
					{
						Status:    gitalypb.ChangedPaths_MODIFIED,
						Path:      []byte("modified.txt"),
						OldMode:   0o100644,
						NewMode:   0o100644,
						OldBlobId: beforeBlobID.String(),
						NewBlobId: afterBlobID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_DELETED,
						Path:      []byte("rename-me.txt"),
						OldMode:   0o100644,
						NewMode:   0o000000,
						OldBlobId: renameBlobID.String(),
						NewBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
					},
					{
						Status:    gitalypb.ChangedPaths_ADDED,
						Path:      []byte("rename-you.txt"),
						OldMode:   0o000000,
						NewMode:   0o100644,
						OldBlobId: gittest.DefaultObjectHash.ZeroOID.String(),
						NewBlobId: renameBlobID.String(),
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
				if errors.Is(err, io.EOF) {
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
