package repository

import (
	"io"
	"os"
	"path"
	"testing"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/streamio"
	"google.golang.org/grpc/codes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"github.com/stretchr/testify/require"
)

func TestSuccessfullRestoreCustomHooksRequest(t *testing.T) {
	server, serverSocketPath := runRepoServer(t)
	defer server.Stop()

	client, conn := newRepositoryClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)

	defer cleanupFn()

	stream, err := client.RestoreCustomHooks(ctx)

	require.NoError(t, err)

	repoPath, err := helper.GetPath(testRepo)
	require.NoError(t, err)
	defer os.RemoveAll(repoPath)
	request := &pb.RestoreCustomHooksRequest{Repository: testRepo}
	writer := streamio.NewWriter(func(p []byte) error {
		request.Data = p
		if err := stream.Send(request); err != nil {
			return err
		}

		request = &pb.RestoreCustomHooksRequest{}
		return nil
	})

	file, err := os.Open("testdata/custom_hooks.tar")
	require.NoError(t, err)
	defer file.Close()

	_, err = io.Copy(writer, file)
	require.NoError(t, err)
	_, err = stream.CloseAndRecv()
	require.NoError(t, err)

	testhelper.MustRunCommand(t, nil, "ls", "-l", path.Join(repoPath, "custom_hooks/pre-push.sample"))
}

func TestFailedRestoreCustomHooksDueToValidations(t *testing.T) {
	server, serverSocketPath := runRepoServer(t)
	defer server.Stop()

	client, conn := newRepositoryClient(t, serverSocketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()

	stream, err := client.RestoreCustomHooks(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&pb.RestoreCustomHooksRequest{}))

	_, err = stream.CloseAndRecv()
	testhelper.AssertGrpcError(t, err, codes.InvalidArgument, "")
}
