package raft

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/keyvalue"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func TestMetadataGroup_BootstrapIfNeeded(t *testing.T) {
	t.Parallel()

	t.Run("bootstrap a singular cluster", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)
		cfg := testcfg.Build(t)
		logger := testhelper.NewLogger(t)

		cluster := newTestRaftCluster(t, 1)
		defer cluster.closeAll()

		db, closeDB := setupStorageDB(t, cfg)
		defer closeDB()

		metadataGroup, err := newMetadataRaftGroup(
			ctx, cluster.nodes[1].nodeHost, dbForGroup(db, MetadataGroupID, 1), cluster.createRaftConfig(1), logger,
		)
		require.NoError(t, err)

		clusterInfo, err := metadataGroup.BootstrapIfNeeded()
		require.NoError(t, err)

		testhelper.ProtoEqual(t, &gitalypb.Cluster{
			ClusterId:     cluster.clusterID,
			NextStorageId: 1,
		}, clusterInfo)

		clusterInfo, err = metadataGroup.ClusterInfo()
		require.NoError(t, err)
		testhelper.ProtoEqual(t, &gitalypb.Cluster{
			ClusterId:     cluster.clusterID,
			NextStorageId: 1,
		}, clusterInfo)
	})

	t.Run("bootstrap a 3-node cluster", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)
		cfg := testcfg.Build(t)
		logger := testhelper.NewLogger(t)

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()

		db, closeDB := setupStorageDB(t, cfg)
		defer closeDB()

		var wg sync.WaitGroup
		for i := raftID(1); i <= 3; i++ {
			wg.Add(1)
			go func(i raftID) {
				defer wg.Done()

				metadataGroup, err := newMetadataRaftGroup(
					ctx, cluster.nodes[i].nodeHost, dbForGroup(db, MetadataGroupID, i), cluster.createRaftConfig(i), logger,
				)
				require.NoError(t, err)

				clusterInfo, err := metadataGroup.BootstrapIfNeeded()
				require.NoError(t, err)

				testhelper.ProtoEqual(t, &gitalypb.Cluster{
					ClusterId:     cluster.clusterID,
					NextStorageId: 1,
				}, clusterInfo)

				clusterInfo, err = metadataGroup.ClusterInfo()
				require.NoError(t, err)
				testhelper.ProtoEqual(t, &gitalypb.Cluster{
					ClusterId:     cluster.clusterID,
					NextStorageId: 1,
				}, clusterInfo)
			}(i)
		}

		wg.Wait()
	})

	t.Run("bootstrap a 3-node cluster with 2 available nodes", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)
		cfg := testcfg.Build(t)
		logger := testhelper.NewLogger(t)

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()

		db, closeDB := setupStorageDB(t, cfg)
		defer closeDB()

		var wg sync.WaitGroup
		// Bootstrap using two nodes.
		for i := raftID(1); i <= 2; i++ {
			wg.Add(1)
			go func(i raftID) {
				defer wg.Done()

				metadataGroup, err := newMetadataRaftGroup(
					ctx, cluster.nodes[i].nodeHost, dbForGroup(db, MetadataGroupID, i), cluster.createRaftConfig(i), logger,
				)
				require.NoError(t, err)

				clusterInfo, err := metadataGroup.BootstrapIfNeeded()
				require.NoError(t, err)

				testhelper.ProtoEqual(t, &gitalypb.Cluster{
					ClusterId:     cluster.clusterID,
					NextStorageId: 1,
				}, clusterInfo)
			}(i)
		}
		wg.Wait()

		// Now node 3 joins.
		metadataGroup, err := newMetadataRaftGroup(
			ctx, cluster.nodes[3].nodeHost, dbForGroup(db, MetadataGroupID, 3), cluster.createRaftConfig(3), logger,
		)
		require.NoError(t, err)

		// It is able to access cluster info.
		require.NoError(t, metadataGroup.WaitReady())

		clusterInfo, err := metadataGroup.ClusterInfo()
		require.NoError(t, err)
		testhelper.ProtoEqual(t, &gitalypb.Cluster{
			ClusterId:     cluster.clusterID,
			NextStorageId: 1,
		}, clusterInfo)
	})

	t.Run("bootstrap a bootstrapped cluster", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)
		cfg := testcfg.Build(t)
		logger := testhelper.NewLogger(t)

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()

		db, closeDB := setupStorageDB(t, cfg)
		defer closeDB()

		var wg sync.WaitGroup
		for i := raftID(1); i <= 3; i++ {
			wg.Add(1)
			go func(i raftID) {
				defer wg.Done()

				metadataGroup, err := newMetadataRaftGroup(
					ctx, cluster.nodes[i].nodeHost, dbForGroup(db, MetadataGroupID, i), cluster.createRaftConfig(i), logger,
				)
				require.NoError(t, err)

				_, err = metadataGroup.BootstrapIfNeeded()
				require.NoError(t, err)

				clusterInfo, err := metadataGroup.BootstrapIfNeeded()
				require.NoError(t, err)
				testhelper.ProtoEqual(t, &gitalypb.Cluster{
					ClusterId:     cluster.clusterID,
					NextStorageId: 1,
				}, clusterInfo)
			}(i)
		}
		wg.Wait()
	})

	t.Run("context cancellation while bootstrapping cluster", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)
		cfg := testcfg.Build(t)
		logger := testhelper.NewLogger(t)

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()

		db, closeDB := setupStorageDB(t, cfg)
		defer closeDB()

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		metadataGroup, err := newMetadataRaftGroup(
			ctx, cluster.nodes[1].nodeHost, dbForGroup(db, MetadataGroupID, 1), cluster.createRaftConfig(1), logger,
		)

		require.NoError(t, err)
		go func() {
			select {
			case <-ctx.Done():
				t.Error("test exits prematurely")
			case <-time.After(200 * time.Millisecond):
				cancel()
			}
		}()

		_, err = metadataGroup.BootstrapIfNeeded()
		require.EqualError(t, err, "waiting to bootstrap cluster: context canceled")
	})
}

func TestMetadataGroup_RegisterStorage(t *testing.T) {
	t.Parallel()

	bootstrapCluster := func(t *testing.T, cluster *testRaftCluster, db keyvalue.Transactioner) map[raftID]*metadataRaftGroup {
		ctx := testhelper.Context(t)
		logger := testhelper.NewLogger(t)

		var mu sync.Mutex
		groups := map[raftID]*metadataRaftGroup{}

		var wg sync.WaitGroup
		for i := raftID(1); i <= 3; i++ {
			wg.Add(1)
			go func(i raftID) {
				defer wg.Done()

				metadataGroup, err := newMetadataRaftGroup(
					ctx, cluster.nodes[i].nodeHost, dbForGroup(db, MetadataGroupID, i), cluster.createRaftConfig(i), logger,
				)
				require.NoError(t, err)

				clusterInfo, err := metadataGroup.BootstrapIfNeeded()
				require.NoError(t, err)
				testhelper.ProtoEqual(t, &gitalypb.Cluster{
					ClusterId:     cluster.clusterID,
					NextStorageId: 1,
				}, clusterInfo)

				mu.Lock()
				groups[i] = metadataGroup
				mu.Unlock()
			}(i)
		}
		wg.Wait()

		return groups
	}

	t.Run("register storages with a non-bootstrapped cluster", func(t *testing.T) {
		t.Parallel()
		cfg := testcfg.Build(t)

		cluster := newTestRaftCluster(t, 1)
		defer cluster.closeAll()

		db, closeDB := setupStorageDB(t, cfg)
		defer closeDB()

		metadataGroup, err := newMetadataRaftGroup(
			testhelper.Context(t), cluster.nodes[1].nodeHost, dbForGroup(db, MetadataGroupID, 1), cluster.createRaftConfig(1), testhelper.NewLogger(t),
		)
		require.NoError(t, err)

		require.NoError(t, metadataGroup.WaitReady())

		_, err = metadataGroup.RegisterStorage("storage-1")
		require.EqualError(t, err, "cluster has not been bootstrapped")
	})

	t.Run("register storages with a bootstrapped cluster", func(t *testing.T) {
		t.Parallel()
		cfg := testcfg.Build(t)

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()

		db, closeDB := setupStorageDB(t, cfg)
		defer closeDB()

		groups := bootstrapCluster(t, cluster, db)

		for i := raftID(1); i <= 3; i++ {
			id, err := groups[i].RegisterStorage(fmt.Sprintf("storage-%d", 2*i))
			require.NoError(t, err)
			require.Equal(t, i, id)
		}

		for i := raftID(1); i <= 3; i++ {
			clusterInfo, err := groups[i].ClusterInfo()
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.Cluster{
				ClusterId:     cluster.clusterID,
				NextStorageId: 4,
				Storages: map[uint64]*gitalypb.Storage{
					1: {StorageId: 1, Name: "storage-2"},
					2: {StorageId: 2, Name: "storage-4"},
					3: {StorageId: 3, Name: "storage-6"},
				},
			}, clusterInfo)
		}
	})

	t.Run("register a duplicated storage name", func(t *testing.T) {
		t.Parallel()
		cfg := testcfg.Build(t)

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()

		db, closeDB := setupStorageDB(t, cfg)
		defer closeDB()

		groups := bootstrapCluster(t, cluster, db)

		id, err := groups[1].RegisterStorage("storage-1")
		require.NoError(t, err)
		require.Equal(t, raftID(1), id)

		_, err = groups[2].RegisterStorage("storage-1")
		require.EqualError(t, err, "storage \"storage-1\" already registered")

		_, err = groups[3].RegisterStorage("storage-1")
		require.EqualError(t, err, "storage \"storage-1\" already registered")

		for i := raftID(1); i <= 3; i++ {
			clusterInfo, err := groups[i].ClusterInfo()
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.Cluster{
				ClusterId:     cluster.clusterID,
				NextStorageId: 2,
				Storages: map[uint64]*gitalypb.Storage{
					1: {StorageId: 1, Name: "storage-1"},
				},
			}, clusterInfo)
		}
	})
}
