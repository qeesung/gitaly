package diff

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/streamio"
)

func TestRawRawRangeDiff_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupDiffService(t)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

	blob1Data := bytes.Repeat([]byte("random string of text\n"), 3)
	blob1 := gittest.WriteBlob(t, cfg, repoPath, []byte("random string of text\n"))
	initCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob1},
	))

	blob2 := gittest.WriteBlob(t, cfg, repoPath, append(blob1Data, []byte("blob2\n")...))
	blob3 := gittest.WriteBlob(t, cfg, repoPath, append(blob1Data, []byte("blob3\n")...))

	commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob2}),
		gittest.WithParents(initCommit),
		gittest.WithMessage("test commit message title 1"))

	commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "foo", Mode: "100644", OID: blob3}),
		gittest.WithParents(initCommit),
		gittest.WithMessage("test commit message title 2"))

	stream, err := client.RawRangeDiff(ctx, &gitalypb.RawRangeDiffRequest{
		Repository: repoProto,
		RangeSpec: &gitalypb.RawRangeDiffRequest_RevisionRange{
			RevisionRange: &gitalypb.RevisionRange{
				Rev1: commit1.String(),
				Rev2: commit2.String(),
			},
		},
	})
	require.NoError(t, err)

	rawDiff, err := io.ReadAll(streamio.NewReader(func() ([]byte, error) {
		response, err := stream.Recv()
		return response.GetData(), err
	}))
	require.NoError(t, err)
	expected := fmt.Sprintf(`1:  %s ! 1:  %s test commit message title 1
    @@ Metadata
     Author: Scrooge McDuck <scrooge@mcduck.com>
     
      ## Commit message ##
    -    test commit message title 1
    +    test commit message title 2
     
      ## foo ##
     @@
      random string of text
     +random string of text
     +random string of text
    -+blob2
    ++blob3
`, commit1.String(), commit2.String())
	require.Equal(t, expected, string(rawDiff))
}

func TestRawRawRangeDiff_failures(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupDiffService(t)

	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	testCases := []struct {
		desc        string
		request     *gitalypb.RawRangeDiffRequest
		expectedErr error
	}{
		{
			desc: "RevisionRange Rev1 is empty",
			request: &gitalypb.RawRangeDiffRequest{
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
			request: &gitalypb.RawRangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RawRangeDiffRequest_RevisionRange{
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
			request: &gitalypb.RawRangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RawRangeDiffRequest_RevisionRange{
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
			request: &gitalypb.RawRangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RawRangeDiffRequest_RangePair{
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
			request: &gitalypb.RawRangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RawRangeDiffRequest_RangePair{
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
			request: &gitalypb.RawRangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RawRangeDiffRequest_BaseWithRevisions{
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
			request: &gitalypb.RawRangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RawRangeDiffRequest_BaseWithRevisions{
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
			request: &gitalypb.RawRangeDiffRequest{
				Repository: repo,
				RangeSpec: &gitalypb.RawRangeDiffRequest_BaseWithRevisions{
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
			stream, err := client.RawRangeDiff(ctx, tc.request)
			require.NoError(t, err)
			testhelper.RequireGrpcError(t, tc.expectedErr, drainRawRangeDiffResponse(stream))
		})
	}
}

func drainRawRangeDiffResponse(c gitalypb.DiffService_RawRangeDiffClient) error {
	for {
		_, err := c.Recv()
		if err != nil {
			return err
		}
	}
}
