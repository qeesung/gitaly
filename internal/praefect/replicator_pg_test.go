package praefect

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/client"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/service/repository"
	"gitlab.com/gitlab-org/gitaly/v15/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testdb"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
)

type mockRepositoryService struct {
	gitalypb.UnimplementedRepositoryServiceServer
	ReplicateRepositoryFunc func(context.Context, *gitalypb.ReplicateRepositoryRequest) (*gitalypb.ReplicateRepositoryResponse, error)
}

func (m *mockRepositoryService) ReplicateRepository(ctx context.Context, r *gitalypb.ReplicateRepositoryRequest) (*gitalypb.ReplicateRepositoryResponse, error) {
	return m.ReplicateRepositoryFunc(ctx, r)
}

func TestReplicatorInvalidSourceRepository(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	tmp := testhelper.TempDir(t)

	socketPath := filepath.Join(tmp, "socket")
	ln, err := net.Listen("unix", socketPath)
	require.NoError(t, err)

	srv := grpc.NewServer()
	gitalypb.RegisterRepositoryServiceServer(srv, &mockRepositoryService{
		ReplicateRepositoryFunc: func(context.Context, *gitalypb.ReplicateRepositoryRequest) (*gitalypb.ReplicateRepositoryResponse, error) {
			return nil, repository.ErrInvalidSourceRepository
		},
	})
	defer srv.Stop()
	go srv.Serve(ln)

	targetCC, err := client.Dial(ln.Addr().Network()+":"+ln.Addr().String(), nil)
	require.NoError(t, err)
	defer testhelper.MustClose(t, targetCC)

	rs := datastore.NewPostgresRepositoryStore(testdb.New(t), nil)

	require.NoError(t, rs.CreateRepository(ctx, 1, "virtual-storage-1", "relative-path-1", "relative-path-1", "gitaly-1", nil, nil, true, false))

	exists, err := rs.RepositoryExists(ctx, "virtual-storage-1", "relative-path-1")
	require.NoError(t, err)
	require.True(t, exists)

	r := &defaultReplicator{rs: rs, log: testhelper.NewDiscardingLogger(t)}
	require.NoError(t, r.Replicate(ctx, datastore.ReplicationEvent{
		Job: datastore.ReplicationJob{
			RepositoryID:      1,
			ReplicaPath:       "relative-path-1",
			VirtualStorage:    "virtual-storage-1",
			RelativePath:      "relative-path-1",
			SourceNodeStorage: "gitaly-1",
			TargetNodeStorage: "gitaly-2",
		},
	}, nil, targetCC))

	exists, err = rs.RepositoryExists(ctx, "virtual-storage-1", "relative-path-1")
	require.NoError(t, err)
	require.False(t, exists)
}

func TestReplicatorDestroy(t *testing.T) {
	t.Parallel()
	db := testdb.New(t)
	for _, tc := range []struct {
		change datastore.ChangeType
		error  error
	}{
		{change: datastore.DeleteReplica},
		{change: datastore.DeleteRepo},
	} {
		t.Run(string(tc.change), func(t *testing.T) {
			db.TruncateAll(t)

			rs := datastore.NewPostgresRepositoryStore(db, nil)
			ctx := testhelper.Context(t)

			require.NoError(t, rs.CreateRepository(ctx, 1, "virtual-storage-1", "relative-path-1", "relative-path-1", "storage-1", []string{"storage-2"}, nil, false, false))

			ln, err := net.Listen("tcp", "localhost:0")
			require.NoError(t, err)

			srv := grpc.NewServer(grpc.UnknownServiceHandler(func(srv interface{}, stream grpc.ServerStream) error {
				return stream.SendMsg(&gitalypb.RemoveRepositoryResponse{})
			}))

			go srv.Serve(ln)
			defer srv.Stop()

			clientConn, err := grpc.Dial(ln.Addr().String(), grpc.WithInsecure())
			require.NoError(t, err)
			defer clientConn.Close()

			require.Equal(t, tc.error, defaultReplicator{
				rs:  rs,
				log: testhelper.NewDiscardingLogger(t),
			}.Destroy(
				ctx,
				datastore.ReplicationEvent{
					Job: datastore.ReplicationJob{
						ReplicaPath:       "relative-path-1",
						Change:            tc.change,
						VirtualStorage:    "virtual-storage-1",
						RelativePath:      "relative-path-1",
						TargetNodeStorage: "storage-1",
					},
				},
				clientConn,
			))

			exists, err := rs.RepositoryExists(ctx, "virtual-storage-1", "relative-path-1")
			require.NoError(t, err)
			require.True(t, exists)
		})
	}
}
