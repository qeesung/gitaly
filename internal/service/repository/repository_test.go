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
		{Name: "broken: directory does not exist", Path: "/does/not/exist"},
	}

	defer func(oldStorages []config.Storage) {
		config.Config.Storages = oldStorages
	}(config.Config.Storages)
	config.Config.Storages = testStorages

	queries := []struct {
		Request   *pb.RepositoryExistsRequest
		ErrorCode codes.Code
		Exists    bool
	}{
		{
			Request: &pb.RepositoryExistsRequest{
				Repository: nil,
			},
			ErrorCode: codes.InvalidArgument,
		},
		{
			Request: &pb.RepositoryExistsRequest{
				Repository: &pb.Repository{
					StorageName:  "",
					RelativePath: testhelper.TestRelativePath,
				},
			},
			ErrorCode: codes.InvalidArgument,
		},
		{
			Request: &pb.RepositoryExistsRequest{
				Repository: &pb.Repository{
					StorageName:  "default",
					RelativePath: "",
				},
			},
			ErrorCode: codes.InvalidArgument,
		},
		{
			Request: &pb.RepositoryExistsRequest{
				Repository: &pb.Repository{
					StorageName:  "default",
					RelativePath: testhelper.TestRelativePath,
				},
			},
			Exists: true,
		},
		{
			Request: &pb.RepositoryExistsRequest{
				Repository: &pb.Repository{
					StorageName:  "other",
					RelativePath: testhelper.TestRelativePath,
				},
			},
			Exists: false,
		},
		{
			Request: &pb.RepositoryExistsRequest{
				Repository: &pb.Repository{
					StorageName:  "broken: directory does not exist",
					RelativePath: "foobar.git",
				},
			},
			ErrorCode: codes.NotFound,
		},
	}

	for _, tc := range queries {
		response, err := client.Exists(context.Background(), tc.Request)
		if err != nil {
			require.Equal(t, tc.ErrorCode, grpc.Code(err))
			continue
		}

		require.Equal(t, tc.Exists, response.Exists)
	}
}
