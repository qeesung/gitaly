package praefect

import (
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	gconfig "gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestConsistencyCheck(t *testing.T) {
	oldStorages := gconfig.Config.Storages
	defer func() { gconfig.Config.Storages = oldStorages }()

	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "praefect",
				Nodes: []*models.Node{
					0: {
						DefaultPrimary: true,
						Storage:        "gitaly-0",
						Address:        "tcp::/this-doesnt-matter",
					},
					1: {
						Storage: "gitaly-1",
						Address: "tcp::/this-doesnt-matter",
					},
				},
			},
		},
	}

	virtualStorage := conf.VirtualStorages[0]
	primary := virtualStorage.Nodes[0]
	secondary := virtualStorage.Nodes[1]

	testStorages := []gconfig.Storage{
		{
			Name: virtualStorage.Nodes[0].Storage,
			Path: tempStoragePath(t),
		},
		{
			Name: virtualStorage.Nodes[1].Storage,
			Path: tempStoragePath(t),
		},
	}

	gconfig.Config.Storages = append(gconfig.Config.Storages, testStorages...)
	defer func() {
		for _, ts := range testStorages {
			require.NoError(t, os.RemoveAll(ts.Path))
		}
	}()

	repo0, _, cleanup0 := testhelper.NewTestRepo(t)
	defer cleanup0()

	_, _, cleanupReference := cloneRepoAtStorage(t, repo0, virtualStorage.Nodes[0].Storage)
	defer cleanupReference()

	_, targetRepoPath, cleanupTarget := cloneRepoAtStorage(t, repo0, virtualStorage.Nodes[1].Storage)
	defer cleanupTarget()

	cc, _, cleanup := runPraefectServerWithGitaly(t, conf)
	defer cleanup()

	praefectCli := gitalypb.NewPraefectInfoServiceClient(cc)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	reposAreConsistent := func() (consistent bool) {
		stream, err := praefectCli.ConsistencyCheck(ctx, &gitalypb.ConsistencyCheckRequest{
			VirtualStorage:   virtualStorage.Name,
			ReferenceStorage: primary.Storage,
			TargetStorage:    secondary.Storage,
		})
		require.NoError(t, err)

		responses := consumeConsistencyCheckResponses(t, stream)
		require.Len(t, responses, 1)

		resp := responses[0]
		require.Equal(t, repo0.RelativePath, resp.RepoRelativePath)

		consistent = resp.TargetChecksum == resp.ReferenceChecksum
		if !consistent {
			require.NotZero(t, resp.ReplJobId,
				"A replication job should be scheduled when inconsistent")
		}

		return consistent
	}

	require.True(t, reposAreConsistent(),
		"both repos expected to be consistent after initial clone")

	testhelper.MustRunCommand(t, nil, "git", "-C", targetRepoPath, "update-ref", "HEAD", "spooky-stuff")

	require.False(t, reposAreConsistent(),
		"repos should no longer be consistent after target HEAD changed")
}

func consumeConsistencyCheckResponses(t *testing.T, stream gitalypb.PraefectInfoService_ConsistencyCheckClient) []*gitalypb.ConsistencyCheckResponse {
	var responses []*gitalypb.ConsistencyCheckResponse
	for {
		resp, err := stream.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		responses = append(responses, resp)
	}
	return responses
}
