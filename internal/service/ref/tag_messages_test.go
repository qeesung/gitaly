package ref

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

func TestSuccessfulGetTagMessagesRequest(t *testing.T) {
	server, serverSocketPath := runRefServiceServer(t)
	defer server.Stop()

	client, conn := newRefServiceClient(t, serverSocketPath)
	defer conn.Close()

	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	ctx, cancel := testhelper.Context()
	defer cancel()

	message1 := strings.Repeat("a", helper.MaxCommitOrTagMessageSize*2)
	message2 := strings.Repeat("b", helper.MaxCommitOrTagMessageSize)

	tag1ID := testhelper.CreateTag(t, testRepoPath, "big-tag-1", "master", &testhelper.CreateTagOpts{Message: message1})
	tag2ID := testhelper.CreateTag(t, testRepoPath, "big-tag-2", "master~", &testhelper.CreateTagOpts{Message: message2})

	request := &gitalypb.GetTagMessagesRequest{
		Repository: testRepo,
		TagIds:     []string{tag1ID, tag2ID},
	}

	expectedMessages := []*gitalypb.GetTagMessagesResponse{
		{
			TagId:   tag1ID,
			Message: []byte(message1 + "\n"),
		},
		{
			TagId:   tag2ID,
			Message: []byte(message2 + "\n"),
		},
	}

	for title, ctxModifier := range map[string]func(context.Context) context.Context{
		"enabled_feature_GetTagMessagesGo": func(ctx context.Context) context.Context {
			return enableGetTagMessagesFeatureFlag(ctx)
		},
		"disabled_feature_GetTagMessagesGo": func(ctx context.Context) context.Context {
			return ctx
		},
	} {
		t.Run(title, func(t *testing.T) {
			c, err := client.GetTagMessages(ctxModifier(ctx), request)
			require.NoError(t, err)

			fetchedMessages := readAllMessagesFromClient(t, c)

			require.Equal(t, expectedMessages, fetchedMessages)
		})
	}
}

func TestFailedGetTagMessagesRequest(t *testing.T) {
	server, serverSocketPath := runRefServiceServer(t)
	defer server.Stop()

	client, conn := newRefServiceClient(t, serverSocketPath)
	defer conn.Close()

	testCases := []struct {
		desc    string
		request *gitalypb.GetTagMessagesRequest
		code    codes.Code
	}{
		{
			desc: "empty Repository",
			request: &gitalypb.GetTagMessagesRequest{
				Repository: nil,
				TagIds:     []string{"5937ac0a7beb003549fc5fd26fc247adbce4a52e"},
			},
			code: codes.InvalidArgument,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			ctx, cancel := testhelper.Context()
			defer cancel()

			c, err := client.GetTagMessages(ctx, testCase.request)
			require.NoError(t, err)

			for {
				_, err = c.Recv()
				if err != nil {
					break
				}
			}

			testhelper.RequireGrpcError(t, err, testCase.code)
		})
	}
}

func readAllMessagesFromClient(t *testing.T, c gitalypb.RefService_GetTagMessagesClient) (messages []*gitalypb.GetTagMessagesResponse) {
	for {
		resp, err := c.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		if resp.TagId != "" {
			messages = append(messages, resp)
			// first message contains a chunk of the message, so no need to append anything
			continue
		}

		currentMessage := messages[len(messages)-1]
		currentMessage.Message = append(currentMessage.Message, resp.Message...)
	}

	return
}

func enableGetTagMessagesFeatureFlag(ctx context.Context) context.Context {
	return metadata.NewOutgoingContext(ctx, metadata.New(map[string]string{
		featureflag.HeaderKey(featureflag.GetTagMessagesGo): "true",
	}))
}
