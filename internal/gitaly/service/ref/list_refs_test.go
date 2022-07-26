//go:build !gitaly_test_sha256

package ref

import (
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

func TestServer_ListRefs(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg, _, _, client := setupRefService(ctx, t)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	oldCommitID := gittest.WriteCommit(t, cfg, repoPath)
	newCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(oldCommitID),
		gittest.WithAuthorDate(time.Date(2011, 2, 16, 14, 1, 0, 0, time.FixedZone("UTC+1", +1*60*60))),
	)

	for _, cmd := range [][]string{
		{"update-ref", "refs/heads/main", newCommitID.String()},
		{"tag", "lightweight-tag", newCommitID.String()},
		{"tag", "-m", "tag message", "annotated-tag", "refs/heads/main"},
		{"symbolic-ref", "refs/heads/symbolic", "refs/heads/main"},
		{"update-ref", "refs/remote/remote-name/remote-branch", newCommitID.String()},
		{"symbolic-ref", "HEAD", "refs/heads/main"},
		{"update-ref", "refs/heads/old", oldCommitID.String()},
	} {
		gittest.Exec(t, cfg, append([]string{"-C", repoPath}, cmd...)...)
	}

	annotatedTagOID := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "annotated-tag"))

	for _, tc := range []struct {
		desc              string
		request           *gitalypb.ListRefsRequest
		expectedGrpcError codes.Code
		expectedError     string
		expected          []*gitalypb.ListRefsResponse_Reference
	}{
		{
			desc: "no repo",
			request: &gitalypb.ListRefsRequest{
				Patterns: [][]byte{[]byte("refs/")},
			},
			expectedGrpcError: codes.InvalidArgument,
			expectedError:     "", // Ideally we would test the message but it changes when running through praefect
		},
		{
			desc: "no patterns",
			request: &gitalypb.ListRefsRequest{
				Repository: repo,
			},
			expectedGrpcError: codes.InvalidArgument,
			expectedError:     "rpc error: code = InvalidArgument desc = patterns must have at least one entry",
		},
		{
			desc: "bad sorting key",
			request: &gitalypb.ListRefsRequest{
				Repository: repo,
				Patterns:   [][]byte{[]byte("refs/")},
				SortBy: &gitalypb.ListRefsRequest_SortBy{
					Key: gitalypb.ListRefsRequest_SortBy_Key(100),
				},
			},
			expectedGrpcError: codes.InvalidArgument,
			expectedError:     `rpc error: code = InvalidArgument desc = sorting key "100" is not supported`,
		},
		{
			desc: "bad sorting direction",
			request: &gitalypb.ListRefsRequest{
				Repository: repo,
				Patterns:   [][]byte{[]byte("refs/")},
				SortBy: &gitalypb.ListRefsRequest_SortBy{
					Direction: gitalypb.SortDirection(100),
				},
			},
			expectedGrpcError: codes.InvalidArgument,
			expectedError:     "rpc error: code = InvalidArgument desc = sorting direction is not supported",
		},
		{
			desc: "not found",
			request: &gitalypb.ListRefsRequest{
				Repository: repo,
				Patterns:   [][]byte{[]byte("this-pattern-does-not-match-anything")},
			},
		},
		{
			desc: "not found and main",
			request: &gitalypb.ListRefsRequest{
				Repository: repo,
				Patterns: [][]byte{
					[]byte("this-pattern-does-not-match-anything"),
					[]byte("refs/heads/main"),
				},
			},
			expected: []*gitalypb.ListRefsResponse_Reference{
				{Name: []byte("refs/heads/main"), Target: newCommitID.String()},
			},
		},
		{
			desc: "all",
			request: &gitalypb.ListRefsRequest{
				Repository: repo,
				Patterns:   [][]byte{[]byte("refs/")},
			},
			expected: []*gitalypb.ListRefsResponse_Reference{
				{Name: []byte("refs/heads/main"), Target: newCommitID.String()},
				{Name: []byte("refs/heads/old"), Target: oldCommitID.String()},
				{Name: []byte("refs/heads/symbolic"), Target: newCommitID.String()},
				{Name: []byte("refs/remote/remote-name/remote-branch"), Target: newCommitID.String()},
				{Name: []byte("refs/tags/annotated-tag"), Target: annotatedTagOID},
				{Name: []byte("refs/tags/lightweight-tag"), Target: newCommitID.String()},
			},
		},
		{
			desc: "sort by authordate desc",
			request: &gitalypb.ListRefsRequest{
				Repository: repo,
				Patterns:   [][]byte{[]byte("refs/heads")},
				SortBy: &gitalypb.ListRefsRequest_SortBy{
					Direction: gitalypb.SortDirection_DESCENDING,
					Key:       gitalypb.ListRefsRequest_SortBy_AUTHORDATE,
				},
			},
			expected: []*gitalypb.ListRefsResponse_Reference{
				{Name: []byte("refs/heads/old"), Target: oldCommitID.String()},
				{Name: []byte("refs/heads/main"), Target: newCommitID.String()},
				{Name: []byte("refs/heads/symbolic"), Target: newCommitID.String()},
			},
		},
		{
			desc: "branches and tags only",
			request: &gitalypb.ListRefsRequest{
				Repository: repo,
				Patterns:   [][]byte{[]byte("refs/heads/*"), []byte("refs/tags/*")},
			},
			expected: []*gitalypb.ListRefsResponse_Reference{
				{Name: []byte("refs/heads/main"), Target: newCommitID.String()},
				{Name: []byte("refs/heads/old"), Target: oldCommitID.String()},
				{Name: []byte("refs/heads/symbolic"), Target: newCommitID.String()},
				{Name: []byte("refs/tags/annotated-tag"), Target: annotatedTagOID},
				{Name: []byte("refs/tags/lightweight-tag"), Target: newCommitID.String()},
			},
		},
		{
			desc: "head and branches and tags only",
			request: &gitalypb.ListRefsRequest{
				Repository: repo,
				Head:       true,
				Patterns:   [][]byte{[]byte("refs/heads/*"), []byte("refs/tags/*")},
			},
			expected: []*gitalypb.ListRefsResponse_Reference{
				{Name: []byte("HEAD"), Target: newCommitID.String()},
				{Name: []byte("refs/heads/main"), Target: newCommitID.String()},
				{Name: []byte("refs/heads/old"), Target: oldCommitID.String()},
				{Name: []byte("refs/heads/symbolic"), Target: newCommitID.String()},
				{Name: []byte("refs/tags/annotated-tag"), Target: annotatedTagOID},
				{Name: []byte("refs/tags/lightweight-tag"), Target: newCommitID.String()},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			c, err := client.ListRefs(ctx, tc.request)
			require.NoError(t, err)

			var refs []*gitalypb.ListRefsResponse_Reference
			for {
				r, err := c.Recv()
				if err == io.EOF {
					break
				}
				if tc.expectedError == "" && tc.expectedGrpcError == 0 {
					require.NoError(t, err)
				} else {
					if tc.expectedError != "" {
						require.EqualError(t, err, tc.expectedError)
					}

					if tc.expectedGrpcError != 0 {
						testhelper.RequireGrpcCode(t, err, tc.expectedGrpcError)
					}

					return
				}

				refs = append(refs, r.GetReferences()...)
			}

			testhelper.ProtoEqual(t, tc.expected, refs)
		})
	}
}
