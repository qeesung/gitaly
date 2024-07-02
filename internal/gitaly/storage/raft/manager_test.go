package raft

import (
	"fmt"
	"testing"

	"github.com/lni/dragonboat/v4"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func managerTestConfig(bootstrap bool) ManagerConfig {
	return ManagerConfig{
		BootstrapCluster: bootstrap,
		expertConfig:     dragonboatTestingProfile,
	}
}

func TestManager_Start(t *testing.T) {
	t.Parallel()

	startManager := func(t *testing.T) nodeStarter {
		return func(cluster *testRaftCluster, node raftID) (*testNode, error) {
			ctx := testhelper.Context(t)

			cfg := testcfg.Build(t, testcfg.WithStorages(fmt.Sprintf("storage-%d", node)))
			logger := testhelper.NewLogger(t)

			dbMgr := setupTestDB(t, cfg)
			t.Cleanup(dbMgr.Close)

			mgr, err := NewManager(ctx, cfg.Storages, cluster.createRaftConfig(node), managerTestConfig(true), dbMgr, logger)
			if err != nil {
				return nil, err
			}

			return &testNode{
				manager: mgr,
				close:   mgr.Close,
			}, nil
		}
	}

	resetManager := func(t *testing.T, m *Manager) {
		m.metadataGroup = nil
		for _, storageMgr := range m.storageManagers {
			storageMgr.clearStorageID()
			storageMgr.nodeHost.Close()
			nodeHost, err := dragonboat.NewNodeHost(storageMgr.nodeHost.NodeHostConfig())
			require.NoError(t, err)
			storageMgr.nodeHost = nodeHost
		}
		m.started.Store(false)
		m.closed.Store(false)
	}

	t.Run("bootstrap a singular cluster", func(t *testing.T) {
		t.Parallel()

		cluster := newTestRaftCluster(t, 1, withNodeStarter(startManager(t)))
		defer cluster.closeAll()

		require.NoError(t, cluster.nodes[1].manager.Start())
		require.Equal(t, raftID(1), cluster.nodes[1].manager.firstStorage.id)

		clusterInfo, err := cluster.nodes[1].manager.ClusterInfo()
		require.NoError(t, err)

		testhelper.ProtoEqual(t, &gitalypb.Cluster{
			ClusterId:     cluster.clusterID,
			NextStorageId: 2,
			Storages: map[uint64]*gitalypb.Storage{
				1: {StorageId: 1, Name: "storage-1"},
			},
		}, clusterInfo)
	})

	t.Run("bootstrap a cluster twice", func(t *testing.T) {
		t.Parallel()

		cluster := newTestRaftCluster(t, 1, withNodeStarter(startManager(t)))
		defer cluster.closeAll()

		require.NoError(t, cluster.nodes[1].manager.Start())
		require.Equal(t, raftID(1), cluster.nodes[1].manager.firstStorage.id)

		require.EqualError(t, cluster.nodes[1].manager.Start(), "raft manager already started")
	})

	for _, numNode := range []int{3, 5} {
		func(numNode int) {
			t.Run(fmt.Sprintf("bootstrap a %d-node cluster", numNode), func(t *testing.T) {
				t.Parallel()

				cluster := newTestRaftCluster(t, numNode, withNodeStarter(startManager(t)))
				defer cluster.closeAll()

				fanOut(numNode, func(node raftID) {
					require.NoError(t, cluster.nodes[node].manager.Start())
				})

				var expectedIDs, allocatedIDs []raftID
				for i := raftID(1); i <= raftID(numNode); i++ {
					expectedIDs = append(expectedIDs, i)
					allocatedIDs = append(allocatedIDs, cluster.nodes[i].manager.firstStorage.id)
				}
				require.ElementsMatch(t, expectedIDs, allocatedIDs)

				fanOut(numNode, func(node raftID) {
					mgr := cluster.nodes[node].manager
					storage := mgr.firstStorage

					clusterInfo, err := mgr.ClusterInfo()
					require.NoError(t, err)

					require.Equal(t, cluster.clusterID, clusterInfo.ClusterId)
					require.Equal(t, uint64(numNode+1), clusterInfo.NextStorageId)
					require.Equal(t, &gitalypb.Storage{
						StorageId: storage.id.ToUint64(), Name: storage.name,
					}, clusterInfo.Storages[storage.id.ToUint64()])
				})
			})
		}(numNode)
	}

	t.Run("bootstrap a 3-node cluster having 2 available nodes (quorum is reached)", func(t *testing.T) {
		for _, bootstrap := range []bool{true, false} {
			func(bootstrap bool) {
				t.Run(fmt.Sprintf("last node sets bootstrap = %v", bootstrap), func(t *testing.T) {
					t.Parallel()

					cluster := newTestRaftCluster(t, 3, withNodeStarter(startManager(t)))
					defer cluster.closeAll()

					fanOut(2, func(node raftID) {
						require.NoError(t, cluster.nodes[node].manager.Start())
						require.Equal(t, true, cluster.nodes[node].manager.Ready())
					})

					// The quorum is reached
					require.ElementsMatch(t, []raftID{1, 2}, []raftID{
						cluster.nodes[1].manager.firstStorage.id,
						cluster.nodes[2].manager.firstStorage.id,
					})

					fanOut(2, func(node raftID) {
						mgr := cluster.nodes[node].manager
						storage := mgr.firstStorage

						clusterInfo, err := mgr.ClusterInfo()
						require.NoError(t, err)

						require.Equal(t, cluster.clusterID, clusterInfo.ClusterId)
						require.Equal(t, uint64(3), clusterInfo.NextStorageId)
						require.Equal(t, &gitalypb.Storage{
							StorageId: storage.id.ToUint64(), Name: storage.name,
						}, clusterInfo.Storages[storage.id.ToUint64()])
					})

					// Now the third node joins. It does not matter whether the third node bootstraps the cluster.
					cluster.nodes[3].manager.managerConfig.BootstrapCluster = bootstrap
					require.NoError(t, cluster.nodes[3].manager.Start())

					fanOut(3, func(node raftID) {
						mgr := cluster.nodes[node].manager
						storage := mgr.firstStorage

						clusterInfo, err := mgr.ClusterInfo()
						require.NoError(t, err)

						require.Equal(t, cluster.clusterID, clusterInfo.ClusterId)
						require.Equal(t, uint64(4), clusterInfo.NextStorageId)
						require.Equal(t, &gitalypb.Storage{
							StorageId: storage.id.ToUint64(), Name: storage.name,
						}, clusterInfo.Storages[storage.id.ToUint64()])
					})
				})
			}(bootstrap)
		}
	})

	t.Run("bootstrap a cluster having a duplicated storage", func(t *testing.T) {
		t.Parallel()

		waits := map[raftID]chan struct{}{
			1: make(chan struct{}),
			2: make(chan struct{}),
			3: make(chan struct{}),
		}
		const duplicatedNode = 3

		ctx := testhelper.Context(t)
		cluster := newTestRaftCluster(t, 3, withNodeStarter(func(cluster *testRaftCluster, node raftID) (*testNode, error) {
			storageName := fmt.Sprintf("storage-%d", node)
			if node == duplicatedNode {
				storageName = "storage-2"
			}
			cfg := testcfg.Build(t, testcfg.WithStorages(storageName))
			logger := testhelper.NewLogger(t)

			dbMgr := setupTestDB(t, cfg)
			t.Cleanup(dbMgr.Close)

			mgrCfg := managerTestConfig(true)
			mgrCfg.testBeforeRegister = func() {
				<-waits[node]
			}

			mgr, err := NewManager(ctx, cfg.Storages, cluster.createRaftConfig(node), mgrCfg, dbMgr, logger)
			if err != nil {
				return nil, err
			}

			return &testNode{
				manager: mgr,
				close:   mgr.Close,
			}, nil
		}))
		defer cluster.closeAll()

		// Serialize storage registration to 1 (successful) -> 2 (successful) -> 3 (duplicated)
		close(waits[1])
		fanOut(3, func(node raftID) {
			if node == duplicatedNode {
				require.EqualError(t, cluster.nodes[node].manager.Start(), "registering storage ID: storage \"storage-2\" already registered")
			} else {
				require.NoError(t, cluster.nodes[node].manager.Start())
			}

			if node != duplicatedNode {
				close(waits[node+1])
			}
		})

		require.ElementsMatch(t, []raftID{1, 2}, []raftID{
			cluster.nodes[1].manager.firstStorage.id,
			cluster.nodes[2].manager.firstStorage.id,
		})

		fanOut(3, func(node raftID) {
			if node == duplicatedNode {
				_, err := cluster.nodes[node].manager.ClusterInfo()
				require.EqualError(t, err, "raft manager already closed")
			} else {
				mgr := cluster.nodes[node].manager
				storage := mgr.firstStorage

				require.NoError(t, mgr.metadataGroup.WaitReady())
				clusterInfo, err := mgr.ClusterInfo()
				require.NoError(t, err)

				require.Equal(t, cluster.clusterID, clusterInfo.ClusterId)
				require.Equal(t, uint64(3), clusterInfo.NextStorageId)

				require.Equal(t, &gitalypb.Storage{
					StorageId: storage.id.ToUint64(), Name: storage.name,
				}, clusterInfo.Storages[storage.id.ToUint64()])
			}
		})
	})

	t.Run("re-bootstrap a bootstrapped cluster", func(t *testing.T) {
		t.Parallel()

		cluster := newTestRaftCluster(t, 3, withNodeStarter(startManager(t)))
		defer cluster.closeAll()

		fanOut(3, func(node raftID) {
			require.NoError(t, cluster.nodes[node].manager.Start())
		})

		for _, node := range cluster.nodes {
			resetManager(t, node.manager)
		}

		fanOut(3, func(node raftID) {
			mgr := cluster.nodes[node].manager

			require.NoError(t, mgr.Start())

			require.NoError(t, mgr.metadataGroup.WaitReady())
			clusterInfo, err := mgr.ClusterInfo()
			require.NoError(t, err)

			require.Equal(t, cluster.clusterID, clusterInfo.ClusterId)
			require.Equal(t, uint64(4), clusterInfo.NextStorageId)

			require.Equal(t, &gitalypb.Storage{
				StorageId: mgr.firstStorage.id.ToUint64(), Name: mgr.firstStorage.name,
			}, clusterInfo.Storages[mgr.firstStorage.id.ToUint64()])
		})
	})

	t.Run("restart nodes of a bootstrapped cluster", func(t *testing.T) {
		t.Parallel()

		cluster := newTestRaftCluster(t, 3, withNodeStarter(startManager(t)))
		defer cluster.closeAll()

		fanOut(3, func(node raftID) {
			require.NoError(t, cluster.nodes[node].manager.Start())
		})

		for _, node := range cluster.nodes {
			resetManager(t, node.manager)
			node.manager.managerConfig.BootstrapCluster = false
		}

		fanOut(3, func(node raftID) {
			mgr := cluster.nodes[node].manager

			require.NoError(t, mgr.Start())

			require.NoError(t, mgr.metadataGroup.WaitReady())
			clusterInfo, err := mgr.ClusterInfo()
			require.NoError(t, err)

			require.Equal(t, cluster.clusterID, clusterInfo.ClusterId)
			require.Equal(t, uint64(4), clusterInfo.NextStorageId)

			require.Equal(t, &gitalypb.Storage{
				StorageId: mgr.firstStorage.id.ToUint64(), Name: mgr.firstStorage.name,
			}, clusterInfo.Storages[mgr.firstStorage.id.ToUint64()])
		})
	})

	t.Run("a node joins a wrong cluster", func(t *testing.T) {
		t.Parallel()

		cluster := newTestRaftCluster(t, 3, withNodeStarter(startManager(t)))
		defer cluster.closeAll()

		fanOut(2, func(node raftID) {
			require.NoError(t, cluster.nodes[node].manager.Start())
		})

		cluster.nodes[3].manager.managerConfig.BootstrapCluster = false
		cluster.nodes[3].manager.clusterConfig.ClusterID = "wrong-cluster-id"
		require.EqualError(
			t, cluster.nodes[3].manager.Start(),
			fmt.Sprintf("joining the wrong cluster, expected to join \"wrong-cluster-id\" but joined \"%s\"", cluster.clusterID),
		)
	})

	t.Run("bootstrap a cluster having multiple storages per node", func(t *testing.T) {
		t.Parallel()

		ctx := testhelper.Context(t)

		cfg := testcfg.Build(t, testcfg.WithStorages("storage-1", "storage-2"))
		logger := testhelper.NewLogger(t)

		dbMgr := setupTestDB(t, cfg)
		t.Cleanup(dbMgr.Close)

		_, err := NewManager(ctx, cfg.Storages, config.Raft{}, managerTestConfig(true), dbMgr, logger)
		require.EqualError(t, err, "the support for multiple storages is temporarily disabled")
	})
}
