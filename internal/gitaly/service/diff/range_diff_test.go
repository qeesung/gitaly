package diff

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/rangediff"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestRangeDiff_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupDiffService(t)

	type setupData struct {
		request             *gitalypb.RangeDiffRequest
		expectedCommitPairs []*rangediff.CommitPair
	}

	testCases := []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "range diff not equal metadata",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				blob1 := gittest.WriteBlob(t, cfg, repoPath, []byte("random string of text\n"))
				initCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob1},
				))

				blob2 := gittest.WriteBlob(t, cfg, repoPath, []byte("random of string text\n"))

				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 2"))

				return setupData{
					request: &gitalypb.RangeDiffRequest{
						Repository: repoProto,
						RangeSpec: &gitalypb.RangeDiffRequest_RevisionRange{
							RevisionRange: &gitalypb.RevisionRange{
								Rev1: commit1.String(),
								Rev2: commit2.String(),
							},
						},
					},
					expectedCommitPairs: []*rangediff.CommitPair{
						{
							FromCommit:         commit1.String(),
							ToCommit:           commit2.String(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
							CommitMessageTitle: "test commit message title 1",
							PatchData: []byte(`@@ Metadata
 Author: Scrooge McDuck <scrooge@mcduck.com>
 
  ## Commit message ##
-    test commit message title 1
+    test commit message title 2
 
  ## foo ##
 @@
`),
						},
					},
				}
			},
		},
		{
			desc: "range diff not equal data",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				blob1Data := bytes.Repeat([]byte("random string of text\n"), 3)
				blob1 := gittest.WriteBlob(t, cfg, repoPath, blob1Data)
				initCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob1},
				))

				blob2Data := append(blob1Data, []byte("blob2\n")...)
				blob2 := gittest.WriteBlob(t, cfg, repoPath, blob2Data)
				blob3Data := append(blob1Data, []byte("blob3\n")...)
				blob3 := gittest.WriteBlob(t, cfg, repoPath, blob3Data)

				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob3}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				return setupData{
					request: &gitalypb.RangeDiffRequest{
						Repository: repoProto,
						RangeSpec: &gitalypb.RangeDiffRequest_RevisionRange{
							RevisionRange: &gitalypb.RevisionRange{
								Rev1: commit1.String(),
								Rev2: commit2.String(),
							},
						},
					},
					expectedCommitPairs: []*rangediff.CommitPair{
						{
							FromCommit:         commit1.String(),
							ToCommit:           commit2.String(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
							CommitMessageTitle: "test commit message title 1",
							PatchData: []byte(`@@ foo
  random string of text
  random string of text
  random string of text
-+blob2
++blob3
`),
						},
					},
				}
			},
		},
		{
			desc: "range diff with range pair",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				blob1 := gittest.WriteBlob(t, cfg, repoPath, []byte("random string of text\n"))
				initCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob1},
				))

				blob2 := gittest.WriteBlob(t, cfg, repoPath, []byte("random of string text\n"))

				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 2"))

				return setupData{
					request: &gitalypb.RangeDiffRequest{
						Repository: repoProto,
						RangeSpec: &gitalypb.RangeDiffRequest_RangePair{
							RangePair: &gitalypb.RangePair{
								Range1: fmt.Sprintf("%s..%s", initCommit.String(), commit1.String()),
								Range2: fmt.Sprintf("%s..%s", initCommit.String(), commit2.String()),
							},
						},
					},
					expectedCommitPairs: []*rangediff.CommitPair{
						{
							FromCommit:         commit1.String(),
							ToCommit:           commit2.String(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
							CommitMessageTitle: "test commit message title 1",
							PatchData: []byte(`@@ Metadata
 Author: Scrooge McDuck <scrooge@mcduck.com>
 
  ## Commit message ##
-    test commit message title 1
+    test commit message title 2
 
  ## foo ##
 @@
`),
						},
					},
				}
			},
		},
		{
			desc: "range diff with base with revisions",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				blob1 := gittest.WriteBlob(t, cfg, repoPath, []byte("random string of text\n"))
				initCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob1},
				))

				blob2 := gittest.WriteBlob(t, cfg, repoPath, []byte("random of string text\n"))

				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 2"))

				return setupData{
					request: &gitalypb.RangeDiffRequest{
						Repository: repoProto,
						RangeSpec: &gitalypb.RangeDiffRequest_BaseWithRevisions{
							BaseWithRevisions: &gitalypb.BaseWithRevisions{
								Base: initCommit.String(),
								Rev1: commit1.String(),
								Rev2: commit2.String(),
							},
						},
					},
					expectedCommitPairs: []*rangediff.CommitPair{
						{
							FromCommit:         commit1.String(),
							ToCommit:           commit2.String(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
							CommitMessageTitle: "test commit message title 1",
							PatchData: []byte(`@@ Metadata
 Author: Scrooge McDuck <scrooge@mcduck.com>
 
  ## Commit message ##
-    test commit message title 1
+    test commit message title 2
 
  ## foo ##
 @@
`),
						},
					},
				}
			},
		},
		{
			desc: "range diff equal",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				blob1 := gittest.WriteBlob(t, cfg, repoPath, []byte("random string of text\n"))
				initCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob1},
				))

				blob2 := gittest.WriteBlob(t, cfg, repoPath, []byte("random of string text\n"))

				current, err := time.Parse("2006-01-02T15:04:05Z", "2023-01-01T00:00:00Z")
				require.NoError(t, err)
				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithCommitterDate(current),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithCommitterDate(current.Add(time.Hour)),
					gittest.WithMessage("test commit message title 1"))

				return setupData{
					request: &gitalypb.RangeDiffRequest{
						Repository: repoProto,
						RangeSpec: &gitalypb.RangeDiffRequest_RevisionRange{
							RevisionRange: &gitalypb.RevisionRange{
								Rev1: commit1.String(),
								Rev2: commit2.String(),
							},
						},
					},
					expectedCommitPairs: []*rangediff.CommitPair{
						{
							FromCommit:         commit1.String(),
							ToCommit:           commit2.String(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_EQUAL_UNSPECIFIED,
							CommitMessageTitle: "test commit message title 1",
						},
					},
				}
			},
		},
		{
			desc: "range diff greater than",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				blob1 := gittest.WriteBlob(t, cfg, repoPath, []byte("random string of text\n"))
				initCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob1},
				))

				blob2 := gittest.WriteBlob(t, cfg, repoPath, []byte("random of string text\n"))

				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				commit3 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(commit2),
					gittest.WithMessage("test commit message title 2"))

				return setupData{
					request: &gitalypb.RangeDiffRequest{
						Repository: repoProto,
						RangeSpec: &gitalypb.RangeDiffRequest_RevisionRange{
							RevisionRange: &gitalypb.RevisionRange{
								Rev1: commit1.String(),
								Rev2: commit3.String(),
							},
						},
					},
					expectedCommitPairs: []*rangediff.CommitPair{
						{
							FromCommit:         getRangeDiffNoObjectID(),
							ToCommit:           commit3.String(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_GREATER_THAN,
							CommitMessageTitle: "test commit message title 2",
						},
					},
				}
			},
		},
		{
			desc: "range diff less than",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				blob1 := gittest.WriteBlob(t, cfg, repoPath, []byte("random string of text\n"))
				initCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob1},
				))

				blob2 := gittest.WriteBlob(t, cfg, repoPath, []byte("random of string text\n"))

				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				commit3 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(commit2),
					gittest.WithMessage("test commit message title 2"))

				return setupData{
					request: &gitalypb.RangeDiffRequest{
						Repository: repoProto,
						RangeSpec: &gitalypb.RangeDiffRequest_RevisionRange{
							RevisionRange: &gitalypb.RevisionRange{
								Rev1: commit3.String(),
								Rev2: commit1.String(),
							},
						},
					},
					expectedCommitPairs: []*rangediff.CommitPair{
						{
							FromCommit:         commit3.String(),
							ToCommit:           getRangeDiffNoObjectID(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN,
							CommitMessageTitle: "test commit message title 2",
						},
					},
				}
			},
		},
		{
			desc: "range diff four comparisons",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				blob1 := gittest.WriteBlob(t, cfg, repoPath, []byte("random string of text\n"))
				blob0Data := bytes.Repeat([]byte("random string of text\n"), 3)
				blob0 := gittest.WriteBlob(t, cfg, repoPath, blob0Data)
				initCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob1},
					gittest.TreeEntry{Path: "bar", Mode: "100644", OID: blob0},
				))

				// =
				blob2 := gittest.WriteBlob(t, cfg, repoPath, []byte("random of string text\n"))
				current, err := time.Parse("2006-01-02T15:04:05Z", "2023-01-01T00:00:00Z")
				require.NoError(t, err)
				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithCommitterDate(current),
					gittest.WithMessage("test commit message title 1"))

				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithCommitterDate(current.Add(time.Hour)),
					gittest.WithMessage("test commit message title 1"))
				// !
				blob3 := gittest.WriteBlob(t, cfg, repoPath, append(blob0Data, []byte("blob3\n")...))
				commit3 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "bar", Mode: "100644", OID: blob3}),
					gittest.WithParents(commit1),
					gittest.WithMessage("test commit message title 3"))
				blob4 := gittest.WriteBlob(t, cfg, repoPath, append(blob0Data, []byte("blob4\n")...))
				commit4 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "bar", Mode: "100644", OID: blob4}),
					gittest.WithParents(commit2),
					gittest.WithMessage("test commit message title 3"))
				// <
				blob5 := gittest.WriteBlob(t, cfg, repoPath, bytes.Repeat([]byte("blob5\n"), 3))
				commit5 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob5", Mode: "100644", OID: blob5}),
					gittest.WithParents(commit3),
					gittest.WithMessage("test commit message title 5"))
				// >
				blob6 := gittest.WriteBlob(t, cfg, repoPath, bytes.Repeat([]byte("blob6\n"), 3))
				commit6 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob6", Mode: "100644", OID: blob6}),
					gittest.WithParents(commit4),
					gittest.WithMessage("test commit message title 6"))
				return setupData{
					request: &gitalypb.RangeDiffRequest{
						Repository: repoProto,
						RangeSpec: &gitalypb.RangeDiffRequest_RevisionRange{
							RevisionRange: &gitalypb.RevisionRange{
								Rev1: commit5.String(),
								Rev2: commit6.String(),
							},
						},
					},
					expectedCommitPairs: []*rangediff.CommitPair{
						{
							FromCommit:         commit1.String(),
							ToCommit:           commit2.String(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_EQUAL_UNSPECIFIED,
							CommitMessageTitle: "test commit message title 1",
						},
						{
							FromCommit:         commit3.String(),
							ToCommit:           commit4.String(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
							CommitMessageTitle: "test commit message title 3",
							PatchData: []byte(`@@ bar (new)
 +random string of text
 +random string of text
 +random string of text
-+blob3
++blob4
 
  ## foo (deleted) ##
 @@
`),
						},
						{
							FromCommit:         commit5.String(),
							ToCommit:           getRangeDiffNoObjectID(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_LESS_THAN,
							CommitMessageTitle: "test commit message title 5",
						},
						{
							FromCommit:         getRangeDiffNoObjectID(),
							ToCommit:           commit6.String(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_GREATER_THAN,
							CommitMessageTitle: "test commit message title 6",
						},
					},
				}
			},
		},
		{
			desc: "range diff not equal big data",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				blob1Data := bytes.Repeat([]byte("1234567890\n"), 1000)
				blob1 := gittest.WriteBlob(t, cfg, repoPath, blob1Data)
				initCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob1},
				))

				blob2Data := bytes.Repeat([]byte("1234567891\n"), 1000)
				blob2 := gittest.WriteBlob(t, cfg, repoPath, blob2Data)
				blob3Data := bytes.Repeat([]byte("1234567892\n"), 1000)
				blob3 := gittest.WriteBlob(t, cfg, repoPath, blob3Data)

				commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob3}),
					gittest.WithParents(initCommit),
					gittest.WithMessage("test commit message title 1"))

				expectPatchData := "@@ foo\n" + strings.Repeat(" -1234567890\n", 3) +
					strings.Repeat("-+1234567891\n", 1000) + strings.Repeat("++1234567892\n", 1000)

				return setupData{
					request: &gitalypb.RangeDiffRequest{
						Repository: repoProto,
						RangeSpec: &gitalypb.RangeDiffRequest_RevisionRange{
							RevisionRange: &gitalypb.RevisionRange{
								Rev1: commit1.String(),
								Rev2: commit2.String(),
							},
						},
					},
					expectedCommitPairs: []*rangediff.CommitPair{
						{
							FromCommit:         commit1.String(),
							ToCommit:           commit2.String(),
							Comparison:         gitalypb.RangeDiffResponse_COMPARATOR_NOT_EQUAL,
							CommitMessageTitle: "test commit message title 1",
							PatchData:          []byte(expectPatchData),
						},
					},
				}
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			setupData := tc.setup(t)

			stream, err := client.RangeDiff(ctx, setupData.request)
			require.NoError(t, err)

			assertExactReceivedRangeDiffs(t, stream, setupData.expectedCommitPairs)
		})
	}
}

func TestRangeDiff_failures(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupDiffService(t)

	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	testCases := []struct {
		desc        string
		request     *gitalypb.RangeDiffRequest
		expectedErr error
	}{
		{
			desc: "RevisionRange Rev1 is empty",
			request: &gitalypb.RangeDiffRequest{
				Repository: &gitalypb.Repository{
					StorageName:  "fake",
					RelativePath: "fake",
				},
			},
			expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
				"%w", storage.NewStorageNotFoundError("fake"),
			)),
		},
		{
			desc: "RevisionRange Rev1 is empty",
			request: &gitalypb.RangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RangeDiffRequest_RevisionRange{
					RevisionRange: &gitalypb.RevisionRange{
						Rev1: "",
						Rev2: "fake",
					},
				},
			},
			expectedErr: structerr.NewInvalidArgument("empty Rev1"),
		},
		{
			desc: "RevisionRange Rev2 is empty",
			request: &gitalypb.RangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RangeDiffRequest_RevisionRange{
					RevisionRange: &gitalypb.RevisionRange{
						Rev1: "fake",
						Rev2: "",
					},
				},
			},
			expectedErr: structerr.NewInvalidArgument("empty Rev2"),
		},
		{
			desc: "RangePair Range1 is empty",
			request: &gitalypb.RangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RangeDiffRequest_RangePair{
					RangePair: &gitalypb.RangePair{
						Range1: "",
						Range2: "fake",
					},
				},
			},
			expectedErr: structerr.NewInvalidArgument("empty Range1"),
		},
		{
			desc: "RangePair Range2 is empty",
			request: &gitalypb.RangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RangeDiffRequest_RangePair{
					RangePair: &gitalypb.RangePair{
						Range1: "fake",
						Range2: "",
					},
				},
			},
			expectedErr: structerr.NewInvalidArgument("empty Range2"),
		},
		{
			desc: "BaseWithRevisions Rev1 is empty",
			request: &gitalypb.RangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RangeDiffRequest_BaseWithRevisions{
					BaseWithRevisions: &gitalypb.BaseWithRevisions{
						Rev1: "",
						Rev2: "fake",
						Base: "fake",
					},
				},
			},
			expectedErr: structerr.NewInvalidArgument("empty Rev1"),
		},
		{
			desc: "BaseWithRevisions Rev2 is empty",
			request: &gitalypb.RangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RangeDiffRequest_BaseWithRevisions{
					BaseWithRevisions: &gitalypb.BaseWithRevisions{
						Rev1: "fake",
						Rev2: "",
						Base: "fake",
					},
				},
			},
			expectedErr: structerr.NewInvalidArgument("empty Rev2"),
		},
		{
			desc: "BaseWithRevisions Base is empty",
			request: &gitalypb.RangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RangeDiffRequest_BaseWithRevisions{
					BaseWithRevisions: &gitalypb.BaseWithRevisions{
						Rev1: "fake",
						Rev2: "fake",
						Base: "",
					},
				},
			},
			expectedErr: structerr.NewInvalidArgument("empty Base"),
		},
	}
	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			stream, err := client.RangeDiff(ctx, tc.request)
			require.NoError(t, err)
			testhelper.RequireGrpcError(t, tc.expectedErr, drainRangeDiffResponse(stream))
		})
	}
}

func drainRangeDiffResponse(c gitalypb.DiffService_RangeDiffClient) error {
	for {
		_, err := c.Recv()
		if err != nil {
			return err
		}
	}
}

func assertExactReceivedRangeDiffs(t *testing.T, client gitalypb.DiffService_RangeDiffClient, expectedRangeDiffs []*rangediff.CommitPair) {
	t.Helper()

	require.Equal(t, expectedRangeDiffs, getRangeDiffsFromCommitDiffClient(t, client))
}

func getRangeDiffsFromCommitDiffClient(t *testing.T, client gitalypb.DiffService_RangeDiffClient) []*rangediff.CommitPair {
	var commitPairs []*rangediff.CommitPair
	var currentCommitPair *rangediff.CommitPair

	for {
		fetchedRangeDiff, err := client.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)

		if currentCommitPair == nil {
			currentCommitPair = &rangediff.CommitPair{
				FromCommit:         fetchedRangeDiff.FromCommitId,
				ToCommit:           fetchedRangeDiff.ToCommitId,
				Comparison:         fetchedRangeDiff.Comparison,
				CommitMessageTitle: fetchedRangeDiff.CommitMessageTitle,
				PatchData:          fetchedRangeDiff.PatchData,
			}
		} else {
			currentCommitPair.PatchData = append(currentCommitPair.PatchData, fetchedRangeDiff.PatchData...)
		}

		if fetchedRangeDiff.EndOfPatch {
			commitPairs = append(commitPairs, currentCommitPair)
			currentCommitPair = nil
		}
	}

	return commitPairs
}

func getRangeDiffNoObjectID() string {
	var objectIDSize int
	if gittest.ObjectHashIsSHA256() {
		objectIDSize = sha256.Size * 2
	} else {
		objectIDSize = sha1.Size * 2
	}
	return strings.Repeat("-", objectIDSize)
}
