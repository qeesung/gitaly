//go:build !gitaly_test_sha256

package commit

import (
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestCheckObjectsExist(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupCommitService(ctx, t)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)

	commitID1 := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch("master"), gittest.WithMessage("commit-1"),
	)
	commitID2 := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch("feature"), gittest.WithMessage("commit-2"), gittest.WithParents(commitID1),
	)
	commitID3 := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("commit-3"), gittest.WithParents(commitID1),
	)

	for _, tc := range []struct {
		desc            string
		requests        []*gitalypb.CheckObjectsExistRequest
		expectedResults map[string]bool
		expectedErr     error
	}{
		{
			desc:     "no requests",
			requests: []*gitalypb.CheckObjectsExistRequest{},
			// Ideally, we'd return an invalid-argument error in case there aren't any
			// requests. We can't do this though as this would diverge from Praefect's
			// behaviour, which always returns `io.EOF`.
			expectedResults: map[string]bool{},
		},
		{
			desc: "missing repository",
			requests: []*gitalypb.CheckObjectsExistRequest{
				{
					Revisions: [][]byte{},
				},
			},
			expectedErr: func() error {
				if testhelper.IsPraefectEnabled() {
					return helper.ErrInvalidArgumentf("repo scoped: empty Repository")
				}
				return helper.ErrInvalidArgumentf("empty Repository")
			}(),
			expectedResults: map[string]bool{},
		},
		{
			desc: "request without revisions",
			requests: []*gitalypb.CheckObjectsExistRequest{
				{
					Repository: repo,
				},
			},
			expectedResults: map[string]bool{},
		},
		{
			desc: "commit ids and refs that exist",
			requests: []*gitalypb.CheckObjectsExistRequest{
				{
					Repository: repo,
					Revisions: [][]byte{
						[]byte(commitID1),
						[]byte("master"),
						[]byte(commitID2),
						[]byte(commitID3),
						[]byte("feature"),
					},
				},
			},
			expectedResults: map[string]bool{
				commitID1.String(): true,
				"master":           true,
				commitID2.String(): true,
				commitID3.String(): true,
				"feature":          true,
			},
		},
		{
			desc: "ref and objects missing",
			requests: []*gitalypb.CheckObjectsExistRequest{
				{
					Repository: repo,
					Revisions: [][]byte{
						[]byte(commitID1),
						[]byte("master"),
						[]byte(commitID2),
						[]byte(commitID3),
						[]byte("feature"),
						[]byte("refs/does/not/exist"),
					},
				},
			},
			expectedResults: map[string]bool{
				commitID1.String():    true,
				"master":              true,
				commitID2.String():    true,
				commitID3.String():    true,
				"feature":             true,
				"refs/does/not/exist": false,
			},
		},
		{
			desc: "chunked input",
			requests: []*gitalypb.CheckObjectsExistRequest{
				{
					Repository: repo,
					Revisions: [][]byte{
						[]byte(commitID1),
					},
				},
				{
					Revisions: [][]byte{
						[]byte(commitID2),
					},
				},
				{
					Revisions: [][]byte{
						[]byte("refs/does/not/exist"),
					},
				},
				{
					Revisions: [][]byte{
						[]byte(commitID3),
					},
				},
			},
			expectedResults: map[string]bool{
				commitID1.String():    true,
				commitID2.String():    true,
				commitID3.String():    true,
				"refs/does/not/exist": false,
			},
		},
		{
			desc: "invalid input",
			requests: []*gitalypb.CheckObjectsExistRequest{
				{
					Repository: repo,
					Revisions: [][]byte{
						[]byte("-not-a-rev"),
					},
				},
			},
			expectedErr:     helper.ErrInvalidArgumentf("invalid revision %q: revision can't start with '-'", "-not-a-rev"),
			expectedResults: map[string]bool{},
		},
		{
			desc: "input with whitespace",
			requests: []*gitalypb.CheckObjectsExistRequest{
				{
					Repository: repo,
					Revisions: [][]byte{
						[]byte(fmt.Sprintf("%s\n%s", commitID1, commitID2)),
					},
				},
			},
			expectedErr:     helper.ErrInvalidArgumentf("invalid revision %q: revision can't contain whitespace", fmt.Sprintf("%s\n%s", commitID1, commitID2)),
			expectedResults: map[string]bool{},
		},
		{
			desc: "chunked invalid input",
			requests: []*gitalypb.CheckObjectsExistRequest{
				{
					Repository: repo,
					Revisions: [][]byte{
						[]byte(commitID1),
					},
				},
				{
					Revisions: [][]byte{
						[]byte("-not-a-rev"),
					},
				},
			},
			expectedErr:     helper.ErrInvalidArgumentf("invalid revision %q: revision can't start with '-'", "-not-a-rev"),
			expectedResults: map[string]bool{},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			client, err := client.CheckObjectsExist(ctx)
			require.NoError(t, err)

			for _, request := range tc.requests {
				require.NoError(t, client.Send(request))
			}
			require.NoError(t, client.CloseSend())

			results := map[string]bool{}
			for {
				var response *gitalypb.CheckObjectsExistResponse
				response, err = client.Recv()
				if err != nil {
					break
				}

				for _, revision := range response.GetRevisions() {
					results[string(revision.GetName())] = revision.GetExists()
				}
			}

			if tc.expectedErr == nil {
				tc.expectedErr = io.EOF
			}

			testhelper.RequireGrpcError(t, tc.expectedErr, err)
			require.Equal(t, tc.expectedResults, results)
		})
	}
}
