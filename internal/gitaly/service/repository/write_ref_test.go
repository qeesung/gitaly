//go:build !gitaly_test_sha256

package repository

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

func TestWriteRef_successful(t *testing.T) {
	t.Parallel()

	txManager := transaction.NewTrackingManager()
	cfg, repo, repoPath, client := setupRepositoryService(testhelper.Context(t), t, testserver.WithTransactionManager(txManager))

	testCases := []struct {
		desc          string
		req           *gitalypb.WriteRefRequest
		expectedVotes int
	}{
		{
			desc: "shell update HEAD to refs/heads/master",
			req: &gitalypb.WriteRefRequest{
				Repository: repo,
				Ref:        []byte("HEAD"),
				Revision:   []byte("refs/heads/master"),
			},
			expectedVotes: 2,
		},
		{
			desc: "shell update refs/heads/master",
			req: &gitalypb.WriteRefRequest{
				Repository: repo,
				Ref:        []byte("refs/heads/master"),
				Revision:   []byte("b83d6e391c22777fca1ed3012fce84f633d7fed0"),
			},
			expectedVotes: 2,
		},
		{
			desc: "shell update refs/heads/master w/ validation",
			req: &gitalypb.WriteRefRequest{
				Repository:  repo,
				Ref:         []byte("refs/heads/master"),
				Revision:    []byte("498214de67004b1da3d820901307bed2a68a8ef6"),
				OldRevision: []byte("b83d6e391c22777fca1ed3012fce84f633d7fed0"),
			},
			expectedVotes: 2,
		},
		{
			desc: "race-free creation of reference",
			req: &gitalypb.WriteRefRequest{
				Repository:  repo,
				Ref:         []byte("refs/heads/branch"),
				Revision:    []byte("498214de67004b1da3d820901307bed2a68a8ef6"),
				OldRevision: []byte("0000000000000000000000000000000000000000"),
			},
			expectedVotes: 2,
		},
		{
			desc: "race-free delete of reference",
			req: &gitalypb.WriteRefRequest{
				Repository:  repo,
				Ref:         []byte("refs/heads/branch"),
				Revision:    []byte("0000000000000000000000000000000000000000"),
				OldRevision: []byte("498214de67004b1da3d820901307bed2a68a8ef6"),
			},
			expectedVotes: 2,
		},
	}

	ctx, err := txinfo.InjectTransaction(testhelper.Context(t), 1, "node", true)
	require.NoError(t, err)
	ctx = metadata.IncomingToOutgoing(ctx)

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			txManager.Reset()
			_, err = client.WriteRef(ctx, tc.req)
			require.NoError(t, err)

			require.Len(t, txManager.Votes(), tc.expectedVotes)

			if bytes.Equal(tc.req.Ref, []byte("HEAD")) {
				content := testhelper.MustReadFile(t, filepath.Join(repoPath, "HEAD"))

				refRevision := bytes.Join([][]byte{[]byte("ref: "), tc.req.Revision, []byte("\n")}, nil)

				require.EqualValues(t, refRevision, content)
				return
			}

			revParseCmd := gittest.NewCommand(t, cfg, "-C", repoPath, "rev-parse", "--verify", string(tc.req.Ref))
			output, err := revParseCmd.CombinedOutput()

			if git.ObjectHashSHA1.IsZeroOID(git.ObjectID(tc.req.GetRevision())) {
				// If the new OID is the all-zeroes object ID then it indicates we
				// should delete the branch. It's thus expected to get an error when
				// we try to resolve the reference here given that it should not
				// exist anymore.
				require.Error(t, err)
				require.Equal(t, "fatal: Needed a single revision\n", string(output))
			} else {
				require.NoError(t, err)
				require.Equal(t, string(tc.req.Revision), text.ChompBytes(output))
			}
		})
	}
}

func TestWriteRef_validation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupRepositoryService(ctx, t)

	testCases := []struct {
		desc string
		req  *gitalypb.WriteRefRequest
	}{
		{
			desc: "empty revision",
			req: &gitalypb.WriteRefRequest{
				Repository: repo,
				Ref:        []byte("refs/heads/master"),
			},
		},
		{
			desc: "empty ref name",
			req: &gitalypb.WriteRefRequest{
				Repository: repo,
				Revision:   []byte("498214de67004b1da3d820901307bed2a68a8ef6"),
			},
		},
		{
			desc: "non-prefixed ref name for shell",
			req: &gitalypb.WriteRefRequest{
				Repository: repo,
				Ref:        []byte("master"),
				Revision:   []byte("498214de67004b1da3d820901307bed2a68a8ef6"),
			},
		},
		{
			desc: "revision contains \\x00",
			req: &gitalypb.WriteRefRequest{
				Repository: repo,
				Ref:        []byte("refs/heads/master"),
				Revision:   []byte("012301230123\x001243"),
			},
		},
		{
			desc: "ref contains \\x00",
			req: &gitalypb.WriteRefRequest{
				Repository: repo,
				Ref:        []byte("refs/head\x00s/master\x00"),
				Revision:   []byte("0123012301231243"),
			},
		},
		{
			desc: "ref contains whitespace",
			req: &gitalypb.WriteRefRequest{
				Repository: repo,
				Ref:        []byte("refs/heads /master"),
				Revision:   []byte("0123012301231243"),
			},
		},
		{
			desc: "invalid revision",
			req: &gitalypb.WriteRefRequest{
				Repository: repo,
				Ref:        []byte("refs/heads/master"),
				Revision:   []byte("--output=/meow"),
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.WriteRef(ctx, tc.req)

			testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
		})
	}
}

func TestWriteRef_missingRevisions(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRepositoryServiceWithoutRepo(t)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)
	commitID := gittest.WriteCommit(t, cfg, repoPath)

	for _, tc := range []struct {
		desc        string
		request     *gitalypb.WriteRefRequest
		expectedErr error
	}{
		{
			desc: "revision refers to missing reference",
			request: &gitalypb.WriteRefRequest{
				Repository: repo,
				Ref:        []byte("refs/heads/main"),
				Revision:   []byte("refs/heads/missing"),
			},
			expectedErr: helper.ErrInternalf("resolving new revision: reference not found"),
		},
		{
			desc: "revision refers to missing object",
			request: &gitalypb.WriteRefRequest{
				Repository: repo,
				Ref:        []byte("refs/heads/main"),
				Revision:   bytes.Repeat([]byte("1"), gittest.DefaultObjectHash.EncodedLen()),
			},
			expectedErr: helper.ErrInternalf("resolving new revision: reference not found"),
		},
		{
			desc: "old revision refers to missing reference",
			request: &gitalypb.WriteRefRequest{
				Repository:  repo,
				Ref:         []byte("refs/heads/main"),
				Revision:    []byte(commitID),
				OldRevision: bytes.Repeat([]byte("1"), gittest.DefaultObjectHash.EncodedLen()),
			},
			expectedErr: helper.ErrInternalf("resolving old revision: reference not found"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.WriteRef(ctx, tc.request)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}
