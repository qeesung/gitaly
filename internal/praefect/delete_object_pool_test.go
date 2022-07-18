//go:build !gitaly_test_sha256

package praefect

import (
	"context"
	"testing"

	grpcmwlogrus "github.com/grpc-ecosystem/go-grpc-middleware/logging/logrus"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/log"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type mockObjectPoolService struct {
	gitalypb.UnimplementedObjectPoolServiceServer
	deleteObjectPoolFunc func(context.Context, *gitalypb.DeleteObjectPoolRequest) (*gitalypb.DeleteObjectPoolResponse, error)
}

func (m mockObjectPoolService) DeleteObjectPool(ctx context.Context, req *gitalypb.DeleteObjectPoolRequest) (*gitalypb.DeleteObjectPoolResponse, error) {
	return m.deleteObjectPoolFunc(ctx, req)
}

func TestDeleteObjectPoolHandler(t *testing.T) {
	// the primary returns a successful response
	primarySrv := grpc.NewServer()
	gitalypb.RegisterObjectPoolServiceServer(primarySrv, mockObjectPoolService{
		deleteObjectPoolFunc: func(context.Context, *gitalypb.DeleteObjectPoolRequest) (*gitalypb.DeleteObjectPoolResponse, error) {
			return &gitalypb.DeleteObjectPoolResponse{}, nil
		},
	})

	// the secondary fails as it doesn't have the service registered
	secondarySrv := grpc.NewServer()

	primaryLn, primaryAddr := testhelper.GetLocalhostListener(t)
	secondaryLn, secondaryAddr := testhelper.GetLocalhostListener(t)

	defer primarySrv.Stop()
	go primarySrv.Serve(primaryLn)

	defer secondarySrv.Stop()
	go secondarySrv.Serve(secondaryLn)

	db := testdb.New(t)
	rs := datastore.NewPostgresRepositoryStore(db, nil)

	ctx := testhelper.Context(t)

	repo := &gitalypb.Repository{
		StorageName:  "virtual-storage",
		RelativePath: gittest.NewObjectPoolName(t),
	}

	require.NoError(t,
		rs.CreateRepository(ctx, 1, repo.StorageName, repo.RelativePath, "replica-path", "primary", []string{"secondary", "unconfigured_storage"}, nil, true, true),
	)

	primaryConn, err := grpc.Dial(primaryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer primaryConn.Close()

	secondaryConn, err := grpc.Dial(secondaryAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer secondaryConn.Close()

	praefectLn, praefectAddr := testhelper.GetLocalhostListener(t)
	logger, hook := test.NewNullLogger()
	praefectSrv := grpc.NewServer(grpc.ChainStreamInterceptor(
		grpcmwlogrus.StreamServerInterceptor(logrus.NewEntry(logger), grpcmwlogrus.WithTimestampFormat(log.LogTimestampFormat)),
	))
	praefectSrv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "gitaly.ObjectPoolService",
		HandlerType: (*interface{})(nil),
		Streams: []grpc.StreamDesc{
			{
				StreamName: "DeleteObjectPool",
				Handler: DeleteObjectPoolHandler(rs, Connections{
					"virtual-storage": {
						"primary":   primaryConn,
						"secondary": secondaryConn,
					},
				}),
				ServerStreams: true,
				ClientStreams: true,
			},
		},
	}, struct{}{})

	defer praefectSrv.Stop()
	go praefectSrv.Serve(praefectLn)

	praefectConn, err := grpc.Dial(praefectAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer praefectConn.Close()

	_, err = gitalypb.NewObjectPoolServiceClient(
		praefectConn,
	).DeleteObjectPool(ctx, &gitalypb.DeleteObjectPoolRequest{
		ObjectPool: &gitalypb.ObjectPool{Repository: repo},
	})
	require.NoError(t, err)

	require.Equal(t, 2, len(hook.Entries), "expected a log entry for failed deletion")
	require.Equal(t, "failed deleting repository", hook.Entries[0].Message)
	require.Equal(t, repo.StorageName, hook.Entries[0].Data["virtual_storage"])
	require.Equal(t, repo.RelativePath, hook.Entries[0].Data["relative_path"])
	require.Equal(t, "secondary", hook.Entries[0].Data["storage"])
}
