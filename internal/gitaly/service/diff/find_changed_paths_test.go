package diff

import (
	"io"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFindChangedPathsRequest_success(t *testing.T) {
	ctx := testhelper.Context(t)
	_, repo, _, client := setupDiffService(ctx, t)

	testCases := []struct {
		desc          string
		commits       []string
		expectedPaths []*gitalypb.ChangedPaths
	}{
		{
			"Returns the expected results without a merge commit",
			[]string{"e4003da16c1c2c3fc4567700121b17bf8e591c6c", "57290e673a4c87f51294f5216672cbc58d485d25", "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab", "d59c60028b053793cecfb4022de34602e1a9218e"},
			[]*gitalypb.ChangedPaths{
				{
					Status: gitalypb.ChangedPaths_MODIFIED,
					Path:   []byte("CONTRIBUTING.md"),
				},
				{
					Status: gitalypb.ChangedPaths_MODIFIED,
					Path:   []byte("MAINTENANCE.md"),
				},
				{
					Status: gitalypb.ChangedPaths_ADDED,
					Path:   []byte("gitaly/テスト.txt"),
				},
				{
					Status: gitalypb.ChangedPaths_ADDED,
					Path:   []byte("gitaly/deleted-file"),
				},
				{
					Status: gitalypb.ChangedPaths_ADDED,
					Path:   []byte("gitaly/file-with-multiple-chunks"),
				},
				{
					Status: gitalypb.ChangedPaths_ADDED,
					Path:   []byte("gitaly/mode-file"),
				},
				{
					Status: gitalypb.ChangedPaths_ADDED,
					Path:   []byte("gitaly/mode-file-with-mods"),
				},
				{
					Status: gitalypb.ChangedPaths_ADDED,
					Path:   []byte("gitaly/named-file"),
				},
				{
					Status: gitalypb.ChangedPaths_ADDED,
					Path:   []byte("gitaly/named-file-with-mods"),
				},
				{
					Status: gitalypb.ChangedPaths_DELETED,
					Path:   []byte("files/js/commit.js.coffee"),
				},
			},
		},
		{
			"Returns the expected results with a merge commit",
			[]string{"7975be0116940bf2ad4321f79d02a55c5f7779aa", "55bc176024cfa3baaceb71db584c7e5df900ea65"},
			[]*gitalypb.ChangedPaths{
				{
					Status: gitalypb.ChangedPaths_ADDED,
					Path:   []byte("files/images/emoji.png"),
				},
				{
					Status: gitalypb.ChangedPaths_MODIFIED,
					Path:   []byte(".gitattributes"),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			rpcRequest := &gitalypb.FindChangedPathsRequest{Repository: repo, Commits: tc.commits}

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
		desc    string
		repo    *gitalypb.Repository
		commits []string
		err     error
	}{
		{
			desc:    "Repo not found",
			repo:    &gitalypb.Repository{StorageName: repo.GetStorageName(), RelativePath: "bar.git"},
			commits: []string{"e4003da16c1c2c3fc4567700121b17bf8e591c6c", "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"},
			err:     status.Errorf(codes.NotFound, "GetRepoPath: not a git repository: %q", filepath.Join(cfg.Storages[0].Path, "bar.git")),
		},
		{
			desc:    "Storage not found",
			repo:    &gitalypb.Repository{StorageName: "foo", RelativePath: "bar.git"},
			commits: []string{"e4003da16c1c2c3fc4567700121b17bf8e591c6c", "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"},
			err:     status.Error(codes.InvalidArgument, "GetStorageByName: no such storage: \"foo\""),
		},
		{
			desc:    "Commits cannot contain an empty commit",
			repo:    repo,
			commits: []string{""},
			err:     status.Error(codes.InvalidArgument, "FindChangedPaths: commits cannot contain an empty commit"),
		},
		{
			desc:    "Invalid commit",
			repo:    repo,
			commits: []string{"invalidinvalidinvalid", "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"},
			err:     status.Error(codes.NotFound, "FindChangedPaths: commit: invalidinvalidinvalid can not be found"),
		},
		{
			desc:    "Commit not found",
			repo:    repo,
			commits: []string{"z4003da16c1c2c3fc4567700121b17bf8e591c6c", "8a0f2ee90d940bfb0ba1e14e8214b0649056e4ab"},
			err:     status.Error(codes.NotFound, "FindChangedPaths: commit: z4003da16c1c2c3fc4567700121b17bf8e591c6c can not be found"),
		},
	}

	for _, tc := range tests {
		rpcRequest := &gitalypb.FindChangedPathsRequest{Repository: tc.repo, Commits: tc.commits}
		stream, err := client.FindChangedPaths(ctx, rpcRequest)
		require.NoError(t, err)

		t.Run(tc.desc, func(t *testing.T) {
			_, err := stream.Recv()
			testhelper.RequireGrpcError(t, tc.err, err)
		})
	}
}

func TestFindChangedPathsBetweenCommitsRequest_success(t *testing.T) {
	ctx := testhelper.Context(t)
	_, repo, _, client := setupDiffService(ctx, t)

	testCases := []struct {
		desc          string
		leftCommit    string
		rightCommit   string
		expectedPaths []*gitalypb.ChangedPaths
	}{
		{
			desc:        "Returns the expected results between distant commits",
			leftCommit:  "54fcc214b94e78d7a41a9a8fe6d87a5e59500e51",
			rightCommit: "5b4bb08538b9249995b94aa69121365ba9d28082",
			expectedPaths: []*gitalypb.ChangedPaths{
				{
					Status: gitalypb.ChangedPaths_DELETED,
					Path:   []byte("CONTRIBUTING.md"),
				},
				{
					Status: gitalypb.ChangedPaths_ADDED,
					Path:   []byte("NEW_FILE.md"),
				},
				{
					Status: gitalypb.ChangedPaths_MODIFIED,
					Path:   []byte("README.md"),
				},
			},
		},
		{
			desc:        "Returns the expected results when a file is renamed",
			leftCommit:  "e63f41fe459e62e1228fcef60d7189127aeba95a",
			rightCommit: "94bb47ca1297b7b3731ff2a36923640991e9236f",
			expectedPaths: []*gitalypb.ChangedPaths{
				{
					Status: gitalypb.ChangedPaths_DELETED,
					Path:   []byte("CHANGELOG"),
				},
				{
					Status: gitalypb.ChangedPaths_ADDED,
					Path:   []byte("CHANGELOG.md"),
				},
			},
		},
		{
			desc:        "Returns the expected results with diverging commits",
			leftCommit:  "5b4bb08538b9249995b94aa69121365ba9d28082",
			rightCommit: "f0f390655872bb2772c85a0128b2fbc2d88670cb",
			expectedPaths: []*gitalypb.ChangedPaths{
				{
					Status: gitalypb.ChangedPaths_ADDED,
					Path:   []byte("CONTRIBUTING.md"),
				},
				{
					Status: gitalypb.ChangedPaths_MODIFIED,
					Path:   []byte("NEW_FILE.md"),
				},
				{
					Status: gitalypb.ChangedPaths_MODIFIED,
					Path:   []byte("README.md"),
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			rpcRequest := &gitalypb.FindChangedPathsBetweenCommitsRequest{Repository: repo, LeftCommitId: tc.leftCommit, RightCommitId: tc.rightCommit}

			stream, err := client.FindChangedPathsBetweenCommits(ctx, rpcRequest)
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

func TestFindChangedPathsBetweenCommitsRequest_failing(t *testing.T) {
	ctx := testhelper.Context(t)
	cfg, repo, _, client := setupDiffService(ctx, t, testserver.WithDisablePraefect())

	tests := []struct {
		desc        string
		repo        *gitalypb.Repository
		leftCommit  string
		rightCommit string
		err         error
	}{
		{
			desc:        "Repo not found",
			repo:        &gitalypb.Repository{StorageName: repo.GetStorageName(), RelativePath: "bar.git"},
			leftCommit:  "742518b2be68fc750bb4c357c0df821a88113286",
			rightCommit: "e4003da16c1c2c3fc4567700121b17bf8e591c6c",
			err:         status.Errorf(codes.NotFound, "GetRepoPath: not a git repository: %q", filepath.Join(cfg.Storages[0].Path, "bar.git")),
		},
		{
			desc:        "Storage not found",
			repo:        &gitalypb.Repository{StorageName: "foo", RelativePath: "bar.git"},
			leftCommit:  "742518b2be68fc750bb4c357c0df821a88113286",
			rightCommit: "e4003da16c1c2c3fc4567700121b17bf8e591c6c",
			err:         status.Error(codes.InvalidArgument, "GetStorageByName: no such storage: \"foo\""),
		},
		{
			desc:        "Left commit cannot be an empty commit",
			repo:        repo,
			leftCommit:  "",
			rightCommit: "e4003da16c1c2c3fc4567700121b17bf8e591c6c",
			err:         status.Error(codes.InvalidArgument, "FindChangedPathsBetweenCommits: commits cannot contain an empty commit"),
		},
		{
			desc:        "Right commit cannot be an empty commit",
			repo:        repo,
			leftCommit:  "742518b2be68fc750bb4c357c0df821a88113286",
			rightCommit: "",
			err:         status.Error(codes.InvalidArgument, "FindChangedPathsBetweenCommits: commits cannot contain an empty commit"),
		},
		{
			desc:        "Invalid left commit",
			repo:        repo,
			leftCommit:  "invalidinvalidinvalid",
			rightCommit: "e4003da16c1c2c3fc4567700121b17bf8e591c6c",
			err:         status.Error(codes.NotFound, "FindChangedPathsBetweenCommits: commit: invalidinvalidinvalid can not be found"),
		},
		{
			desc:        "Invalid right commit",
			repo:        repo,
			leftCommit:  "742518b2be68fc750bb4c357c0df821a88113286",
			rightCommit: "invalidinvalidinvalid",
			err:         status.Error(codes.NotFound, "FindChangedPathsBetweenCommits: commit: invalidinvalidinvalid can not be found"),
		},
		{
			desc:        "Left commit not found",
			repo:        repo,
			leftCommit:  "z4003da16c1c2c3fc4567700121b17bf8e591c6c",
			rightCommit: "e4003da16c1c2c3fc4567700121b17bf8e591c6c",
			err:         status.Error(codes.NotFound, "FindChangedPathsBetweenCommits: commit: z4003da16c1c2c3fc4567700121b17bf8e591c6c can not be found"),
		},
		{
			desc:        "Right commit not found",
			repo:        repo,
			leftCommit:  "742518b2be68fc750bb4c357c0df821a88113286",
			rightCommit: "z4003da16c1c2c3fc4567700121b17bf8e591c6c",
			err:         status.Error(codes.NotFound, "FindChangedPathsBetweenCommits: commit: z4003da16c1c2c3fc4567700121b17bf8e591c6c can not be found"),
		},
	}

	for _, tc := range tests {
		rpcRequest := &gitalypb.FindChangedPathsBetweenCommitsRequest{Repository: tc.repo, LeftCommitId: tc.leftCommit, RightCommitId: tc.rightCommit}
		stream, err := client.FindChangedPathsBetweenCommits(ctx, rpcRequest)
		require.NoError(t, err)

		t.Run(tc.desc, func(t *testing.T) {
			_, err := stream.Recv()
			testhelper.RequireGrpcError(t, tc.err, err)
		})
	}
}
