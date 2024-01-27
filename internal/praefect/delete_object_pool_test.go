package praefect

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
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
	go testhelper.MustServe(t, primarySrv, primaryLn)

	defer secondarySrv.Stop()
	go testhelper.MustServe(t, secondarySrv, secondaryLn)

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
	logger := testhelper.NewLogger(t)
	hook := testhelper.AddLoggerHook(logger)
	praefectSrv := grpc.NewServer(grpc.ChainStreamInterceptor(
		logger.StreamServerInterceptor(log.DefaultInterceptorLogger(logger),
			log.WithTimestampFormat(log.LogTimestampFormat)),
	))
	praefectSrv.RegisterService(&grpc.ServiceDesc{
		ServiceName: "gitaly.ObjectPoolService",
		HandlerType: (*interface{})(nil),
		Streams: []grpc.StreamDesc{
			{
				StreamName: "DeleteObjectPool",
				Handler: DeleteObjectPoolHandler(rs, logger, Connections{
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
	go testhelper.MustServe(t, praefectSrv, praefectLn)

	praefectConn, err := grpc.Dial(praefectAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer praefectConn.Close()

	_, err = gitalypb.NewObjectPoolServiceClient(
		praefectConn,
	).DeleteObjectPool(ctx, &gitalypb.DeleteObjectPoolRequest{
		ObjectPool: &gitalypb.ObjectPool{Repository: repo},
	})
	require.NoError(t, err)

	require.Len(t, hook.AllEntries(), 3, "expected a log entry for failed deletion")
	entry := hook.AllEntries()[1]
	require.Equal(t, "failed deleting repository", entry.Message)
	require.Equal(t, repo.StorageName, entry.Data["virtual_storage"])
	require.Equal(t, repo.RelativePath, entry.Data["relative_path"])
	require.Equal(t, "secondary", entry.Data["storage"])
}
