package internalgitaly

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWalkRepos(t *testing.T) {
	server, serverSocketPath := runInternalGitalyServer(t)
	defer server.Stop()

	client, conn := newInternalGitalyClient(t, serverSocketPath)
	defer conn.Close()

	testRepo1, _, cleanupFn1 := testhelper.NewTestRepo(t)
	defer cleanupFn1()

	testRepo2, _, cleanupFn2 := testhelper.NewTestRepo(t)
	defer cleanupFn2()

	ctx, cancel := testhelper.Context()
	defer cancel()

	stream, err := client.WalkRepos(ctx, &gitalypb.WalkReposRequest{
		StorageName: "invalid storage name",
	})
	require.NoError(t, err)

	_, err = stream.Recv()
	require.NotNil(t, err)
	s, ok := status.FromError(err)
	require.True(t, ok)
	require.Equal(t, codes.NotFound, s.Code())

	stream, err = client.WalkRepos(ctx, &gitalypb.WalkReposRequest{
		StorageName: config.Config.Storages[0].Name,
	})
	require.NoError(t, err)

	actualRepos := consumeWalkReposStream(t, stream)
	require.Contains(t, actualRepos, testRepo1.GetRelativePath())
	require.Contains(t, actualRepos, testRepo2.GetRelativePath())
}

func consumeWalkReposStream(t *testing.T, stream gitalypb.InternalGitaly_WalkReposClient) []string {
	var repos []string
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		} else {
			require.NoError(t, err)
		}
		repos = append(repos, resp.RelativePath)
	}
	return repos
}
