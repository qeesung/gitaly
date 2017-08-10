package commit

import (
	"context"
	"testing"

	"google.golang.org/grpc/codes"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestCommitStatsUnimplemented(t *testing.T) {
	service, ruby, serverSocketPath := startTestServices(t)
	defer stopTestServices(service, ruby)

	client := newCommitServiceClient(t, serverSocketPath)

	_, err := client.CommitStats(context.Background(), &pb.CommitStatsRequest{Repository: testRepo, Revision: []byte("master")})
	testhelper.AssertGrpcError(t, err, codes.Unimplemented, "not implemented")
}
