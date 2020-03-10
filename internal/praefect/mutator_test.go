package praefect

import (
	"testing"

	"github.com/stretchr/testify/require"
	gitalyconfig "gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestRepoService_WriteRef_Success(t *testing.T) {
	conf := config.Config{
		VirtualStorages: []*config.VirtualStorage{
			{
				Name: "default",
				Nodes: []*models.Node{
					{
						Storage:        "praefect-internal-1",
						DefaultPrimary: true,
					},
					{
						Storage: "praefect-internal-2",
					},
					{
						Storage: "praefect-internal-3",
					},
				},
			},
		},
	}

	testRepo, testRepoPath, cleanup := testhelper.NewTestRepo(t)
	defer cleanup()

	newRev, _ := testhelper.CreateCommitOnNewBranch(t, testRepoPath)

	cleanupTempStoragePaths := createTempStoragePaths(t, conf.VirtualStorages[0].Nodes)
	defer cleanupTempStoragePaths()

	for _, node := range conf.VirtualStorages[0].Nodes {
		cloneRepoAtStorage(t, testRepo, node.Storage)
	}

	cc, nodeMgr, cleanup := runPraefectServerWithGitaly(t, conf)
	defer cleanup()

	shard, err := nodeMgr.GetShard("default")
	require.NoError(t, err)

	primary, err := shard.GetPrimary()
	require.NoError(t, err)

	ctx, cancel := testhelper.Context()
	defer cancel()

	ref := "refs/heads/master"
	branch, err := gitalypb.NewRefServiceClient(primary.GetConnection()).FindBranch(ctx, &gitalypb.FindBranchRequest{
		Repository: &gitalypb.Repository{
			StorageName:  primary.GetStorage(),
			RelativePath: testRepo.GetRelativePath(),
		},
		Name: []byte(ref),
	})
	require.NoError(t, err)

	oldRev := branch.Branch.TargetCommit.Id

	_, err = gitalypb.NewRepositoryServiceClient(cc).WriteRef(ctx, &gitalypb.WriteRefRequest{
		Repository:  testRepo,
		Ref:         []byte(ref),
		Revision:    []byte(newRev),
		OldRevision: []byte(oldRev),
		Force:       false,
	})
	require.NoError(t, err)

	secondaries, err := shard.GetSecondaries()
	require.NoError(t, err)

	for _, secondary := range secondaries {
		branch, err = gitalypb.NewRefServiceClient(secondary.GetConnection()).FindBranch(ctx, &gitalypb.FindBranchRequest{
			Repository: &gitalypb.Repository{
				StorageName:  primary.GetStorage(),
				RelativePath: testRepo.GetRelativePath(),
			},
			Name: []byte(ref),
		})
		require.Equal(t, newRev, branch.GetBranch().TargetCommit.Id)
	}
}

func createTempStoragePaths(t *testing.T, nodes []*models.Node) func() {
	oldStorages := gitalyconfig.Config.Storages

	var tempDirCleanups []func() error
	for _, node := range nodes {
		tempPath, cleanup := testhelper.TempDir(t, node.Storage)
		tempDirCleanups = append(tempDirCleanups, cleanup)

		gitalyconfig.Config.Storages = append(gitalyconfig.Config.Storages, gitalyconfig.Storage{
			Name: node.Storage,
			Path: tempPath,
		})
	}

	return func() {
		gitalyconfig.Config.Storages = oldStorages
		for _, tempDirCleanup := range tempDirCleanups {
			tempDirCleanup()
		}
	}
}
