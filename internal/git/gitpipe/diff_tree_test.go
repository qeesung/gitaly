//go:build !gitaly_test_sha256

package gitpipe

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestDiffTree(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	repoProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
		SkipCreationViaService: true,
		Seed:                   gittest.SeedGitLabTest,
	})
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	for _, tc := range []struct {
		desc            string
		leftRevision    string
		rightRevision   string
		options         []DiffTreeOption
		expectedResults []RevisionResult
		expectedErr     error
	}{
		{
			desc:          "single file",
			leftRevision:  "b83d6e391c22777fca1ed3012fce84f633d7fed0",
			rightRevision: "4a24d82dbca5c11c61556f3b35ca472b7463187e",
			expectedResults: []RevisionResult{
				{
					OID:        "c60514b6d3d6bf4bec1030f70026e34dfbd69ad5",
					ObjectName: []byte("README.md"),
				},
			},
		},
		{
			desc:          "single file in subtree without recursive",
			leftRevision:  "7975be0116940bf2ad4321f79d02a55c5f7779aa",
			rightRevision: "1e292f8fedd741b75372e19097c76d327140c312",
			expectedResults: []RevisionResult{
				{
					OID:        "ceb102b8d3f9a95c2eb979213e49f7cc1b23d56e",
					ObjectName: []byte("files"),
				},
			},
		},
		{
			desc:          "single file in subtree with recursive",
			leftRevision:  "7975be0116940bf2ad4321f79d02a55c5f7779aa",
			rightRevision: "1e292f8fedd741b75372e19097c76d327140c312",
			options: []DiffTreeOption{
				DiffTreeWithRecursive(),
			},
			expectedResults: []RevisionResult{
				{
					OID:        "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391",
					ObjectName: []byte("files/flat/path/correct/content.txt"),
				},
			},
		},
		{
			desc:          "with submodules",
			leftRevision:  "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
			rightRevision: "5937ac0a7beb003549fc5fd26fc247adbce4a52e",
			expectedResults: []RevisionResult{
				{
					OID:        "efd587ccb47caf5f31fc954edb21f0a713d9ecc3",
					ObjectName: []byte(".gitmodules"),
				},
				{
					OID:        "645f6c4c82fd3f5e06f67134450a570b795e55a6",
					ObjectName: []byte("gitlab-grack"),
				},
			},
		},
		{
			desc:          "without submodules",
			leftRevision:  "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
			rightRevision: "5937ac0a7beb003549fc5fd26fc247adbce4a52e",
			options: []DiffTreeOption{
				DiffTreeWithIgnoreSubmodules(),
			},
			expectedResults: []RevisionResult{
				{
					OID:        "efd587ccb47caf5f31fc954edb21f0a713d9ecc3",
					ObjectName: []byte(".gitmodules"),
				},
			},
		},
		{
			desc:          "with skip function",
			leftRevision:  "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
			rightRevision: "5937ac0a7beb003549fc5fd26fc247adbce4a52e",
			options: []DiffTreeOption{
				DiffTreeWithSkip(func(r *RevisionResult) bool {
					return r.OID == "efd587ccb47caf5f31fc954edb21f0a713d9ecc3"
				}),
			},
			expectedResults: []RevisionResult{
				{
					OID:        "645f6c4c82fd3f5e06f67134450a570b795e55a6",
					ObjectName: []byte("gitlab-grack"),
				},
			},
		},
		{
			desc:          "invalid revision",
			leftRevision:  "refs/heads/master",
			rightRevision: "refs/heads/does-not-exist",
			expectedErr: errors.New("diff-tree pipeline command: exit status 128, stderr: " +
				"\"fatal: ambiguous argument 'refs/heads/does-not-exist': unknown revision or path not in the working tree.\\n" +
				"Use '--' to separate paths from revisions, like this:\\n" +
				"'git <command> [<revision>...] -- [<file>...]'\\n\""),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			it := DiffTree(ctx, repo, tc.leftRevision, tc.rightRevision, tc.options...)

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
			require.Equal(t, tc.expectedResults, results)
		})
	}
}
