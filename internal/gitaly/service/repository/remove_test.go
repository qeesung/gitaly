package repository

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRemoveRepository(t *testing.T) {
	t.Parallel()

	cfg, client := setupRepositoryServiceWithoutRepo(t)
	ctx := testhelper.Context(t)

	repo := &gitalypb.Repository{
		StorageName:  cfg.Storages[0].Name,
		RelativePath: git.NewRepositoryName(t),
	}

	_, err := client.CreateRepository(ctx, &gitalypb.CreateRepositoryRequest{
		Repository: repo,
	})
	require.NoError(t, err)

	_, err = client.RemoveRepository(ctx, &gitalypb.RemoveRepositoryRequest{
		Repository: repo,
	})
	require.NoError(t, err)

	require.NoFileExists(t, filepath.Join(cfg.Storages[0].Path, repo.RelativePath))
}

func TestRemoveRepository_doesNotExist(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, client := setupRepositoryServiceWithoutRepo(t)

	_, err := client.RemoveRepository(ctx, &gitalypb.RemoveRepositoryRequest{
		Repository: &gitalypb.Repository{StorageName: cfg.Storages[0].Name, RelativePath: "/does/not/exist"},
	})
	testhelper.RequireGrpcError(t, structerr.NewNotFound("repository does not exist"), err)
}

func TestRemoveRepository_validate(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)
	_, client := setupRepositoryServiceWithoutRepo(t)
	_, err := client.RemoveRepository(ctx, &gitalypb.RemoveRepositoryRequest{Repository: nil})
	testhelper.RequireGrpcError(t, status.Error(codes.InvalidArgument, testhelper.GitalyOrPraefect(
		"empty Repository",
		"missing repository",
	)), err)
}

func TestRemoveRepository_locking(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	// Praefect does not acquire a lock on repository deletion so disable the test case for Praefect.
	cfg, client := setupRepositoryServiceWithoutRepo(t, testserver.WithDisablePraefect())
	repo, repoPath := git.CreateRepository(t, ctx, cfg)

	// Simulate a concurrent RPC holding the repository lock.
	lockPath := repoPath + ".lock"
	require.NoError(t, os.WriteFile(lockPath, []byte{}, 0o644))
	defer func() { require.NoError(t, os.RemoveAll(lockPath)) }()

	_, err := client.RemoveRepository(ctx, &gitalypb.RemoveRepositoryRequest{Repository: repo})
	testhelper.RequireGrpcError(t, structerr.NewFailedPrecondition("repository is already locked"), err)
}
