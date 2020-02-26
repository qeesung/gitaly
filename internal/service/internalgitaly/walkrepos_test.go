package internalgitaly

import (
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestWalkRepos(t *testing.T) {
	server, serverSocketPath := runInternalGitalyServer(t)
	defer server.Stop()

	client, conn := newInternalGitalyClient(t, serverSocketPath)
	defer conn.Close()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	ctx, cancel := testhelper.Context()
	defer cancel()

	stream, err := client.WalkRepos(ctx, &gitalypb.WalkReposRequest{
		StorageName: config.Config.Storages[0].Name,
	})
	require.NoError(t, err)

	actualRepos := consumeWalkReposStream(t, stream)
	require.Contains(t, actualRepos, testRepo.GetRelativePath())
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
