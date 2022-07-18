//go:build !gitaly_test_sha256

package diff

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
)

func TestFindChangedPathsRequest_success(t *testing.T) {
	ctx := testhelper.Context(t)
	_, repo, _, client := setupDiffService(ctx, t)

	testCases := []struct {
		desc          string
		commits       []string
		requests      []*gitalypb.FindChangedPathsRequest_Request
		expectedPaths []*gitalypb.ChangedPaths
	}{
		{
			desc:    "Returns the expected results without a merge commit",
			commits: []string{"e4003da16c1c2c3fc4567700121b17bf8e591c6c", "57290e673a4c87f51294f5216672cbc58d485d25", "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab", "d59c60028b053793cecfb4022de34602e1a9218e"},
			expectedPaths: []*gitalypb.ChangedPaths{
				{
					Status:  gitalypb.ChangedPaths_MODIFIED,
					Path:    []byte("CONTRIBUTING.md"),
					OldMode: 0o100644,
					NewMode: 0o100644,
				},
				{
					Status:  gitalypb.ChangedPaths_MODIFIED,
					Path:    []byte("MAINTENANCE.md"),
					OldMode: 0o100644,
					NewMode: 0o100644,
				},
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("gitaly/テスト.txt"),
					OldMode: 0o000000,
					NewMode: 0o100755,
				},
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("gitaly/deleted-file"),
					OldMode: 0o000000,
					NewMode: 0o100644,
				},
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("gitaly/file-with-multiple-chunks"),
					OldMode: 0o000000,
					NewMode: 0o100644,
				},
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("gitaly/mode-file"),
					OldMode: 0o000000,
					NewMode: 0o100644,
				},
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("gitaly/mode-file-with-mods"),
					OldMode: 0o000000,
					NewMode: 0o100644,
				},
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("gitaly/named-file"),
					OldMode: 0o000000,
					NewMode: 0o100644,
				},
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("gitaly/named-file-with-mods"),
					OldMode: 0o000000,
					NewMode: 0o100644,
				},
				{
					Status:  gitalypb.ChangedPaths_DELETED,
					Path:    []byte("files/js/commit.js.coffee"),
					OldMode: 0o100644,
					NewMode: 0o000000,
				},
			},
		},
		{
			desc:    "Returns the expected results with a merge commit",
			commits: []string{"7975be0116940bf2ad4321f79d02a55c5f7779aa", "55bc176024cfa3baaceb71db584c7e5df900ea65"},
			expectedPaths: []*gitalypb.ChangedPaths{
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("files/images/emoji.png"),
					OldMode: 0o000000,
					NewMode: 0o100644,
				},
				{
					Status:  gitalypb.ChangedPaths_MODIFIED,
					Path:    []byte(".gitattributes"),
					OldMode: 0o100644,
					NewMode: 0o100644,
				},
			},
		},
		{
			desc: "Commit request without parents uses actual parents",
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: "5b4bb08538b9249995b94aa69121365ba9d28082",
						},
					},
				},
			},
			expectedPaths: []*gitalypb.ChangedPaths{
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("NEW_FILE.md"),
					OldMode: 0o000000,
					NewMode: 0o100644,
				},
			},
		},
		{
			desc: "Returns the expected results between distant commits",
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: "5b4bb08538b9249995b94aa69121365ba9d28082",
							ParentCommitRevisions: []string{
								"54fcc214b94e78d7a41a9a8fe6d87a5e59500e51",
							},
						},
					},
				},
			},
			expectedPaths: []*gitalypb.ChangedPaths{
				{
					Status:  gitalypb.ChangedPaths_DELETED,
					Path:    []byte("CONTRIBUTING.md"),
					OldMode: 0o100644,
					NewMode: 0o000000,
				},
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("NEW_FILE.md"),
					OldMode: 0o000000,
					NewMode: 0o100644,
				},
				{
					Status:  gitalypb.ChangedPaths_MODIFIED,
					Path:    []byte("README.md"),
					OldMode: 0o100644,
					NewMode: 0o100644,
				},
			},
		},
		{
			desc: "Returns the expected results when a file is renamed",
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: "94bb47ca1297b7b3731ff2a36923640991e9236f",
							ParentCommitRevisions: []string{
								"e63f41fe459e62e1228fcef60d7189127aeba95a",
							},
						},
					},
				},
			},
			expectedPaths: []*gitalypb.ChangedPaths{
				{
					Status:  gitalypb.ChangedPaths_DELETED,
					Path:    []byte("CHANGELOG"),
					OldMode: 0o100644,
					NewMode: 0o000000,
				},
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("CHANGELOG.md"),
					OldMode: 0o000000,
					NewMode: 0o100644,
				},
			},
		},
		{
			desc: "Returns the expected results with diverging commits",
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: "f0f390655872bb2772c85a0128b2fbc2d88670cb",
							ParentCommitRevisions: []string{
								"5b4bb08538b9249995b94aa69121365ba9d28082",
							},
						},
					},
				},
			},
			expectedPaths: []*gitalypb.ChangedPaths{
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("CONTRIBUTING.md"),
					OldMode: 0o000000,
					NewMode: 0o100644,
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
			},
		},
		{
			desc: "Returns the expected results with trees",
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_TreeRequest_{
						TreeRequest: &gitalypb.FindChangedPathsRequest_Request_TreeRequest{
							LeftTreeRevision:  "05b1cb35ce45609df9de644c62db980a4b6e8814",
							RightTreeRevision: "7b06af2882bea3c0433955883bb65217256a634e",
						},
					},
				},
			},
			expectedPaths: []*gitalypb.ChangedPaths{
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
			},
		},
		{
			desc: "Returns the expected results when multiple parent commits are specified",
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: "5b4bb08538b9249995b94aa69121365ba9d28082",
							ParentCommitRevisions: []string{
								"5d03ab53225e8d8fe4f0597c70fc21c6542a7a10",
								"f0f390655872bb2772c85a0128b2fbc2d88670cb",
							},
						},
					},
				},
			},
			expectedPaths: []*gitalypb.ChangedPaths{
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
			},
		},
		{
			desc: "Returns the expected results with multiple requests",
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_TreeRequest_{
						TreeRequest: &gitalypb.FindChangedPathsRequest_Request_TreeRequest{
							LeftTreeRevision:  "05b1cb35ce45609df9de644c62db980a4b6e8814",
							RightTreeRevision: "7b06af2882bea3c0433955883bb65217256a634e",
						},
					},
				},
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: "5b4bb08538b9249995b94aa69121365ba9d28082",
							ParentCommitRevisions: []string{
								"5d03ab53225e8d8fe4f0597c70fc21c6542a7a10",
								"f0f390655872bb2772c85a0128b2fbc2d88670cb",
							},
						},
					},
				},
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_TreeRequest_{
						TreeRequest: &gitalypb.FindChangedPathsRequest_Request_TreeRequest{
							LeftTreeRevision:  "05b1cb35ce45609df9de644c62db980a4b6e8814",
							RightTreeRevision: "7b06af2882bea3c0433955883bb65217256a634e",
						},
					},
				},
			},
			expectedPaths: []*gitalypb.ChangedPaths{
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
			},
		},
		{
			desc: "Returns the expected results with refs and tags as commits",
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: "v1.0.0",
							ParentCommitRevisions: []string{
								"v1.0.0^^",
							},
						},
					},
				},
			},
			expectedPaths: []*gitalypb.ChangedPaths{
				{
					Status:  gitalypb.ChangedPaths_DELETED,
					Path:    []byte(".DS_Store"),
					OldMode: 0o100644,
					NewMode: 0o000000,
				},
				{
					Status:  gitalypb.ChangedPaths_MODIFIED,
					Path:    []byte(".gitmodules"),
					OldMode: 0o100644,
					NewMode: 0o100644,
				},
				{
					Status:  gitalypb.ChangedPaths_DELETED,
					Path:    []byte("files/.DS_Store"),
					OldMode: 0o100644,
					NewMode: 0o000000,
				},
				{
					Status:  gitalypb.ChangedPaths_ADDED,
					Path:    []byte("gitlab-shell"),
					OldMode: 0o000000,
					NewMode: 0o160000,
				},
			},
		},
		{
			desc: "Returns the expected results with commits as trees",
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_TreeRequest_{
						TreeRequest: &gitalypb.FindChangedPathsRequest_Request_TreeRequest{
							LeftTreeRevision:  "54fcc214b94e78d7a41a9a8fe6d87a5e59500e51",
							RightTreeRevision: "be93687618e4b132087f430a4d8fc3a609c9b77c",
						},
					},
				},
			},
			expectedPaths: []*gitalypb.ChangedPaths{
				{
					Status:  gitalypb.ChangedPaths_DELETED,
					Path:    []byte("README"),
					OldMode: 0o100644,
					NewMode: 0o000000,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			rpcRequest := &gitalypb.FindChangedPathsRequest{Repository: repo, Commits: tc.commits, Requests: tc.requests}

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

			require.Equal(t, tc.expectedPaths, paths)
		})
	}
}

func TestFindChangedPathsRequest_failing(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg, repo, _, client := setupDiffService(ctx, t, testserver.WithDisablePraefect())

	tests := []struct {
		desc     string
		repo     *gitalypb.Repository
		commits  []string
		requests []*gitalypb.FindChangedPathsRequest_Request
		err      error
	}{
		{
			desc:    "Repo not found",
			repo:    &gitalypb.Repository{StorageName: repo.GetStorageName(), RelativePath: "bar.git"},
			commits: []string{"e4003da16c1c2c3fc4567700121b17bf8e591c6c", "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"},
			err:     helper.ErrNotFoundf("GetRepoPath: not a git repository: %q", filepath.Join(cfg.Storages[0].Path, "bar.git")),
		},
		{
			desc:    "Storage not found",
			repo:    &gitalypb.Repository{StorageName: "foo", RelativePath: "bar.git"},
			commits: []string{"e4003da16c1c2c3fc4567700121b17bf8e591c6c", "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"},
			err:     helper.ErrInvalidArgumentf("GetStorageByName: no such storage: \"foo\""),
		},
		{
			desc:    "Commits cannot contain an empty commit",
			repo:    repo,
			commits: []string{""},
			err:     helper.ErrInvalidArgumentf("resolving commit: revision cannot be empty"),
		},
		{
			desc:    "Specifying both commits and requests",
			repo:    repo,
			commits: []string{"8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"},
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab",
						},
					},
				},
			},
			err: helper.ErrInvalidArgumentf("cannot specify both commits and requests"),
		},
		{
			desc:    "Invalid commit",
			repo:    repo,
			commits: []string{"invalidinvalidinvalid", "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"},
			err:     helper.ErrNotFoundf(`resolving commit: revision can not be found: "invalidinvalidinvalid"`),
		},
		{
			desc:    "Commit not found",
			repo:    repo,
			commits: []string{"z4003da16c1c2c3fc4567700121b17bf8e591c6c", "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"},
			err:     helper.ErrNotFoundf(`resolving commit: revision can not be found: "z4003da16c1c2c3fc4567700121b17bf8e591c6c"`),
		},
		{
			desc: "Tree object as commit",
			repo: repo,
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: "07f8147e8e73aab6c935c296e8cdc5194dee729b",
						},
					},
				},
			},
			err: helper.ErrNotFoundf(`resolving commit: revision can not be found: "07f8147e8e73aab6c935c296e8cdc5194dee729b"`),
		},
		{
			desc: "Tree object as parent commit",
			repo: repo,
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_CommitRequest_{
						CommitRequest: &gitalypb.FindChangedPathsRequest_Request_CommitRequest{
							CommitRevision: "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab",
							ParentCommitRevisions: []string{
								"07f8147e8e73aab6c935c296e8cdc5194dee729b",
							},
						},
					},
				},
			},
			err: helper.ErrNotFoundf(`resolving commit parent: revision can not be found: "07f8147e8e73aab6c935c296e8cdc5194dee729b"`),
		},
		{
			desc: "Blob object as left tree",
			repo: repo,
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_TreeRequest_{
						TreeRequest: &gitalypb.FindChangedPathsRequest_Request_TreeRequest{
							LeftTreeRevision:  "50b27c6518be44c42c4d87966ae2481ce895624c",
							RightTreeRevision: "07f8147e8e73aab6c935c296e8cdc5194dee729b",
						},
					},
				},
			},
			err: helper.ErrNotFoundf(`resolving left tree: revision can not be found: "50b27c6518be44c42c4d87966ae2481ce895624c"`),
		},
		{
			desc: "Blob object as right tree",
			repo: repo,
			requests: []*gitalypb.FindChangedPathsRequest_Request{
				{
					Type: &gitalypb.FindChangedPathsRequest_Request_TreeRequest_{
						TreeRequest: &gitalypb.FindChangedPathsRequest_Request_TreeRequest{
							LeftTreeRevision:  "07f8147e8e73aab6c935c296e8cdc5194dee729b",
							RightTreeRevision: "50b27c6518be44c42c4d87966ae2481ce895624c",
						},
					},
				},
			},
			err: helper.ErrNotFoundf(`resolving right tree: revision can not be found: "50b27c6518be44c42c4d87966ae2481ce895624c"`),
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
