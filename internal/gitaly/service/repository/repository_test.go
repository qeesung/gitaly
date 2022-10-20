//go:build !gitaly_test_sha256

package repository

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestRepositoryExists(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfgBuilder := testcfg.NewGitalyCfgBuilder(testcfg.WithStorages("default", "other", "broken"))
	cfg := cfgBuilder.Build(t)

	require.NoError(t, os.RemoveAll(cfg.Storages[2].Path), "third storage needs to be invalid")

	client, socketPath := runRepositoryService(t, cfg, nil, testserver.WithDisablePraefect())
	cfg.SocketPath = socketPath

	repo, _ := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
		Seed: gittest.SeedGitLabTest,
	})

	queries := []struct {
		desc      string
		request   *gitalypb.RepositoryExistsRequest
		errorCode codes.Code
		exists    bool
	}{
		{
			desc: "repository nil",
			request: &gitalypb.RepositoryExistsRequest{
				Repository: nil,
			},
			errorCode: codes.InvalidArgument,
		},
		{
			desc: "storage name empty",
			request: &gitalypb.RepositoryExistsRequest{
				Repository: &gitalypb.Repository{
					StorageName:  "",
					RelativePath: repo.GetRelativePath(),
				},
			},
			errorCode: codes.InvalidArgument,
		},
		{
			desc: "relative path empty",
			request: &gitalypb.RepositoryExistsRequest{
				Repository: &gitalypb.Repository{
					StorageName:  repo.GetStorageName(),
					RelativePath: "",
				},
			},
			errorCode: codes.InvalidArgument,
		},
		{
			desc: "exists true",
			request: &gitalypb.RepositoryExistsRequest{
				Repository: &gitalypb.Repository{
					StorageName:  repo.GetStorageName(),
					RelativePath: repo.GetRelativePath(),
				},
			},
			exists: true,
		},
		{
			desc: "exists false, wrong storage",
			request: &gitalypb.RepositoryExistsRequest{
				Repository: &gitalypb.Repository{
					StorageName:  "other",
					RelativePath: repo.GetRelativePath(),
				},
			},
			exists: false,
		},
		{
			desc: "storage directory does not exist",
			request: &gitalypb.RepositoryExistsRequest{
				Repository: &gitalypb.Repository{
					StorageName:  "broken",
					RelativePath: "foobar.git",
				},
			},
			errorCode: codes.NotFound,
		},
	}

	for _, tc := range queries {
		t.Run(tc.desc, func(t *testing.T) {
			response, err := client.RepositoryExists(ctx, tc.request)

			require.Equal(t, tc.errorCode, helper.GrpcCode(err))

			if err != nil {
				// Ignore the response message if there was an error
				return
			}

			require.Equal(t, tc.exists, response.Exists)
		})
	}
}

func TestSuccessfulHasLocalBranches(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, _, client := setupRepositoryService(t, ctx)

	emptyRepo, _ := gittest.CreateRepository(t, ctx, cfg)

	testCases := []struct {
		desc      string
		request   *gitalypb.HasLocalBranchesRequest
		value     bool
		errorCode codes.Code
	}{
		{
			desc:    "repository has branches",
			request: &gitalypb.HasLocalBranchesRequest{Repository: repo},
			value:   true,
		},
		{
			desc: "repository doesn't have branches",
			request: &gitalypb.HasLocalBranchesRequest{
				Repository: emptyRepo,
			},
			value: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			response, err := client.HasLocalBranches(ctx, tc.request)

			require.Equal(t, tc.errorCode, helper.GrpcCode(err))
			if err != nil {
				return
			}

			require.Equal(t, tc.value, response.Value)
		})
	}
}

func TestFailedHasLocalBranches(t *testing.T) {
	t.Parallel()
	_, client := setupRepositoryServiceWithoutRepo(t)

	testCases := []struct {
		desc       string
		repository *gitalypb.Repository
		expErr     error
	}{
		{
			desc:       "repository nil",
			repository: nil,
			expErr: status.Error(codes.InvalidArgument, testhelper.GitalyOrPraefect(
				"empty Repository",
				"repo scoped: empty Repository",
			)),
		},
		{
			desc:       "repository doesn't exist",
			repository: &gitalypb.Repository{StorageName: "fake", RelativePath: "path"},
			expErr: status.Error(codes.InvalidArgument, testhelper.GitalyOrPraefect(
				`GetStorageByName: no such storage: "fake"`,
				"repo scoped: invalid Repository",
			)),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)
			request := &gitalypb.HasLocalBranchesRequest{Repository: tc.repository}
			_, err := client.HasLocalBranches(ctx, request)
			testhelper.RequireGrpcError(t, tc.expErr, err)
		})
	}
}
