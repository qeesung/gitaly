// +build postgres

package nodes

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/promtest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
)

var shardName string = "test-shard-0"

func TestGetPrimaryAndSecondaries(t *testing.T) {
	db := getDB(t)

	logger := testhelper.NewTestLogger(t).WithField("test", t.Name())
	praefectSocket := testhelper.GetTemporaryGitalySocketFileName()
	socketName := "unix://" + praefectSocket

	conf := config.Config{
		SocketPath: socketName,
	}

	internalSocket0 := testhelper.GetTemporaryGitalySocketFileName()
	srv0, _ := testhelper.NewServerWithHealth(t, internalSocket0)
	defer srv0.Stop()

	cc0, err := grpc.Dial(
		"unix://"+internalSocket0,
		grpc.WithInsecure(),
	)
	require.NoError(t, err)

	storageName := "default"
	mockHistogramVec0 := promtest.NewMockHistogramVec()
	cs0 := newConnectionStatus(models.Node{Storage: storageName + "-0"}, cc0, testhelper.DiscardTestEntry(t), mockHistogramVec0)

	ns := []*nodeStatus{cs0}
	elector := newSQLElector(shardName, conf, 1, defaultActivePraefectSeconds, db.DB, logger, ns)
	require.Contains(t, elector.praefectName, ":"+socketName)
	require.Equal(t, elector.shardName, shardName)

	ctx, cancel := testhelper.Context()
	defer cancel()
	err = elector.checkNodes(ctx)
	db.RequireRowsInTable(t, "shard_primaries", 1)

	elector.demotePrimary()
	shard, err := elector.GetShard()
	db.RequireRowsInTable(t, "shard_primaries", 1)
	require.Equal(t, ErrPrimaryNotHealthy, err)
	require.Empty(t, shard)
}

func TestBasicFailover(t *testing.T) {
	db := getDB(t)

	logger := testhelper.NewTestLogger(t).WithField("test", t.Name())
	praefectSocket := testhelper.GetTemporaryGitalySocketFileName()
	socketName := "unix://" + praefectSocket

	conf := config.Config{
		SocketPath: socketName,
	}

	internalSocket0, internalSocket1 := testhelper.GetTemporaryGitalySocketFileName(), testhelper.GetTemporaryGitalySocketFileName()
	srv0, healthSrv0 := testhelper.NewServerWithHealth(t, internalSocket0)
	defer srv0.Stop()

	srv1, healthSrv1 := testhelper.NewServerWithHealth(t, internalSocket1)
	defer srv1.Stop()

	cc0, err := grpc.Dial(
		"unix://"+internalSocket0,
		grpc.WithInsecure(),
	)
	require.NoError(t, err)

	cc1, err := grpc.Dial(
		"unix://"+internalSocket1,
		grpc.WithInsecure(),
	)

	require.NoError(t, err)

	storageName := "default"
	mockHistogramVec0, mockHistogramVec1 := promtest.NewMockHistogramVec(), promtest.NewMockHistogramVec()
	cs0 := newConnectionStatus(models.Node{Storage: storageName + "-0"}, cc0, testhelper.DiscardTestEntry(t), mockHistogramVec0)
	cs1 := newConnectionStatus(models.Node{Storage: storageName + "-1"}, cc1, testhelper.DiscardTestEntry(t), mockHistogramVec1)

	ns := []*nodeStatus{cs0, cs1}
	elector := newSQLElector(shardName, conf, 1, defaultActivePraefectSeconds, db.DB, logger, ns)

	ctx, cancel := testhelper.Context()
	defer cancel()
	err = elector.checkNodes(ctx)

	require.NoError(t, err)
	db.RequireRowsInTable(t, "node_status", 2)
	db.RequireRowsInTable(t, "shard_primaries", 1)

	require.Equal(t, cs0, elector.primaryNode.Node)
	shard, err := elector.GetShard()
	require.NoError(t, err)
	require.Equal(t, cs0.GetStorage(), shard.Primary.GetStorage())
	require.Equal(t, 1, len(shard.Secondaries))
	require.Equal(t, cs1.GetStorage(), shard.Secondaries[0].GetStorage())
	require.False(t, shard.IsReadOnly, "new shard should not be read-only")

	// Bring first node down
	healthSrv0.SetServingStatus("", grpc_health_v1.HealthCheckResponse_UNKNOWN)

	// Primary should remain even after the first check
	err = elector.checkNodes(ctx)
	require.NoError(t, err)
	shard, err = elector.GetShard()
	require.NoError(t, err)
	require.False(t, shard.IsReadOnly)

	// Wait for stale timeout to expire
	time.Sleep(1 * time.Second)

	// Expect that the other node is promoted
	err = elector.checkNodes(ctx)
	require.NoError(t, err)

	db.RequireRowsInTable(t, "node_status", 2)
	db.RequireRowsInTable(t, "shard_primaries", 1)
	shard, err = elector.GetShard()
	require.NoError(t, err)
	require.Equal(t, cs1.GetStorage(), shard.Primary.GetStorage())
	require.True(t, shard.IsReadOnly, "shard should be read-only after a failover")

	// We should be able to enable writes on the new primary
	require.NoError(t, elector.enableWrites(ctx))
	shard, err = elector.GetShard()
	require.NoError(t, err)
	require.False(t, shard.IsReadOnly, "")

	// Bring second node down
	healthSrv1.SetServingStatus("", grpc_health_v1.HealthCheckResponse_UNKNOWN)

	// Wait for stale timeout to expire
	time.Sleep(1 * time.Second)
	err = elector.checkNodes(ctx)
	require.NoError(t, err)

	db.RequireRowsInTable(t, "node_status", 2)
	// No new candidates
	_, err = elector.GetShard()
	require.Error(t, ErrPrimaryNotHealthy, err)
	require.Error(t, ErrPrimaryNotHealthy, elector.enableWrites(ctx),
		"shouldn't be able to enable writes with unhealthy master")
}
