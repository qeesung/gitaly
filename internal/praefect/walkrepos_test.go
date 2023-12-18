package praefect

import (
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestWalkReposHandler(t *testing.T) {
	t.Parallel()

	db := testdb.New(t)
	for _, tc := range []struct {
		desc        string
		request     *gitalypb.WalkReposRequest
		responses   []*gitalypb.WalkReposResponse
		expectedErr error
	}{
		{
			desc:        "missing storage name",
			request:     &gitalypb.WalkReposRequest{},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrStorageNotSet),
		},
		{
			desc:    "repositories found",
			request: &gitalypb.WalkReposRequest{StorageName: "virtual-storage"},
			responses: []*gitalypb.WalkReposResponse{
				{RelativePath: "relative-path"},
				{RelativePath: "relative-path-2"},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db.TruncateAll(t)
			rs := datastore.NewPostgresRepositoryStore(db, map[string][]string{"virtual-storage": {"storage"}})
			ctx := testhelper.Context(t)

			require.NoError(t, rs.CreateRepository(ctx, 0, "virtual-storage", "relative-path", "relative-path", "storage", nil, nil, false, false))
			require.NoError(t, rs.CreateRepository(ctx, 1, "virtual-storage", "relative-path-2", "relative-path-2", "storage", nil, nil, false, false))

			tmp := testhelper.TempDir(t)

			ln, err := net.Listen("unix", filepath.Join(tmp, "praefect"))
			require.NoError(t, err)

			srv := NewGRPCServer(&Dependencies{
				Config:          config.Config{Failover: config.Failover{ElectionStrategy: config.ElectionStrategyPerRepository}},
				Logger:          testhelper.SharedLogger(t),
				RepositoryStore: rs,
				Registry:        protoregistry.GitalyProtoPreregistered,
			}, nil)
			defer srv.Stop()

			go testhelper.MustServe(t, srv, ln)

			clientConn, err := grpc.DialContext(ctx, "unix://"+ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			defer testhelper.MustClose(t, clientConn)

			client := gitalypb.NewInternalGitalyClient(clientConn)

			stream, err := client.WalkRepos(ctx, tc.request)
			if tc.expectedErr != nil {
				// Consume the first message and test for errors only if we're expecting an error.
				_, err = stream.Recv()
				testhelper.RequireGrpcError(t, tc.expectedErr, err)
				return
			}
			require.NoError(t, err)

			actualRepos, err := testhelper.Receive(stream.Recv)
			require.NoError(t, err)
			testhelper.ProtoEqual(t, tc.responses, actualRepos)
		})
	}
}
