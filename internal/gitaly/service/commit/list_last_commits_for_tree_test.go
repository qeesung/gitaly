//go:build !gitaly_test_sha256

package commit

import (
	"io"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

type commitInfo struct {
	path []byte
	id   string
}

func TestSuccessfulListLastCommitsForTreeRequest(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupCommitServiceWithRepo(ctx, t)

	testCases := []struct {
		desc     string
		revision string
		path     []byte
		info     []commitInfo
		limit    int32
		offset   int32
	}{
		{
			desc:     "path is '/'",
			revision: "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
			path:     []byte("/"),
			info: []commitInfo{
				{
					path: []byte("encoding"),
					id:   "913c66a37b4a45b9769037c55c2d238bd0942d2e",
				},
				{
					path: []byte("files"),
					id:   "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
				},
				{
					path: []byte(".gitignore"),
					id:   "c1acaa58bbcbc3eafe538cb8274ba387047b69f8",
				},
				{
					path: []byte(".gitmodules"),
					id:   "6f6d7e7ed97bb5f0054f2b1df789b39ca89b6ff9",
				},
				{
					path: []byte("CHANGELOG"),
					id:   "913c66a37b4a45b9769037c55c2d238bd0942d2e",
				},
				{
					path: []byte("CONTRIBUTING.md"),
					id:   "6d394385cf567f80a8fd85055db1ab4c5295806f",
				},
				{
					path: []byte("Gemfile.zip"),
					id:   "ae73cb07c9eeaf35924a10f713b364d32b2dd34f",
				},
				{
					path: []byte("LICENSE"),
					id:   "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
				},
				{
					path: []byte("MAINTENANCE.md"),
					id:   "913c66a37b4a45b9769037c55c2d238bd0942d2e",
				},
				{
					path: []byte("PROCESS.md"),
					id:   "913c66a37b4a45b9769037c55c2d238bd0942d2e",
				},
				{
					path: []byte("README.md"),
					id:   "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863",
				},
				{
					path: []byte("VERSION"),
					id:   "913c66a37b4a45b9769037c55c2d238bd0942d2e",
				},
				{
					path: []byte("gitlab-shell"),
					id:   "6f6d7e7ed97bb5f0054f2b1df789b39ca89b6ff9",
				},
				{
					path: []byte("six"),
					id:   "cfe32cf61b73a0d5e9f13e774abde7ff789b1660",
				},
				{
					path: []byte("*"),
					id:   "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
				},
			},
			limit:  25,
			offset: 0,
		},
		{
			desc:     "path is 'files/'",
			revision: "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
			path:     []byte("files/"),
			info: []commitInfo{
				{
					path: []byte("files/html"),
					id:   "913c66a37b4a45b9769037c55c2d238bd0942d2e",
				},
				{
					path: []byte("files/images"),
					id:   "2f63565e7aac07bcdadb654e253078b727143ec4",
				},
				{
					path: []byte("files/js"),
					id:   "913c66a37b4a45b9769037c55c2d238bd0942d2e",
				},
				{
					path: []byte("files/markdown"),
					id:   "913c66a37b4a45b9769037c55c2d238bd0942d2e",
				},
				{
					path: []byte("files/ruby"),
					id:   "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
				},
				{
					path: []byte("files/*"),
					id:   "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
				},
			},
			limit:  25,
			offset: 0,
		},
		{
			desc:     "with offset higher than number of paths",
			revision: "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
			path:     []byte("/"),
			info:     []commitInfo{},
			limit:    25,
			offset:   14,
		},
		{
			desc:     "with limit 1",
			revision: "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
			path:     []byte("/"),
			info: []commitInfo{
				{
					path: []byte("encoding"),
					id:   "913c66a37b4a45b9769037c55c2d238bd0942d2e",
				},
			},
			limit:  1,
			offset: 0,
		},
		{
			desc:     "with offset 13",
			revision: "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
			path:     []byte("/"),
			info: []commitInfo{
				{
					path: []byte("six"),
					id:   "cfe32cf61b73a0d5e9f13e774abde7ff789b1660",
				},
			},
			limit:  25,
			offset: 13,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			request := &gitalypb.ListLastCommitsForTreeRequest{
				Repository: repo,
				Revision:   testCase.revision,
				Path:       testCase.path,
				Limit:      testCase.limit,
				Offset:     testCase.offset,
			}

			stream, err := client.ListLastCommitsForTree(ctx, request)
			require.NoError(t, err)

			counter := 0
			for {
				fetchedCommits, err := stream.Recv()
				if err == io.EOF {
					break
				}

				require.NoError(t, err)

				commits := fetchedCommits.GetCommits()

				for _, fetchedCommit := range commits {
					expectedInfo := testCase.info[counter]

					require.Equal(t, expectedInfo.path, fetchedCommit.PathBytes)
					require.Equal(t, expectedInfo.id, fetchedCommit.Commit.Id)

					counter++
				}
			}
		})
	}
}

func TestFailedListLastCommitsForTreeRequest(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, _, client := setupCommitServiceWithRepo(ctx, t)

	invalidRepo := &gitalypb.Repository{StorageName: "broken", RelativePath: "path"}

	testCases := []struct {
		desc    string
		request *gitalypb.ListLastCommitsForTreeRequest
		code    codes.Code
	}{
		{
			desc: "Revision is missing",
			request: &gitalypb.ListLastCommitsForTreeRequest{
				Repository: repo,
				Path:       []byte("/"),
				Revision:   "",
				Offset:     0,
				Limit:      25,
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "Invalid repository",
			request: &gitalypb.ListLastCommitsForTreeRequest{
				Repository: invalidRepo,
				Path:       []byte("/"),
				Revision:   "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
				Offset:     0,
				Limit:      25,
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "Repository is nil",
			request: &gitalypb.ListLastCommitsForTreeRequest{
				Path:     []byte("/"),
				Revision: "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
				Offset:   0,
				Limit:    25,
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "Revision is missing",
			request: &gitalypb.ListLastCommitsForTreeRequest{
				Repository: repo,
				Path:       []byte("/"),
				Offset:     0,
				Limit:      25,
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "Ambiguous revision",
			request: &gitalypb.ListLastCommitsForTreeRequest{
				Repository: repo,
				Revision:   "a",
				Offset:     0,
				Limit:      25,
			},
			code: codes.Internal,
		},
		{
			desc: "Invalid revision",
			request: &gitalypb.ListLastCommitsForTreeRequest{
				Repository: repo,
				Revision:   "--output=/meow",
				Offset:     0,
				Limit:      25,
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "Negative offset",
			request: &gitalypb.ListLastCommitsForTreeRequest{
				Repository: repo,
				Revision:   "--output=/meow",
				Offset:     -1,
				Limit:      25,
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "Negative limit",
			request: &gitalypb.ListLastCommitsForTreeRequest{
				Repository: repo,
				Revision:   "--output=/meow",
				Offset:     0,
				Limit:      -1,
			},
			code: codes.InvalidArgument,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			stream, err := client.ListLastCommitsForTree(ctx, testCase.request)
			require.NoError(t, err)

			_, err = stream.Recv()
			testhelper.RequireGrpcCode(t, err, testCase.code)
		})
	}
}

func TestNonUtf8ListLastCommitsForTreeRequest(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, repoPath, client := setupCommitServiceWithRepo(ctx, t)

	// This is an arbitrary blob known to exist in the test repository
	const blobID = "c60514b6d3d6bf4bec1030f70026e34dfbd69ad5"

	nonUTF8Filename := "hello\x80world"
	require.False(t, utf8.ValidString(nonUTF8Filename))

	commitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithTreeEntries(gittest.TreeEntry{
			Mode: "100644", Path: nonUTF8Filename, OID: blobID,
		}),
	)

	request := &gitalypb.ListLastCommitsForTreeRequest{
		Repository: repo,
		Revision:   commitID.String(),
		Limit:      100,
		Offset:     0,
	}

	stream, err := client.ListLastCommitsForTree(ctx, request)
	require.NoError(t, err)

	assert.True(t, fileExistsInCommits(t, stream, nonUTF8Filename))
}

func TestSuccessfulListLastCommitsForTreeRequestWithGlobCharacters(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, repoPath, client := setupCommitServiceWithRepo(ctx, t)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(gittest.TreeEntry{
		Path: ":wq", Mode: "040000", OID: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
			{Path: "README.md", Mode: "100644", Content: "something"},
		}),
	}))

	t.Run("with literal pathspecs", func(t *testing.T) {
		stream, err := client.ListLastCommitsForTree(ctx, &gitalypb.ListLastCommitsForTreeRequest{
			Repository:    repo,
			Revision:      commitID.String(),
			Path:          []byte(":wq"),
			GlobalOptions: &gitalypb.GlobalOptions{LiteralPathspecs: true},
			Limit:         100,
		})
		require.NoError(t, err)
		require.Equal(t, []string{":wq"}, fetchCommitPaths(t, stream))
	})

	t.Run("without literal pathspecs", func(t *testing.T) {
		stream, err := client.ListLastCommitsForTree(ctx, &gitalypb.ListLastCommitsForTreeRequest{
			Repository:    repo,
			Revision:      commitID.String(),
			Path:          []byte(":wq"),
			GlobalOptions: &gitalypb.GlobalOptions{LiteralPathspecs: false},
			Limit:         100,
		})
		require.NoError(t, err)
		require.Nil(t, fetchCommitPaths(t, stream))
	})
}

func fileExistsInCommits(t *testing.T, stream gitalypb.CommitService_ListLastCommitsForTreeClient, path string) bool {
	t.Helper()

	for _, commitPath := range fetchCommitPaths(t, stream) {
		if commitPath == path {
			return true
		}
	}

	return false
}

func fetchCommitPaths(t *testing.T, stream gitalypb.CommitService_ListLastCommitsForTreeClient) []string {
	t.Helper()

	var files []string
	for {
		response, err := stream.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		for _, commit := range response.GetCommits() {
			files = append(files, string(commit.PathBytes))
		}
	}

	return files
}
