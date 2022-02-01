package commit

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

func createRepoWithDivergentBranches(t *testing.T, cfg config.Cfg, leftCommits, rightCommits int, leftBranchName, rightBranchName string) *gitalypb.Repository {
	/* create a branch structure as follows
	   	   a
	   	   |
	   	   b
	      / \
	     c   d
	     |   |
	     e   f
	     |   |
		 f   h
	*/

	repo, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

	mainCommitOID := createCommits(t, cfg, repoPath, "master", 2, "")
	createCommits(t, cfg, repoPath, leftBranchName, leftCommits, mainCommitOID)
	createCommits(t, cfg, repoPath, rightBranchName, rightCommits, mainCommitOID)

	return repo
}

func TestSuccessfulCountDivergentCommitsRequest(t *testing.T) {
	t.Parallel()
	cfg, client := setupCommitService(t)

	testRepo := createRepoWithDivergentBranches(t, cfg, 3, 3, "left", "right")

	testCases := []struct {
		leftRevision  string
		rightRevision string
		leftCount     int32
		rightCount    int32
	}{
		{
			leftRevision:  "left",
			rightRevision: "right",
			leftCount:     3,
			rightCount:    3,
		},
		{
			leftRevision:  "left^",
			rightRevision: "right",
			leftCount:     2,
			rightCount:    3,
		},
		{
			leftRevision:  "left",
			rightRevision: "right^",
			leftCount:     3,
			rightCount:    2,
		},
		{
			leftRevision:  "left^",
			rightRevision: "right^",
			leftCount:     2,
			rightCount:    2,
		},
		{
			leftRevision:  "master",
			rightRevision: "right",
			leftCount:     0,
			rightCount:    3,
		},
		{
			leftRevision:  "left",
			rightRevision: "master",
			leftCount:     3,
			rightCount:    0,
		},
		{
			leftRevision:  "master",
			rightRevision: "master",
			leftCount:     0,
			rightCount:    0,
		},
	}

	for _, testCase := range testCases {
		t.Run(fmt.Sprintf("%+v", testCase), func(t *testing.T) {
			request := &gitalypb.CountDivergingCommitsRequest{
				Repository: testRepo,
				From:       []byte(testCase.leftRevision),
				To:         []byte(testCase.rightRevision),
				MaxCount:   int32(1000),
			}
			ctx := testhelper.Context(t)
			response, err := client.CountDivergingCommits(ctx, request)
			require.NoError(t, err)
			assert.Equal(t, testCase.leftCount, response.GetLeftCount())
			assert.Equal(t, testCase.rightCount, response.GetRightCount())
		})
	}
}

func TestSuccessfulCountDivergentCommitsRequestWithMaxCount(t *testing.T) {
	t.Parallel()
	cfg, client := setupCommitService(t)

	testRepo := createRepoWithDivergentBranches(t, cfg, 4, 4, "left", "right")

	testCases := []struct {
		leftRevision  string
		rightRevision string
		maxCount      int
	}{
		{
			leftRevision:  "left",
			rightRevision: "right",
			maxCount:      2,
		},
		{
			leftRevision:  "left",
			rightRevision: "right",
			maxCount:      3,
		},
		{
			leftRevision:  "left",
			rightRevision: "right",
			maxCount:      4,
		},
	}

	for _, testCase := range testCases {
		t.Run(fmt.Sprintf("%+v", testCase), func(t *testing.T) {
			request := &gitalypb.CountDivergingCommitsRequest{
				Repository: testRepo,
				From:       []byte(testCase.leftRevision),
				To:         []byte(testCase.rightRevision),
				MaxCount:   int32(testCase.maxCount),
			}
			ctx := testhelper.Context(t)
			response, err := client.CountDivergingCommits(ctx, request)
			require.NoError(t, err)
			assert.Equal(t, testCase.maxCount, int(response.GetRightCount()+response.GetLeftCount()))
		})
	}
}

func TestFailedCountDivergentCommitsRequestDueToValidationError(t *testing.T) {
	t.Parallel()
	_, repo, _, client := setupCommitServiceWithRepo(t, true)

	revision := []byte("d42783470dc29fde2cf459eb3199ee1d7e3f3a72")

	rpcRequests := []*gitalypb.CountDivergingCommitsRequest{
		{Repository: &gitalypb.Repository{StorageName: "fake", RelativePath: "path"}, From: []byte("abcdef"), To: []byte("12345")}, // Repository doesn't exist
		{Repository: repo, From: nil, To: revision}, // From is empty
		{Repository: repo, From: revision, To: nil}, // To is empty
		{Repository: repo, From: nil, To: nil},      // From and To are empty
	}

	for _, rpcRequest := range rpcRequests {
		t.Run(fmt.Sprintf("%v", rpcRequest), func(t *testing.T) {
			ctx := testhelper.Context(t)
			_, err := client.CountDivergingCommits(ctx, rpcRequest)
			testhelper.RequireGrpcCode(t, err, codes.InvalidArgument)
		})
	}
}
