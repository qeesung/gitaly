package repository

import (
	"os"
	"path"
	"testing"

	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
)

func TestSuccessfulRepositorySizeRequest(t *testing.T) {
	server := runRepoServer(t)
	defer server.Stop()

	client := newRepositoryClient(t)

	storageName := "default"
	storagePath, found := config.StoragePath(storageName)
	if !found {
		t.Fatalf("No %q storage was found", storageName)
	}

	repoCopyPath := path.Join(storagePath, "fixed-size-repo.git")
	testhelper.MustRunCommand(t, nil, "cp", "-R", "testdata/fixed-size-repo.git", repoCopyPath)
	defer os.RemoveAll(repoCopyPath)

	request := &pb.RepositorySizeRequest{
		Repository: &pb.Repository{
			StorageName:  storageName,
			RelativePath: "fixed-size-repo.git",
		},
	}

	response, err := client.RepositorySize(context.Background(), request)
	require.NoError(t, err)
	// We can't test for an exact size because it will be different for systems with different sector sizes,
	// so we settle for anything greater than zero.
	require.True(t, response.Size > 0, "size must be greater than zero")
}

func TestFailedRepositorySizeRequest(t *testing.T) {
	server := runRepoServer(t)
	defer server.Stop()

	client := newRepositoryClient(t)

	invalidRepo := &pb.Repository{StorageName: "fake", RelativePath: "path"}

	testCases := []struct {
		description string
		repo        *pb.Repository
	}{
		{repo: invalidRepo, description: "Invalid repo"},
	}

	for _, testCase := range testCases {
		t.Logf("test case: %q", testCase.description)

		request := &pb.RepositorySizeRequest{
			Repository: testCase.repo,
		}

		_, err := client.RepositorySize(context.Background(), request)
		testhelper.AssertGrpcError(t, err, codes.InvalidArgument, "")
	}
}
