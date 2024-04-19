package objectpool

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDelete(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repoProto, _, _, client := setup(t, ctx)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	repositoryClient := gitalypb.NewRepositoryServiceClient(extractConn(client))

	poolProto, _, _ := createObjectPool(t, ctx, cfg, repoProto)
	validPoolPath := poolProto.GetRepository().GetRelativePath()

	errWithTransactions := func() error {
		// With WAL enabled, the transaction fails to begin leading to a different error message. However if Praefect
		// is also enabled, Praefect intercepts the call, and return invalid pool directory error due to not finding
		// metadata for the pool repository.
		if testhelper.IsWALEnabled() && !testhelper.IsPraefectEnabled() {
			return status.Error(codes.Internal, "begin transaction: get partition: get partition ID: validate git directory: invalid git directory")
		}

		return errInvalidPoolDir
	}

	for _, tc := range []struct {
		desc         string
		noPool       bool
		relativePath string
		expectedErr  error
	}{
		{
			desc:   "no pool in request fails",
			noPool: true,
			expectedErr: testhelper.GitalyOrPraefect(
				structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
				structerr.NewInvalidArgument("no object pool repository"),
			),
		},
		{
			desc:         "deleting outside pools directory fails",
			relativePath: ".",
			expectedErr:  errWithTransactions(),
		},
		{
			desc:         "deleting pools directory fails",
			relativePath: "@pools",
			expectedErr:  errWithTransactions(),
		},
		{
			desc:         "deleting first level subdirectory fails",
			relativePath: "@pools/ab",
			expectedErr:  errInvalidPoolDir,
		},
		{
			desc:         "deleting second level subdirectory fails",
			relativePath: "@pools/ab/cd",
			expectedErr:  errInvalidPoolDir,
		},
		{
			desc:         "deleting pool subdirectory fails",
			relativePath: filepath.Join(validPoolPath, "objects"),
			expectedErr:  errWithTransactions(),
		},
		{
			desc:         "path traversing fails",
			relativePath: validPoolPath + "/../../../../..",
			expectedErr: testhelper.GitalyOrPraefect(
				testhelper.WithInterceptedMetadata(
					structerr.NewInvalidArgument("%w", storage.ErrRelativePathEscapesRoot),
					"relative_path", validPoolPath+"/../../../../..",
				),
				errInvalidPoolDir,
			),
		},
		{
			desc:         "deleting pool succeeds",
			relativePath: validPoolPath,
		},
		{
			desc:         "deleting non-existent pool succeeds",
			relativePath: gittest.NewObjectPoolName(t),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			request := &gitalypb.DeleteObjectPoolRequest{ObjectPool: &gitalypb.ObjectPool{
				Repository: &gitalypb.Repository{
					StorageName:  repo.GetStorageName(),
					RelativePath: tc.relativePath,
				},
			}}

			if tc.noPool {
				request.ObjectPool = nil
			}

			_, err := client.DeleteObjectPool(ctx, request)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)

			response, err := repositoryClient.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{
				Repository: poolProto.GetRepository(),
			})
			require.NoError(t, err)
			require.Equal(t, tc.expectedErr != nil, response.GetExists())
		})
	}
}
