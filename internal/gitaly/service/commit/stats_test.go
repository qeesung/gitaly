//go:build !gitaly_test_sha256

package commit

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestCommitStatsSuccess(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupCommitServiceWithRepo(ctx, t)

	tests := []struct {
		desc                 string
		revision             string
		oid                  string
		additions, deletions int32
	}{
		{
			desc:      "multiple changes, multiple files",
			revision:  "test-do-not-touch",
			oid:       "899d3d27b04690ac1cd9ef4d8a74fde0667c57f1",
			additions: 27,
			deletions: 59,
		},
		{
			desc:      "multiple changes, multiple files, reference by commit ID",
			revision:  "899d3d27b04690ac1cd9ef4d8a74fde0667c57f1",
			oid:       "899d3d27b04690ac1cd9ef4d8a74fde0667c57f1",
			additions: 27,
			deletions: 59,
		},
		{
			desc:      "merge commit",
			revision:  "60ecb67",
			oid:       "60ecb67744cb56576c30214ff52294f8ce2def98",
			additions: 1,
			deletions: 0,
		},
		{
			desc:      "binary file",
			revision:  "ae73cb0",
			oid:       "ae73cb07c9eeaf35924a10f713b364d32b2dd34f",
			additions: 0,
			deletions: 0,
		},
		{
			desc:      "initial commit",
			revision:  "1a0b36b3",
			oid:       "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
			additions: 43,
			deletions: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			resp, err := client.CommitStats(ctx, &gitalypb.CommitStatsRequest{
				Repository: repo,
				Revision:   []byte(tc.revision),
			})
			require.NoError(t, err)

			assert.Equal(t, tc.oid, resp.GetOid())
			assert.Equal(t, tc.additions, resp.GetAdditions())
			assert.Equal(t, tc.deletions, resp.GetDeletions())
		})
	}
}

func TestCommitStatsFailure(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, _, client := setupCommitServiceWithRepo(ctx, t)

	for _, tc := range []struct {
		desc        string
		request     *gitalypb.CommitStatsRequest
		expectedErr error
	}{
		{
			desc: "repo not found",
			request: &gitalypb.CommitStatsRequest{
				Repository: &gitalypb.Repository{
					StorageName:  repo.GetStorageName(),
					RelativePath: "bar.git",
				},
				Revision: []byte("test-do-not-touch"),
			},
			expectedErr: helper.ErrNotFoundf(gitalyOrPraefect(
				fmt.Sprintf("GetRepoPath: not a git repository: %q", filepath.Join(cfg.Storages[0].Path, "bar.git")),
				"accessor call: route repository accessor: consistent storages: repository \"default\"/\"bar.git\" not found",
			)),
		},
		{
			desc: "storage not found",
			request: &gitalypb.CommitStatsRequest{
				Repository: &gitalypb.Repository{
					StorageName:  "foo",
					RelativePath: "bar.git",
				},
				Revision: []byte("test-do-not-touch"),
			},
			expectedErr: helper.ErrInvalidArgumentf(gitalyOrPraefect(
				"GetStorageByName: no such storage: \"foo\"",
				"repo scoped: invalid Repository",
			)),
		},
		{
			desc: "ref not found",
			request: &gitalypb.CommitStatsRequest{
				Repository: repo,
				Revision:   []byte("non/existing"),
			},
			expectedErr: helper.ErrInternalf("object not found"),
		},
		{
			desc: "invalid revision",
			request: &gitalypb.CommitStatsRequest{
				Repository: repo,
				Revision:   []byte("--outpu=/meow"),
			},
			expectedErr: helper.ErrInvalidArgumentf("revision can't start with '-'"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.CommitStats(ctx, tc.request)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}
