package raft

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backoff"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

func TestRequester_SyncWrite(t *testing.T) {
	t.Parallel()

	defaultRequestOption := requestOption{
		retry:   3,
		timeout: 10 * time.Second,
	}

	expectedReq := &gitalypb.RegisterStorageRequest{StorageName: "storage-1"}
	expectedResp := &gitalypb.RegisterStorageResponse{
		Storage: &gitalypb.Storage{StorageId: 1, Name: "storage-1"},
	}
	updater := func(m proto.Message) (uint64, proto.Message, error) {
		return 0, expectedResp, nil
	}

	t.Run("perform an operation successfully in a singular cluster", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		cluster := newTestRaftCluster(t, 1)
		defer cluster.closeAll()
		cluster.startTestGroup(t, 1, 1, updater, nil)

		requester := NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), defaultRequestOption,
		)
		result, response, err := requester.SyncWrite(ctx, expectedReq)
		require.NoError(t, err)
		require.Equal(t, updateResult(0), result)
		testhelper.ProtoEqual(t, expectedResp, response)

		entries := cluster.nodes[1].sm.getEntries()
		require.Equal(t, 1, len(entries))
		testhelper.ProtoEqual(t, expectedReq, entries[0])
	})

	t.Run("perform an operation successfully in a 3-node cluster", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		// Ensure statemachines of three nodes applied the operation before moving on.
		var wg sync.WaitGroup
		wg.Add(3)
		updater := func(m proto.Message) (uint64, proto.Message, error) {
			defer wg.Done()
			return 0, expectedResp, nil
		}

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{updater, updater, updater}, []mockReaderFunc{nil, nil, nil})

		requester := NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), defaultRequestOption,
		)
		result, response, err := requester.SyncWrite(ctx, expectedReq)
		require.NoError(t, err)
		require.Equal(t, updateResult(0), result)
		testhelper.ProtoEqual(t, expectedResp, response)

		wg.Wait()
		fanOutNodes(cluster, func(node *testNode) {
			entries := node.sm.getEntries()
			require.Equal(t, 1, len(entries))
			testhelper.ProtoEqual(t, expectedReq, entries[0])
		})
	})

	t.Run("statemachine returns an update result", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		count := 0
		updater := func(m proto.Message) (uint64, proto.Message, error) {
			count++
			if count <= 1 {
				return 0, expectedResp, nil
			}
			return 1, expectedResp, nil
		}

		cluster := newTestRaftCluster(t, 1)
		defer cluster.closeAll()
		cluster.startTestGroup(t, 1, 1, updater, nil)

		requester := NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), defaultRequestOption,
		)
		result, response, err := requester.SyncWrite(ctx, expectedReq)
		require.NoError(t, err)
		require.Equal(t, updateResult(0), result)
		testhelper.ProtoEqual(t, expectedResp, response)

		result, response, err = requester.SyncWrite(ctx, expectedReq)
		require.NoError(t, err)
		require.Equal(t, updateResult(1), result)
		testhelper.ProtoEqual(t, expectedResp, response)

		entries := cluster.nodes[1].sm.getEntries()
		require.Equal(t, 2, len(entries))
		testhelper.ProtoEqual(t, expectedReq, entries[0])
		testhelper.ProtoEqual(t, expectedReq, entries[1])
	})

	t.Run("handle context cancellation", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(testhelper.Context(t))
		cancel()

		expectedReq := &gitalypb.RegisterStorageRequest{StorageName: "storage-1"}
		updater := func(m proto.Message) (uint64, proto.Message, error) {
			return 0, &gitalypb.RegisterStorageResponse{}, nil
		}

		cluster := newTestRaftCluster(t, 1)
		defer cluster.closeAll()
		cluster.startTestGroup(t, 1, 1, updater, nil)

		requester := NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), defaultRequestOption,
		)
		_, _, err := requester.SyncWrite(ctx, expectedReq)
		require.Error(t, err)
		require.Contains(t, err.Error(), "context canceled")
	})

	t.Run("perform an operation successfully in a 3-node cluster with 1 unavailable node (quorum is maintained)", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		var wg sync.WaitGroup
		wg.Add(2)
		updater := func(m proto.Message) (uint64, proto.Message, error) {
			defer wg.Done()
			return 0, expectedResp, nil
		}

		cluster := newTestRaftCluster(t, 3)
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{updater, updater, updater}, []mockReaderFunc{nil, nil, nil})
		defer cluster.closeNode(1)
		defer cluster.closeNode(2)

		// Close the last node immediately.
		cluster.closeNode(3)

		require.NoError(t, WaitGroupReady(ctx, cluster.nodes[1].nodeHost, 1))
		requester := NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), defaultRequestOption,
		)
		result, response, err := requester.SyncWrite(ctx, expectedReq)
		require.NoError(t, err)
		require.Equal(t, updateResult(0), result)
		testhelper.ProtoEqual(t, expectedResp, response)

		wg.Wait()
		entries := cluster.nodes[1].sm.getEntries()
		require.Equal(t, 1, len(entries))
		testhelper.ProtoEqual(t, expectedReq, entries[0])

		entries = cluster.nodes[2].sm.getEntries()
		require.Equal(t, 1, len(entries))
		testhelper.ProtoEqual(t, expectedReq, entries[0])

		require.Equal(t, 0, len(cluster.nodes[3].sm.getEntries()))
	})

	t.Run("unable to perform an operation in a 3-node cluster with 2 unavailable nodes (quorum is broken)", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		cluster := newTestRaftCluster(t, 3)
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{updater, updater, updater}, []mockReaderFunc{nil, nil, nil})
		defer cluster.closeNode(1)
		cluster.closeNode(2)
		cluster.closeNode(3)

		// Without retry
		requester := NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), requestOption{
				retry:   0,
				timeout: 1 * time.Second,
			},
		)
		_, _, err := requester.SyncWrite(ctx, expectedReq)
		// Technically, the cluster is unavailable temporarily. The cluster should be back if nodes
		// are available. The requester returns this error so that client makes decision whether it
		// should retry.
		require.Equal(t, err, ErrTemporaryFailure)

		// With retry
		requester = NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), requestOption{
				retry:   3,
				timeout: 1 * time.Second,
			},
		)
		_, _, err = requester.SyncWrite(ctx, expectedReq)
		require.Error(t, err)

		require.Equal(t, 0, len(cluster.nodes[1].sm.getEntries()))
		require.Equal(t, 0, len(cluster.nodes[1].sm.getEntries()))
		require.Equal(t, 0, len(cluster.nodes[2].sm.getEntries()))
	})

	t.Run("retry successful when the cluster recovers after unavailable (quorum is re-established)", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		cluster := newTestRaftCluster(t, 3)
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{updater, updater, updater}, []mockReaderFunc{nil, nil, nil})
		defer cluster.closeNode(1)
		defer cluster.closeNode(2)
		cluster.stopGroup(t, 2, 1)
		cluster.closeNode(3)

		requester := NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), requestOption{
				retry:   0,
				timeout: 1 * time.Second,
			},
		)
		_, _, err := requester.SyncWrite(ctx, expectedReq)
		require.Equal(t, err, ErrTemporaryFailure)

		retryCount := 0
		requester = NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), requestOption{
				retry:   3,
				timeout: 1 * time.Second,
				failureCallback: func(err error) {
					retryCount++
					if retryCount == 1 {
						cluster.startTestGroup(t, 2, 1, updater, nil)
					}
				},
			},
		)

		result, response, err := requester.SyncWrite(ctx, expectedReq)
		require.NoError(t, err)
		require.Equal(t, updateResult(0), result)
		testhelper.ProtoEqual(t, expectedResp, response)
	})

	t.Run("statemachine returns invalid type", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		updater := func(m proto.Message) (uint64, proto.Message, error) {
			return 0, &gitalypb.Storage{StorageId: 1, Name: "storage-1"}, nil
		}

		cluster := newTestRaftCluster(t, 1)
		cluster.startTestGroup(t, 1, 1, updater, nil)
		defer cluster.closeAll()

		fanOutNodes(cluster, func(node *testNode) {
			requester := NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
				node.nodeHost, 1, testhelper.NewLogger(t), requestOption{
					timeout: 1 * time.Second,
				},
			)
			_, _, err := requester.SyncWrite(ctx, expectedReq)
			require.EqualError(t, err, "casting SyncWrite response to desired type, expected RegisterStorageResponse got Storage")
		})
	})
}

func TestRequester_SyncRead(t *testing.T) {
	t.Parallel()

	defaultRequestOption := requestOption{
		retry:   3,
		timeout: 10 * time.Second,
	}

	expectedReq := &gitalypb.GetClusterRequest{}
	expectedResp := &gitalypb.GetClusterResponse{
		Cluster: &gitalypb.Cluster{
			ClusterId:     "cluster-1",
			NextStorageId: 2,
			Storages: map[uint64]*gitalypb.Storage{
				1: {
					StorageId: 1,
					Name:      "storage-1",
				},
			},
		},
	}

	reader := func(t *testing.T) mockReaderFunc {
		return func(m proto.Message) (proto.Message, error) {
			testhelper.ProtoEqual(t, expectedReq, m)
			return expectedResp, nil
		}
	}

	t.Run("perform an operation successfully in a singular cluster", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		cluster := newTestRaftCluster(t, 1)
		defer cluster.closeAll()
		cluster.startTestGroup(t, 1, 1, nil, reader(t))

		requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), defaultRequestOption,
		)
		response, err := requester.SyncRead(ctx, expectedReq)
		require.NoError(t, err)
		testhelper.ProtoEqual(t, expectedResp, response)
	})

	t.Run("perform an operation successfully in a 3-node cluster", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{nil, nil, nil}, []mockReaderFunc{reader(t), reader(t), reader(t)})

		fanOutNodes(cluster, func(node *testNode) {
			requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
				node.nodeHost, 1, testhelper.NewLogger(t), defaultRequestOption,
			)
			response, err := requester.SyncRead(ctx, expectedReq)
			require.NoError(t, err)
			testhelper.ProtoEqual(t, expectedResp, response)
		})
	})

	t.Run("handle context cancellation", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(testhelper.Context(t))
		cancel()

		cluster := newTestRaftCluster(t, 1)
		defer cluster.closeAll()
		cluster.startTestGroup(t, 1, 1, nil, reader(t))

		requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), defaultRequestOption,
		)
		_, err := requester.SyncRead(ctx, expectedReq)
		require.Error(t, err)
		require.Contains(t, err.Error(), "context canceled")
	})

	t.Run("perform an operation successfully in a 3-node cluster with 1 unavailable node (quorum is maintained)", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		cluster := newTestRaftCluster(t, 3)
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{nil, nil, nil}, []mockReaderFunc{reader(t), reader(t), reader(t)})
		defer cluster.closeNode(1)
		defer cluster.closeNode(2)

		// Close the last node immediately.
		cluster.closeNode(3)

		fanOut(2, func(node raftID) {
			requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
				cluster.nodes[node].nodeHost, 1, testhelper.NewLogger(t), defaultRequestOption,
			)

			require.NoError(t, WaitGroupReady(ctx, cluster.nodes[node].nodeHost, 1))
			response, err := requester.SyncRead(ctx, expectedReq)
			require.NoError(t, err)
			testhelper.ProtoEqual(t, expectedResp, response)
		})

		requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
			cluster.nodes[raftID(3)].nodeHost, 1, testhelper.NewLogger(t), defaultRequestOption,
		)
		_, err := requester.SyncRead(ctx, expectedReq)
		require.EqualError(t, err, "retry exhausted: dragonboat: closed")
	})

	t.Run("perform an operation successfully in a 3-node cluster with 2 unavailable nodes (quorum is broken)", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		cluster := newTestRaftCluster(t, 3)
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{nil, nil, nil}, []mockReaderFunc{reader(t), reader(t), reader(t)})
		defer cluster.closeNode(1)
		cluster.closeNode(2)
		cluster.closeNode(3)

		requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), requestOption{
				retry:   3,
				timeout: 1 * time.Second,
			},
		)
		_, err := requester.SyncRead(ctx, expectedReq)
		require.EqualError(t, err, "retry exhausted: timeout")

		for i := 2; i <= 3; i++ {
			requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
				cluster.nodes[raftID(i)].nodeHost, 1, testhelper.NewLogger(t), defaultRequestOption,
			)
			_, err := requester.SyncRead(ctx, expectedReq)
			require.EqualError(t, err, "retry exhausted: dragonboat: closed")
		}
	})

	t.Run("retry successful when the cluster recovers after unavailable (quorum is re-established)", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		cluster := newTestRaftCluster(t, 3)
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{nil, nil, nil}, []mockReaderFunc{reader(t), reader(t), reader(t)})
		defer cluster.closeNode(1)
		defer cluster.closeNode(2)
		cluster.stopGroup(t, 2, 1)
		cluster.closeNode(3)

		requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), requestOption{
				timeout: 1 * time.Second,
			},
		)
		_, err := requester.SyncRead(ctx, expectedReq)
		require.Equal(t, err, ErrTemporaryFailure)

		retryCount := 0
		requester = NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), requestOption{
				retry:   3,
				timeout: 1 * time.Second,
				failureCallback: func(err error) {
					retryCount++
					if retryCount == 1 {
						cluster.startTestGroup(t, 2, 1, nil, reader(t))
					}
				},
			},
		)

		response, err := requester.SyncRead(ctx, expectedReq)
		require.NoError(t, err)
		testhelper.ProtoEqual(t, expectedResp, response)
	})

	t.Run("read changes from other nodes", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		sm := func() (mockUpdaterFunc, mockReaderFunc) {
			cluster := &gitalypb.Cluster{
				ClusterId:     "cluster-1",
				NextStorageId: 2,
				Storages: map[uint64]*gitalypb.Storage{
					1: {
						StorageId: 1,
						Name:      "storage-1",
					},
				},
			}
			return func(m proto.Message) (uint64, proto.Message, error) {
					req, ok := m.(*gitalypb.RegisterStorageRequest)
					require.True(t, ok)

					newStorage := &gitalypb.Storage{StorageId: cluster.NextStorageId, Name: req.StorageName}
					cluster.NextStorageId++
					cluster.Storages[newStorage.StorageId] = newStorage

					return 0, &gitalypb.RegisterStorageResponse{Storage: newStorage}, nil
				}, func(m proto.Message) (proto.Message, error) {
					return &gitalypb.GetClusterResponse{Cluster: cluster}, nil
				}
		}

		var updaters []mockUpdaterFunc
		var readers []mockReaderFunc

		for i := 1; i <= 3; i++ {
			updater, reader := sm()
			updaters = append(updaters, updater)
			readers = append(readers, reader)
		}

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, updaters, readers)

		fanOutNodes(cluster, func(node *testNode) {
			requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
				node.nodeHost, 1, testhelper.NewLogger(t), requestOption{
					timeout: 1 * time.Second,
				},
			)
			response, err := requester.SyncRead(ctx, expectedReq)
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.GetClusterResponse{
				Cluster: &gitalypb.Cluster{
					ClusterId:     "cluster-1",
					NextStorageId: 2,
					Storages: map[uint64]*gitalypb.Storage{
						1: {
							StorageId: 1,
							Name:      "storage-1",
						},
					},
				},
			}, response)
		})

		requester := NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), requestOption{
				timeout: 1 * time.Second,
			},
		)

		result, response, err := requester.SyncWrite(ctx, &gitalypb.RegisterStorageRequest{StorageName: "storage-2"})
		require.NoError(t, err)
		require.Equal(t, updateResult(0), result)
		testhelper.ProtoEqual(t, &gitalypb.RegisterStorageResponse{Storage: &gitalypb.Storage{StorageId: 2, Name: "storage-2"}}, response)

		fanOutNodes(cluster, func(node *testNode) {
			requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
				node.nodeHost, 1, testhelper.NewLogger(t), requestOption{
					timeout: 1 * time.Second,
				},
			)
			response, err := requester.SyncRead(ctx, expectedReq)
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.GetClusterResponse{
				Cluster: &gitalypb.Cluster{
					ClusterId:     "cluster-1",
					NextStorageId: 3,
					Storages: map[uint64]*gitalypb.Storage{
						1: {
							StorageId: 1,
							Name:      "storage-1",
						},
						2: {
							StorageId: 2,
							Name:      "storage-2",
						},
					},
				},
			}, response)
		})
	})

	t.Run("statemachine returns invalid type", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		reader := func(proto.Message) (proto.Message, error) {
			return &gitalypb.Storage{StorageId: 1, Name: "storage-1"}, nil
		}

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{nil, nil, nil}, []mockReaderFunc{reader, reader, reader})

		fanOutNodes(cluster, func(node *testNode) {
			requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
				node.nodeHost, 1, testhelper.NewLogger(t), requestOption{
					timeout: 1 * time.Second,
				},
			)
			_, err := requester.SyncRead(ctx, expectedReq)
			require.EqualError(t, err, "casting SyncRead response to desired type, expected GetClusterResponse")
		})
	})

	t.Run("statemachine returns an error", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		reader := func(proto.Message) (proto.Message, error) {
			return nil, fmt.Errorf("something goes wrong")
		}

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{nil, nil, nil}, []mockReaderFunc{reader, reader, reader})

		fanOutNodes(cluster, func(node *testNode) {
			requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
				node.nodeHost, 1, testhelper.NewLogger(t), requestOption{
					timeout: 1 * time.Second,
				},
			)
			_, err := requester.SyncRead(ctx, expectedReq)
			require.EqualError(t, err, "failed to send request: something goes wrong")
		})
	})

	t.Run("statemachine returns nil", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		reader := func(proto.Message) (proto.Message, error) {
			return nil, nil
		}

		cluster := newTestRaftCluster(t, 3)
		defer cluster.closeAll()
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{nil, nil, nil}, []mockReaderFunc{reader, reader, reader})

		fanOutNodes(cluster, func(node *testNode) {
			requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
				node.nodeHost, 1, testhelper.NewLogger(t), requestOption{
					timeout: 1 * time.Second,
				},
			)
			response, err := requester.SyncRead(ctx, expectedReq)
			require.NoError(t, err)
			require.Nil(t, response)
		})
	})
}

func TestRequester_exponential(t *testing.T) {
	t.Parallel()

	expectedReq := &gitalypb.GetClusterRequest{}
	expectedResp := &gitalypb.GetClusterResponse{
		Cluster: &gitalypb.Cluster{
			ClusterId:     "cluster-1",
			NextStorageId: 2,
			Storages: map[uint64]*gitalypb.Storage{
				1: {
					StorageId: 1,
					Name:      "storage-1",
				},
			},
		},
	}

	t.Run("successful with exponential backoff", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		reader := func(proto.Message) (proto.Message, error) {
			return expectedResp, nil
		}

		cluster := newTestRaftCluster(t, 3)
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{nil, nil, nil}, []mockReaderFunc{reader, reader, reader})
		defer cluster.closeNode(1)
		defer cluster.closeNode(2)
		cluster.stopGroup(t, 2, 1)
		cluster.closeNode(3)

		wait := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				t.Error("test exits prematurely")
			case <-time.After(2 * time.Second):
				cluster.startTestGroup(t, 2, 1, nil, reader)
				close(wait)
			}
		}()

		requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), requestOption{
				retry:       10,
				timeout:     500 * time.Millisecond,
				exponential: backoff.NewDefaultExponential(rand.New(rand.NewSource(time.Now().UnixNano()))),
			},
		)

		response, err := requester.SyncRead(ctx, expectedReq)
		require.NoError(t, err)
		testhelper.ProtoEqual(t, expectedResp, response)
		<-wait
	})

	t.Run("timeout with exponential backoff", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		reader := func(proto.Message) (proto.Message, error) {
			return expectedResp, nil
		}

		cluster := newTestRaftCluster(t, 3)
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{nil, nil, nil}, []mockReaderFunc{reader, reader, reader})
		defer cluster.closeNode(1)
		defer cluster.closeNode(2)
		cluster.stopGroup(t, 2, 1)
		cluster.closeNode(3)

		ctx, cancel := context.WithTimeout(ctx, 1*time.Second)
		defer cancel()

		requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), requestOption{
				retry:       5,
				timeout:     500 * time.Millisecond,
				exponential: backoff.NewDefaultExponential(rand.New(rand.NewSource(time.Now().UnixNano()))),
			},
		)

		_, err := requester.SyncRead(ctx, expectedReq)
		require.Equal(t, context.DeadlineExceeded, err)
	})

	t.Run("context cancelled with exponential backoff", func(t *testing.T) {
		t.Parallel()
		ctx := testhelper.Context(t)

		reader := func(proto.Message) (proto.Message, error) {
			return expectedResp, nil
		}

		cluster := newTestRaftCluster(t, 3)
		cluster.startTestGroups(t, []raftID{1, 2, 3}, []raftID{1, 1, 1}, []mockUpdaterFunc{nil, nil, nil}, []mockReaderFunc{reader, reader, reader})
		defer cluster.closeNode(1)
		defer cluster.closeNode(2)
		cluster.stopGroup(t, 2, 1)
		cluster.closeNode(3)

		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		go func() {
			select {
			case <-ctx.Done():
				t.Error("test exits prematurely")
			case <-time.After(1 * time.Second):
				cancel()
			}
		}()

		requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
			cluster.nodes[1].nodeHost, 1, testhelper.NewLogger(t), requestOption{
				retry:       5,
				timeout:     500 * time.Millisecond,
				exponential: backoff.NewDefaultExponential(rand.New(rand.NewSource(time.Now().UnixNano()))),
			},
		)

		_, err := requester.SyncRead(ctx, expectedReq)
		require.Equal(t, context.Canceled, err)
	})
}
