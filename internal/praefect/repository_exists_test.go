//go:build !gitaly_test_sha256

package praefect

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/grpc-proxy/proxy"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/protoregistry"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func TestRepositoryExistsHandler(t *testing.T) {
	t.Parallel()
	errServedByGitaly := status.Error(codes.Unknown, "request passed to Gitaly")

	db := testdb.New(t)
	for _, tc := range []struct {
		desc          string
		routeToGitaly bool
		repository    *gitalypb.Repository
		response      *gitalypb.RepositoryExistsResponse
		error         error
	}{
		{
			desc:  "missing repository",
			error: errMissingRepository,
		},
		{
			desc:       "missing storage name",
			repository: &gitalypb.Repository{RelativePath: "relative-path"},
			error:      errMissingStorageName,
		},
		{
			desc:       "missing relative path",
			repository: &gitalypb.Repository{StorageName: "virtual-storage"},
			error:      errMissingRelativePath,
		},
		{
			desc:       "invalid virtual storage",
			repository: &gitalypb.Repository{StorageName: "invalid virtual storage", RelativePath: "relative-path"},
			response:   &gitalypb.RepositoryExistsResponse{Exists: false},
		},
		{
			desc:       "invalid relative path",
			repository: &gitalypb.Repository{StorageName: "virtual-storage", RelativePath: "invalid relative path"},
			response:   &gitalypb.RepositoryExistsResponse{Exists: false},
		},
		{
			desc:       "repository found",
			repository: &gitalypb.Repository{StorageName: "virtual-storage", RelativePath: "relative-path"},
			response:   &gitalypb.RepositoryExistsResponse{Exists: true},
		},
		{
			desc:          "routed to gitaly",
			routeToGitaly: true,
			error:         errServedByGitaly,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			db.TruncateAll(t)
			rs := datastore.NewPostgresRepositoryStore(db, map[string][]string{"virtual-storage": {"storage"}})
			ctx := testhelper.Context(t)

			require.NoError(t, rs.CreateRepository(ctx, 0, "virtual-storage", "relative-path", "relative-path", "storage", nil, nil, false, false))

			tmp := testhelper.TempDir(t)

			ln, err := net.Listen("unix", filepath.Join(tmp, "praefect"))
			require.NoError(t, err)

			electionStrategy := config.ElectionStrategyPerRepository
			if tc.routeToGitaly {
				electionStrategy = config.ElectionStrategySQL
			}

			srv := NewGRPCServer(
				config.Config{Failover: config.Failover{ElectionStrategy: electionStrategy}},
				testhelper.NewDiscardingLogEntry(t),
				protoregistry.GitalyProtoPreregistered,
				func(ctx context.Context, fullMethodName string, peeker proxy.StreamPeeker) (*proxy.StreamParameters, error) {
					return nil, errServedByGitaly
				},
				nil,
				rs,
				nil,
				nil,
				nil,
				nil,
				nil,
			)
			defer srv.Stop()

			go testhelper.MustServe(t, srv, ln)

			clientConn, err := grpc.DialContext(ctx, "unix://"+ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			defer testhelper.MustClose(t, clientConn)

			client := gitalypb.NewRepositoryServiceClient(clientConn)
			_, err = client.RepositorySize(ctx, &gitalypb.RepositorySizeRequest{Repository: tc.repository})
			testhelper.RequireGrpcError(t, errServedByGitaly, err)

			resp, err := client.RepositoryExists(ctx, &gitalypb.RepositoryExistsRequest{Repository: tc.repository})
			testhelper.RequireGrpcError(t, tc.error, err)
			testhelper.ProtoEqual(t, tc.response, resp)
		})
	}
}
