package rubyserver

import (
	"testing"

	"github.com/stretchr/testify/assert"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	"google.golang.org/grpc/codes"
)

func TestStopSafe(t *testing.T) {
	badServers := []*Server{
		nil,
		&Server{},
	}

	for _, bs := range badServers {
		bs.Stop()
	}
}

func TestSetHeaders(t *testing.T) {
	testRepo := testhelper.TestRepository()

	testCases := []struct {
		repo    *pb.Repository
		errType codes.Code
	}{
		{
			repo:    &pb.Repository{StorageName: "foo", RelativePath: "bar.git"},
			errType: codes.InvalidArgument,
		},
		{
			repo:    testRepo,
			errType: codes.OK,
		},
	}

	for _, tc := range testCases {
		ctx, cancel := testhelper.Context()
		defer cancel()

		clientCtx, err := SetHeaders(ctx, tc.repo)

		if tc.errType != codes.OK {
			testhelper.AssertGrpcError(t, err, tc.errType, "")
			assert.Nil(t, clientCtx)
		} else {
			assert.NoError(t, err)
			assert.NotNil(t, clientCtx)
		}
	}
}
