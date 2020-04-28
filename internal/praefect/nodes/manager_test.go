package nodes

import (
	"context"
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

func TestNodeStatus(t *testing.T) {
	socket := testhelper.GetTemporaryGitalySocketFileName()
	svr, healthSvr := testhelper.NewServerWithHealth(t, socket)
	defer svr.Stop()

	cc, err := grpc.Dial(
		"unix://"+socket,
		grpc.WithInsecure(),
	)

	require.NoError(t, err)

	mockHistogramVec := promtest.NewMockHistogramVec()

	storageName := "default"
	cs := newConnectionStatus(models.Node{Storage: storageName}, cc, testhelper.DiscardTestEntry(t), mockHistogramVec)

	var expectedLabels [][]string
	for i := 0; i < healthcheckThreshold; i++ {
		ctx := context.Background()
		status, err := cs.check(ctx)

		require.NoError(t, err)
		require.True(t, status)
		expectedLabels = append(expectedLabels, []string{storageName})
	}

	require.Equal(t, expectedLabels, mockHistogramVec.LabelsCalled())
	require.Len(t, mockHistogramVec.Observer().Observed(), healthcheckThreshold)

	healthSvr.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	ctx := context.Background()
	status, err := cs.check(ctx)
	require.NoError(t, err)
	require.False(t, status)
}

func TestManagerFailoverDisabledElectionStrategySQL(t *testing.T) {
	const virtualStorageName = "virtual-storage-0"
	const primaryStorage = "praefect-internal-0"
	socket0, socket1 := testhelper.GetTemporaryGitalySocketFileName(), testhelper.GetTemporaryGitalySocketFileName()
	virtualStorage := &config.VirtualStorage{
		Name: virtualStorageName,
		Nodes: []*models.Node{
			{
				Storage:        primaryStorage,
				Address:        "unix://" + socket0,
				DefaultPrimary: true,
			},
			{
				Storage: "praefect-internal-1",
				Address: "unix://" + socket1,
			},
		},
	}

	srv0, healthSrv := testhelper.NewServerWithHealth(t, socket0)
	defer srv0.Stop()

	srv1, _ := testhelper.NewServerWithHealth(t, socket1)
	defer srv1.Stop()

	conf := config.Config{
		Failover:        config.Failover{Enabled: false, ElectionStrategy: "sql"},
		VirtualStorages: []*config.VirtualStorage{virtualStorage},
	}
	nm, err := NewManager(testhelper.DiscardTestEntry(t), conf, nil, promtest.NewMockHistogramVec())
	require.NoError(t, err)

	nm.Start(time.Millisecond, time.Millisecond)

	shard, err := nm.GetShard(virtualStorageName)
	require.NoError(t, err)
	require.Equal(t, primaryStorage, shard.Primary.GetStorage())

	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_UNKNOWN)
	nm.checkShards()

	shard, err = nm.GetShard(virtualStorageName)
	require.NoError(t, err)
	require.Equal(t, primaryStorage, shard.Primary.GetStorage())
}

func TestPrimaryIsSecond(t *testing.T) {
	virtualStorages := []*config.VirtualStorage{
		{
			Name: "virtual-storage-0",
			Nodes: []*models.Node{
				{
					Storage:        "praefect-internal-0",
					Address:        "unix://socket0",
					DefaultPrimary: false,
				},
				{
					Storage:        "praefect-internal-1",
					Address:        "unix://socket1",
					DefaultPrimary: true,
				},
			},
		},
	}

	conf := config.Config{
		VirtualStorages: virtualStorages,
		Failover:        config.Failover{Enabled: false},
	}

	mockHistogram := promtest.NewMockHistogramVec()
	nm, err := NewManager(testhelper.DiscardTestEntry(t), conf, nil, mockHistogram)
	require.NoError(t, err)

	shard, err := nm.GetShard("virtual-storage-0")
	require.NoError(t, err)

	require.Equal(t, virtualStorages[0].Nodes[1].Storage, shard.Primary.GetStorage())
	require.Equal(t, virtualStorages[0].Nodes[1].Address, shard.Primary.GetAddress())

	require.Len(t, shard.Secondaries, 1)
	require.Equal(t, virtualStorages[0].Nodes[0].Storage, shard.Secondaries[0].GetStorage())
	require.Equal(t, virtualStorages[0].Nodes[0].Address, shard.Secondaries[0].GetAddress())
}

func TestNodeManager(t *testing.T) {
	internalSocket0, internalSocket1 := testhelper.GetTemporaryGitalySocketFileName(), testhelper.GetTemporaryGitalySocketFileName()
	srv0, healthSrv0 := testhelper.NewServerWithHealth(t, internalSocket0)
	defer srv0.Stop()

	srv1, healthSrv1 := testhelper.NewServerWithHealth(t, internalSocket1)
	defer srv1.Stop()

	virtualStorages := []*config.VirtualStorage{
		{
			Name: "virtual-storage-0",
			Nodes: []*models.Node{
				{
					Storage:        "praefect-internal-0",
					Address:        "unix://" + internalSocket0,
					DefaultPrimary: true,
				},
				{
					Storage: "praefect-internal-1",
					Address: "unix://" + internalSocket1,
				},
			},
		},
	}

	confWithFailover := config.Config{
		VirtualStorages: virtualStorages,
		Failover:        config.Failover{Enabled: true},
	}
	confWithoutFailover := config.Config{
		VirtualStorages: virtualStorages,
		Failover:        config.Failover{Enabled: false},
	}

	mockHistogram := promtest.NewMockHistogramVec()
	nm, err := NewManager(testhelper.DiscardTestEntry(t), confWithFailover, nil, mockHistogram)
	require.NoError(t, err)

	nmWithoutFailover, err := NewManager(testhelper.DiscardTestEntry(t), confWithoutFailover, nil, mockHistogram)
	require.NoError(t, err)

	nm.Start(1*time.Millisecond, 5*time.Second)
	nmWithoutFailover.Start(1*time.Millisecond, 5*time.Second)

	_, err = nm.GetShard("virtual-storage-0")
	require.NoError(t, err)

	shardWithoutFailover, err := nmWithoutFailover.GetShard("virtual-storage-0")
	require.NoError(t, err)

	shard, err := nm.GetShard("virtual-storage-0")
	require.NoError(t, err)

	// shard without failover and shard with failover should be the same
	require.Equal(t, shardWithoutFailover.Primary.GetStorage(), shard.Primary.GetStorage())
	require.Equal(t, shardWithoutFailover.Primary.GetAddress(), shard.Primary.GetAddress())
	require.Len(t, shard.Secondaries, 1)
	require.Equal(t, shardWithoutFailover.Secondaries[0].GetStorage(), shard.Secondaries[0].GetStorage())
	require.Equal(t, shardWithoutFailover.Secondaries[0].GetAddress(), shard.Secondaries[0].GetAddress())

	require.Equal(t, virtualStorages[0].Nodes[0].Storage, shard.Primary.GetStorage())
	require.Equal(t, virtualStorages[0].Nodes[0].Address, shard.Primary.GetAddress())
	require.Len(t, shard.Secondaries, 1)
	require.Equal(t, virtualStorages[0].Nodes[1].Storage, shard.Secondaries[0].GetStorage())
	require.Equal(t, virtualStorages[0].Nodes[1].Address, shard.Secondaries[0].GetAddress())

	healthSrv0.SetServingStatus("", grpc_health_v1.HealthCheckResponse_UNKNOWN)
	nm.checkShards()

	labelsCalled := mockHistogram.LabelsCalled()
	for _, node := range virtualStorages[0].Nodes {
		require.Contains(t, labelsCalled, []string{node.Storage})
	}

	// since the primary is unhealthy, we expect checkShards to demote primary to secondary, and promote the healthy
	// secondary to primary

	shardWithoutFailover, err = nmWithoutFailover.GetShard("virtual-storage-0")
	require.NoError(t, err)

	shard, err = nm.GetShard("virtual-storage-0")
	require.NoError(t, err)

	// shard without failover and shard with failover should not be the same
	require.NotEqual(t, shardWithoutFailover.Primary.GetStorage(), shard.Primary.GetStorage())
	require.NotEqual(t, shardWithoutFailover.Primary.GetAddress(), shard.Primary.GetAddress())
	require.NotEqual(t, shardWithoutFailover.Secondaries[0].GetStorage(), shard.Secondaries[0].GetStorage())
	require.NotEqual(t, shardWithoutFailover.Secondaries[0].GetAddress(), shard.Secondaries[0].GetAddress())

	// shard without failover should still match the config
	require.Equal(t, virtualStorages[0].Nodes[0].Storage, shardWithoutFailover.Primary.GetStorage())
	require.Equal(t, virtualStorages[0].Nodes[0].Address, shardWithoutFailover.Primary.GetAddress())
	require.Len(t, shard.Secondaries, 1)
	require.Equal(t, virtualStorages[0].Nodes[1].Storage, shardWithoutFailover.Secondaries[0].GetStorage())
	require.Equal(t, virtualStorages[0].Nodes[1].Address, shardWithoutFailover.Secondaries[0].GetAddress())

	// shard with failover should have promoted a secondary to primary and demoted the primary to a secondary
	require.Equal(t, virtualStorages[0].Nodes[1].Storage, shard.Primary.GetStorage())
	require.Equal(t, virtualStorages[0].Nodes[1].Address, shard.Primary.GetAddress())
	require.Len(t, shard.Secondaries, 1)
	require.Equal(t, virtualStorages[0].Nodes[0].Storage, shard.Secondaries[0].GetStorage())
	require.Equal(t, virtualStorages[0].Nodes[0].Address, shard.Secondaries[0].GetAddress())

	healthSrv1.SetServingStatus("", grpc_health_v1.HealthCheckResponse_UNKNOWN)
	nm.checkShards()

	_, err = nm.GetShard("virtual-storage-0")
	require.Error(t, err, "should return error since no nodes are healthy")
}
