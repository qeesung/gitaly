package ref

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"

	"golang.org/x/net/context"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
)

func TestFindRefNameSuccess(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	repo := &pb.Repository{Path: testRepoPath}
	rpcRequest := &pb.FindRefNameRequest{
		Repository: repo,
		CommitId:   "0b4bc9a49b562e85de7cc9e834518ea6828729b9",
		Prefix:     []byte(`refs/heads/`),
	}

	c, err := client.FindRefName(context.Background(), rpcRequest)
	assert.NoError(t, err)

	response := string(c.GetName())

	assert.Equal(t, `refs/heads/expand-collapse-diffs`, response)
}

func TestFindRefNameEmptyCommit(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	repo := &pb.Repository{Path: testRepoPath}
	rpcRequest := &pb.FindRefNameRequest{
		Repository: repo,
		CommitId:   "",
		Prefix:     []byte(`refs/heads/`),
	}

	c, err := client.FindRefName(context.Background(), rpcRequest)
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, grpc.Code(err))

	response := string(c.GetName())
	assert.Empty(t, response)
}

func TestFindRefNameInvalidRepo(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	repo := &pb.Repository{Path: ""}
	rpcRequest := &pb.FindRefNameRequest{
		Repository: repo,
		CommitId:   "0b4bc9a49b562e85de7cc9e834518ea6828729b9",
		Prefix:     []byte(`refs/heads/`),
	}

	c, err := client.FindRefName(context.Background(), rpcRequest)
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, grpc.Code(err))

	response := string(c.GetName())
	assert.Empty(t, response)
}

func TestFindRefNameInvalidPrefix(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	repo := &pb.Repository{Path: testRepoPath}
	rpcRequest := &pb.FindRefNameRequest{
		Repository: repo,
		CommitId:   "0b4bc9a49b562e85de7cc9e834518ea6828729b9",
		Prefix:     []byte(`refs/nonexistant/`),
	}

	c, err := client.FindRefName(context.Background(), rpcRequest)
	assert.NoError(t, err)
	assert.Empty(t, c.Name)
}

func TestFindRefNameInvalidObject(t *testing.T) {
	server := runRefServer(t)
	defer server.Stop()

	client := newRefClient(t)
	repo := &pb.Repository{Path: testRepoPath}
	rpcRequest := &pb.FindRefNameRequest{
		Repository: repo,
		CommitId:   "dead1234dead1234dead1234dead1234dead1234",
	}

	c, err := client.FindRefName(context.Background(), rpcRequest)
	assert.NoError(t, err)
	assert.NotEmpty(t, c.GetName())
}
