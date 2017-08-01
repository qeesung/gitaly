package repository

import (
	"testing"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

func TestRepositoryExists(t *testing.T) {
	server := runRepoServer(t)
	defer server.Stop()

	client := newRepositoryClient(t)

	// Setup storage paths
	testStorages := []config.Storage{
		{Name: "default", Path: testhelper.GitlabTestStoragePath()},
		{Name: "other", Path: "/home/git/repositories2"},
		{Name: "broken", Path: "/does/not/exist"},
	}

	defer func(oldStorages []config.Storage) {
		config.Config.Storages = oldStorages
	}(config.Config.Storages)
	config.Config.Storages = testStorages

	queries := []struct {
		desc      string
		request   *pb.RepositoryExistsRequest
		errorCode codes.Code
		exists    bool
	}{
		{
			desc: "repository nil",
			request: &pb.RepositoryExistsRequest{
				Repository: nil,
			},
			errorCode: codes.InvalidArgument,
		},
		{
			desc: "storage name empty",
			request: &pb.RepositoryExistsRequest{
				Repository: &pb.Repository{
					StorageName:  "",
					RelativePath: testhelper.TestRelativePath,
				},
			},
			errorCode: codes.InvalidArgument,
		},
		{
			desc: "relative path empty",
			request: &pb.RepositoryExistsRequest{
				Repository: &pb.Repository{
					StorageName:  "default",
					RelativePath: "",
				},
			},
			errorCode: codes.InvalidArgument,
		},
		{
			desc: "exists true",
			request: &pb.RepositoryExistsRequest{
				Repository: &pb.Repository{
					StorageName:  "default",
					RelativePath: testhelper.TestRelativePath,
				},
			},
			exists: true,
		},
		{
			desc: "exists false, wrong storage",
			request: &pb.RepositoryExistsRequest{
				Repository: &pb.Repository{
					StorageName:  "other",
					RelativePath: testhelper.TestRelativePath,
				},
			},
			exists: false,
		},
		{
			desc: "storage directory does not exist",
			request: &pb.RepositoryExistsRequest{
				Repository: &pb.Repository{
					StorageName:  "broken",
					RelativePath: "foobar.git",
				},
			},
			errorCode: codes.NotFound,
		},
	}

	for _, tc := range queries {
		response, err := client.Exists(context.Background(), tc.request)
		if tc.errorCode != codes.OK {
			require.Equal(t, tc.errorCode, grpc.Code(err), tc.desc)
			continue
		}

		require.NoError(t, err, tc.desc)
		require.Equal(t, tc.exists, response.Exists, tc.desc)
	}
}
