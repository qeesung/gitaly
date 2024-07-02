package raft

import (
	"testing"

	"github.com/lni/dragonboat/v4/statemachine"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

func wrapSMMessage(t *testing.T, msg proto.Message) []byte {
	result, err := anyProtoMarshal(msg)
	require.NoError(t, err)

	return result
}

func TestMetadataStatemachine_Open(t *testing.T) {
	t.Parallel()

	t.Run("the DB is freshly new", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))

		lastApplied, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)
		require.Equal(t, uint64(0), lastApplied)
	})

	t.Run("re-open an existing DB", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		opReq := statemachine.Entry{Index: 1, Cmd: wrapSMMessage(t, &gitalypb.BootstrapClusterRequest{ClusterId: "1234"})}
		opRes := statemachine.Entry{Index: 1, Result: statemachine.Result{
			Value: uint64(resultClusterBootstrapSuccessful),
			Data: wrapSMMessage(t, &gitalypb.BootstrapClusterResponse{
				Cluster: &gitalypb.Cluster{
					ClusterId:     "1234",
					NextStorageId: 1,
				},
			}),
		}}

		result, err := sm.Update([]statemachine.Entry{opReq})
		require.NoError(t, err)
		require.Equal(t, []statemachine.Entry{opRes}, result)

		require.NoError(t, sm.Close())

		lastApplied, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)
		require.Equal(t, uint64(1), lastApplied)
	})

	t.Run("multiple statemachines co-exist", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		sm2 := newMetadataStatemachine(1, 2, dbForGroup(db, 1, 2))
		_, err = sm2.Open(make(<-chan struct{}))
		require.NoError(t, err)

		opReq := statemachine.Entry{Index: 1, Cmd: wrapSMMessage(t, &gitalypb.BootstrapClusterRequest{ClusterId: "1234"})}
		opRes := statemachine.Entry{Index: 1, Result: statemachine.Result{
			Value: uint64(resultClusterBootstrapSuccessful),
			Data: wrapSMMessage(t, &gitalypb.BootstrapClusterResponse{
				Cluster: &gitalypb.Cluster{
					ClusterId:     "1234",
					NextStorageId: 1,
				},
			}),
		}}

		result, err := sm.Update([]statemachine.Entry{opReq})
		require.NoError(t, err)
		require.Equal(t, []statemachine.Entry{opRes}, result)

		lastApplied, err := sm.LastApplied()
		require.NoError(t, err)
		require.Equal(t, raftID(1), lastApplied)

		lastApplied, err = sm2.LastApplied()
		require.NoError(t, err)
		require.Equal(t, raftID(0), lastApplied)
	})

	t.Run("the cluster stops when opening statemachine", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))

		stopC := make(chan struct{}, 1)
		stopC <- struct{}{}

		_, err := sm.Open(stopC)
		require.Equal(t, err, statemachine.ErrOpenStopped)
	})

	t.Run("the DB is closed when opening statemachine", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		close()
		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.Contains(t, err.Error(), "DB::Get key")
	})
}

func TestMetadataStateMachine_Update(t *testing.T) {
	t.Parallel()

	bootstrapReq := &gitalypb.BootstrapClusterRequest{ClusterId: "1234"}
	bootstrapRes := &gitalypb.BootstrapClusterResponse{
		Cluster: &gitalypb.Cluster{
			ClusterId:     "1234",
			NextStorageId: 1,
		},
	}

	requireLastApplied := func(t *testing.T, sm *metadataStateMachine, expected raftID) {
		lastApplied, err := sm.LastApplied()
		require.NoError(t, err)
		require.Equal(t, expected, lastApplied)
	}

	requireClusterState := func(t *testing.T, sm *metadataStateMachine, expected *gitalypb.Cluster) {
		cluster, err := sm.Cluster()
		require.NoError(t, err)
		testhelper.ProtoEqual(t, expected, cluster)
	}

	bootstrapCluster := func(t *testing.T, sm *metadataStateMachine) {
		result, err := sm.Update([]statemachine.Entry{{Index: 1, Cmd: wrapSMMessage(t, bootstrapReq)}})
		require.NoError(t, err)
		require.Equal(t, []statemachine.Entry{{Index: 1, Result: statemachine.Result{
			Value: uint64(resultClusterBootstrapSuccessful),
			Data:  wrapSMMessage(t, bootstrapRes),
		}}}, result)
	}

	t.Run("bootstrap a new cluster", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		bootstrapCluster(t, sm)
		requireLastApplied(t, sm, 1)
		requireClusterState(t, sm, &gitalypb.Cluster{
			ClusterId:     "1234",
			NextStorageId: 1,
		})
	})

	t.Run("bootstrap in a bootstrapped cluster", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		bootstrapCluster(t, sm)
		requireLastApplied(t, sm, 1)

		result, err := sm.Update([]statemachine.Entry{{Index: 2, Cmd: wrapSMMessage(t, bootstrapReq)}})
		require.NoError(t, err)
		require.Equal(t, []statemachine.Entry{{Index: 2, Result: statemachine.Result{
			Value: uint64(resultClusterAlreadyBootstrapped),
			Data:  wrapSMMessage(t, bootstrapRes), // No change
		}}}, result)

		requireLastApplied(t, sm, 2)
		requireClusterState(t, sm, &gitalypb.Cluster{
			ClusterId:     "1234",
			NextStorageId: 1,
		})
	})

	t.Run("register a new storage", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		bootstrapCluster(t, sm)
		requireLastApplied(t, sm, 1)

		result, err := sm.Update([]statemachine.Entry{
			{Index: 2, Cmd: wrapSMMessage(t, &gitalypb.RegisterStorageRequest{StorageName: "storage-1"})},
			{Index: 3, Cmd: wrapSMMessage(t, &gitalypb.RegisterStorageRequest{StorageName: "storage-2"})},
		})
		require.NoError(t, err)
		require.Equal(t, []statemachine.Entry{
			{Index: 2, Result: statemachine.Result{
				Value: uint64(resultRegisterStorageSuccessful),
				Data: wrapSMMessage(t, &gitalypb.RegisterStorageResponse{
					Storage: &gitalypb.Storage{
						StorageId: 1,
						Name:      "storage-1",
					},
				}),
			}},
			{Index: 3, Result: statemachine.Result{
				Value: uint64(resultRegisterStorageSuccessful),
				Data: wrapSMMessage(t, &gitalypb.RegisterStorageResponse{
					Storage: &gitalypb.Storage{
						StorageId: 2,
						Name:      "storage-2",
					},
				}),
			}},
		}, result)

		requireLastApplied(t, sm, 3)
		requireClusterState(t, sm, &gitalypb.Cluster{
			ClusterId:     "1234",
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
		})
	})

	t.Run("register an already registered storage", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		bootstrapCluster(t, sm)
		requireLastApplied(t, sm, 1)

		result, err := sm.Update([]statemachine.Entry{
			{Index: 2, Cmd: wrapSMMessage(t, &gitalypb.RegisterStorageRequest{StorageName: "storage-1"})},
			{Index: 3, Cmd: wrapSMMessage(t, &gitalypb.RegisterStorageRequest{StorageName: "storage-1"})},
		})
		require.NoError(t, err)
		require.Equal(t, []statemachine.Entry{
			{Index: 2, Result: statemachine.Result{
				Value: uint64(resultRegisterStorageSuccessful),
				Data: wrapSMMessage(t, &gitalypb.RegisterStorageResponse{
					Storage: &gitalypb.Storage{
						StorageId: 1,
						Name:      "storage-1",
					},
				}),
			}},
			{Index: 3, Result: statemachine.Result{
				Value: uint64(resultStorageAlreadyRegistered),
			}},
		}, result)

		requireLastApplied(t, sm, 3)
		requireClusterState(t, sm, &gitalypb.Cluster{
			ClusterId:     "1234",
			NextStorageId: 2,
			Storages: map[uint64]*gitalypb.Storage{
				1: {
					StorageId: 1,
					Name:      "storage-1",
				},
			},
		})
	})

	t.Run("register a storage in a non-bootstrapped cluster", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		result, err := sm.Update([]statemachine.Entry{
			{Index: 1, Cmd: wrapSMMessage(t, &gitalypb.RegisterStorageRequest{StorageName: "storage-1"})},
		})
		require.NoError(t, err)
		require.Equal(t, []statemachine.Entry{
			{Index: 1, Result: statemachine.Result{Value: uint64(resultRegisterStorageClusterNotBootstrappedYet)}},
		}, result)

		requireLastApplied(t, sm, 1)
		requireClusterState(t, sm, &gitalypb.Cluster{})
	})

	t.Run("unsupported request type", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		bootstrapCluster(t, sm)

		_, err = sm.Update([]statemachine.Entry{{Index: 2, Cmd: wrapSMMessage(t, &gitalypb.GetClusterRequest{})}})
		require.EqualError(t, err, "updating entry index 2: request not supported: GetClusterRequest")

		requireLastApplied(t, sm, 1)
		requireClusterState(t, sm, &gitalypb.Cluster{
			ClusterId:     "1234",
			NextStorageId: 1,
		})
	})

	t.Run("entry with already applied log index", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		bootstrapCluster(t, sm)

		_, err = sm.Update([]statemachine.Entry{{Index: 1, Cmd: wrapSMMessage(t, &gitalypb.BootstrapClusterRequest{ClusterId: "1234"})}})
		require.EqualError(t, err, "log entry with previously applied index, last applied 1 entry index 1")

		requireLastApplied(t, sm, 1)
		requireClusterState(t, sm, &gitalypb.Cluster{
			ClusterId:     "1234",
			NextStorageId: 1,
		})
	})
}

func TestMetadataStateMachine_Lookup(t *testing.T) {
	t.Parallel()

	bootstrapReq := &gitalypb.BootstrapClusterRequest{ClusterId: "1234"}
	bootstrapRes := &gitalypb.BootstrapClusterResponse{
		Cluster: &gitalypb.Cluster{
			ClusterId:     "1234",
			NextStorageId: 1,
		},
	}

	bootstrapCluster := func(t *testing.T, sm *metadataStateMachine) {
		result, err := sm.Update([]statemachine.Entry{{Index: 1, Cmd: wrapSMMessage(t, bootstrapReq)}})
		require.NoError(t, err)
		require.Equal(t, []statemachine.Entry{{Index: 1, Result: statemachine.Result{
			Value: uint64(resultClusterBootstrapSuccessful),
			Data:  wrapSMMessage(t, bootstrapRes),
		}}}, result)
	}

	t.Run("get a non-bootstrapped cluster", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		response, err := sm.Lookup(&gitalypb.GetClusterRequest{})
		require.NoError(t, err)

		testhelper.ProtoEqual(t, &gitalypb.GetClusterResponse{}, response)
	})

	t.Run("get a newly bootstrapped cluster", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		bootstrapCluster(t, sm)

		response, err := sm.Lookup(&gitalypb.GetClusterRequest{})
		require.NoError(t, err)

		testhelper.ProtoEqual(t, &gitalypb.GetClusterResponse{Cluster: &gitalypb.Cluster{
			ClusterId:     "1234",
			NextStorageId: 1,
		}}, response)
	})

	t.Run("get an established cluster", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		bootstrapCluster(t, sm)
		_, err = sm.Update([]statemachine.Entry{
			{Index: 2, Cmd: wrapSMMessage(t, &gitalypb.RegisterStorageRequest{StorageName: "storage-1"})},
			{Index: 3, Cmd: wrapSMMessage(t, &gitalypb.RegisterStorageRequest{StorageName: "storage-2"})},
		})
		require.NoError(t, err)

		response, err := sm.Lookup(&gitalypb.GetClusterRequest{})
		require.NoError(t, err)

		testhelper.ProtoEqual(t, &gitalypb.GetClusterResponse{Cluster: &gitalypb.Cluster{
			ClusterId:     "1234",
			NextStorageId: 3,
			Storages: map[uint64]*gitalypb.Storage{
				1: {StorageId: 1, Name: "storage-1"},
				2: {StorageId: 2, Name: "storage-2"},
			},
		}}, response)
	})

	t.Run("unsupported request type", func(t *testing.T) {
		t.Parallel()

		cfg := testcfg.Build(t)
		db, close := setupStorageDB(t, cfg)
		defer close()

		sm := newMetadataStatemachine(1, 1, dbForGroup(db, 1, 1))
		_, err := sm.Open(make(<-chan struct{}))
		require.NoError(t, err)

		_, err = sm.Lookup(&gitalypb.BootstrapClusterRequest{})
		require.EqualError(t, err, "request not supported: *gitalypb.BootstrapClusterRequest")
	})
}
