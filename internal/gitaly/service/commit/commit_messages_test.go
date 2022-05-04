package commit

import (
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v14/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

func TestSuccessfulGetCommitMessagesRequest(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, repoPath, client := setupCommitServiceWithRepo(ctx, t)

	message1 := strings.Repeat("a\n", helper.MaxCommitOrTagMessageSize*2)
	message2 := strings.Repeat("b\n", helper.MaxCommitOrTagMessageSize*2)

	commit1ID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch("local-big-commits"), gittest.WithMessage(message1),
	)
	commit2ID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch("local-big-commits"), gittest.WithMessage(message2),
		gittest.WithParents(commit1ID),
	)

	request := &gitalypb.GetCommitMessagesRequest{
		Repository: repo,
		CommitIds:  []string{commit1ID.String(), commit2ID.String()},
	}

	c, err := client.GetCommitMessages(ctx, request)
	require.NoError(t, err)

	expectedMessages := []*gitalypb.GetCommitMessagesResponse{
		{
			CommitId: commit1ID.String(),
			Message:  []byte(message1),
		},
		{
			CommitId: commit2ID.String(),
			Message:  []byte(message2),
		},
	}
	fetchedMessages := readAllMessagesFromClient(t, c)

	require.Len(t, fetchedMessages, len(expectedMessages))
	testhelper.ProtoEqual(t, expectedMessages[0], fetchedMessages[0])
	testhelper.ProtoEqual(t, expectedMessages[1], fetchedMessages[1])
}

func TestFailedGetCommitMessagesRequest(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, _, _, client := setupCommitServiceWithRepo(ctx, t)

	testCases := []struct {
		desc    string
		request *gitalypb.GetCommitMessagesRequest
		code    codes.Code
	}{
		{
			desc: "empty Repository",
			request: &gitalypb.GetCommitMessagesRequest{
				Repository: nil,
				CommitIds:  []string{"5937ac0a7beb003549fc5fd26fc247adbce4a52e"},
			},
			code: codes.InvalidArgument,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			c, err := client.GetCommitMessages(ctx, testCase.request)
			require.NoError(t, err)

			for {
				_, err = c.Recv()
				if err != nil {
					break
				}
			}

			testhelper.RequireGrpcCode(t, err, testCase.code)
		})
	}
}

func readAllMessagesFromClient(t *testing.T, c gitalypb.CommitService_GetCommitMessagesClient) (messages []*gitalypb.GetCommitMessagesResponse) {
	t.Helper()

	for {
		resp, err := c.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		if resp.CommitId != "" {
			messages = append(messages, resp)
			// first message contains a chunk of the message, so no need to append anything
			continue
		}

		currentMessage := messages[len(messages)-1]
		currentMessage.Message = append(currentMessage.Message, resp.Message...)
	}

	return
}
