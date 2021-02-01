package hook

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
)

func TestPackObjectsInvalidArgument(t *testing.T) {
	serverSocketPath, stop := runHooksServer(t, config.Config)
	defer stop()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	stream, err := client.PackObjectsHook(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&gitalypb.PackObjectsHookRequest{}), "empty repository should result in an error")
	_, err = stream.Recv()

	testhelper.RequireGrpcError(t, err, codes.InvalidArgument)
}

func TestPackObjectsCommandValidation(t *testing.T) {
	serverSocketPath, stop := runHooksServer(t, config.Config)
	defer stop()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	testCases := []struct {
		args  []string
		valid bool
	}{
		{[]string{"pack-objects"}, true},
		{[]string{"rm", "-rf"}, false},
	}

	for _, tc := range testCases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			stream, err := client.PackObjectsHook(ctx)
			require.NoError(t, err)

			require.NoError(t, stream.Send(&gitalypb.PackObjectsHookRequest{
				Repository: testRepo,
				Args:       tc.args,
			}))
			resp, err := stream.Recv()
			require.NoError(t, err, "response")

			require.Equal(t, tc.valid, resp.CommandAccepted, "command accepted")
		})
	}
}

func TestPackObjectsSuccess(t *testing.T) {
	serverSocketPath, stop := runHooksServer(t, config.Config)
	defer stop()

	client, conn := newHooksClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	testRepo, testRepoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	stdin := `3dd08961455abf80ef9115f4afdc1c6f968b503c
--not

`
	args := []string{"pack-objects", "--revs", "--thin", "--stdout", "--progress", "--delta-base-offset"}

	stream, err := client.PackObjectsHook(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&gitalypb.PackObjectsHookRequest{
		Repository: testRepo,
		Args:       args,
	}))
	resp, err := stream.Recv()
	require.NoError(t, err, "response")

	require.True(t, resp.CommandAccepted, "command accepted")

	require.NoError(t, stream.Send(&gitalypb.PackObjectsHookRequest{
		Stdin: []byte(stdin),
	}), "send stdin")
	require.NoError(t, stream.CloseSend(), "close send")

	var stdout []byte
	for err == nil {
		resp, err = stream.Recv()
		stdout = append(stdout, resp.GetStdout()...)
		if stderr := resp.GetStderr(); len(stderr) > 0 {
			t.Log(string(stderr))
		}
	}
	require.Equal(t, io.EOF, err)

	testhelper.MustRunCommand(t, bytes.NewReader(stdout), "git", "-C", testRepoPath, "index-pack", "--stdin", "--fix-thin")
}
