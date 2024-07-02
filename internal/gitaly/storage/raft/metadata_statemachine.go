package raft

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/dgraph-io/badger/v4"
	"github.com/lni/dragonboat/v4/statemachine"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/keyvalue"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

type metadataStateMachine struct {
	ctx       context.Context
	groupID   raftID
	replicaID raftID
	accessDB  dbAccessor
}

const (
	resultClusterBootstrapSuccessful = updateResult(iota)
	resultClusterAlreadyBootstrapped
	resultRegisterStorageSuccessful
	resultStorageAlreadyRegistered
	resultRegisterStorageClusterNotBootstrappedYet
)

var (
	initialStorageID = raftID(1)
	keyLastApplied   = []byte("applied_lsn")
	keyClusterInfo   = []byte("cluster")
)

// Open initializes the metadata state machine and returns the last applied index. Metadata is
// persisted to disk by the keyvalue package, so we don't maintain a separate in-memory
// representation here.
func (s *metadataStateMachine) Open(stopC <-chan struct{}) (uint64, error) {
	lastApplied, err := s.LastApplied()
	if err != nil {
		return 0, fmt.Errorf("reading last index from DB: %w", err)
	}

	select {
	case <-stopC:
		return 0, statemachine.ErrOpenStopped
	default:
		return lastApplied.ToUint64(), nil
	}
}

// LastApplied returns the last applied index of the state machine.
func (s *metadataStateMachine) LastApplied() (lastApplied raftID, err error) {
	s.accessDB(s.ctx, func(txn keyvalue.ReadWriter) error {
		lastApplied, err = s.getLastIndex(txn)
		if err != nil {
			err = fmt.Errorf("getting cluster from DB: %w", err)
		}
		return err
	})
	return
}

// Cluster returns the latest cluster state of state machine.
func (s *metadataStateMachine) Cluster() (cluster *gitalypb.Cluster, err error) {
	s.accessDB(s.ctx, func(txn keyvalue.ReadWriter) error {
		cluster, err = s.getCluster(txn)
		if err != nil {
			err = fmt.Errorf("getting cluster from DB: %w", err)
		}
		return err
	})
	return
}

// Update applies each entry to the cluster. The Cmd of each entry can be one of the
// following operations:
//   - gitalypb.BootstrapClusterRequest to bootstrap a cluster for the first time.
//   - gitalypb.RegisterStorageRequest to register a new storage.
func (s *metadataStateMachine) Update(entries []statemachine.Entry) (returnedEntries []statemachine.Entry, returnedErr error) {
	if err := s.accessDB(s.ctx, func(txn keyvalue.ReadWriter) error {
		returnedEntries, returnedErr = s.update(txn, entries)
		return returnedErr
	}); err != nil {
		return nil, fmt.Errorf("committing metadata transaction: %w", err)
	}
	return
}

func (s *metadataStateMachine) update(txn keyvalue.ReadWriter, entries []statemachine.Entry) (_ []statemachine.Entry, returnedErr error) {
	cluster, err := s.getCluster(txn)
	if err != nil {
		return nil, fmt.Errorf("reading cluster from DB: %w", err)
	}
	if cluster == nil {
		cluster = &gitalypb.Cluster{}
	}

	lastApplied, err := s.getLastIndex(txn)
	if err != nil {
		return nil, fmt.Errorf("reading cluster from DB: %w", err)
	}

	var returnedEntries []statemachine.Entry
	for _, entry := range entries {
		if lastApplied >= raftID(entry.Index) {
			return nil, fmt.Errorf("log entry with previously applied index, last applied %d entry index %d", lastApplied, entry.Index)
		}
		result, err := s.updateEntry(cluster, &entry)
		if err != nil {
			return nil, fmt.Errorf("updating entry index %d: %w", entry.Index, err)
		}
		returnedEntries = append(returnedEntries, statemachine.Entry{
			Index:  entry.Index,
			Result: *result,
		})
		lastApplied = raftID(entry.Index)
	}
	marshaledCluster, err := proto.Marshal(cluster)
	if err != nil {
		return nil, fmt.Errorf("marshaling cluster: %w", err)
	}
	if err := txn.Set(keyClusterInfo, marshaledCluster); err != nil {
		return nil, fmt.Errorf("setting cluster: %w", err)
	}
	if err := txn.Set(keyLastApplied, lastApplied.MarshalBinary()); err != nil {
		return nil, fmt.Errorf("setting last index: %w", err)
	}
	return returnedEntries, nil
}

// SupportRead returns whether s supports the requested read operation.
func (s *metadataStateMachine) SupportRead(req proto.Message) bool {
	switch req.(type) {
	case *gitalypb.GetClusterRequest:
		return true
	}

	return false
}

// SupportWrite returns whether s supports the requested write operation.
func (s *metadataStateMachine) SupportWrite(req proto.Message) bool {
	switch req.(type) {
	case *gitalypb.BootstrapClusterRequest,
		*gitalypb.RegisterStorageRequest:
		return true
	}

	return false
}

func (s *metadataStateMachine) updateEntry(cluster *gitalypb.Cluster, entry *statemachine.Entry) (*statemachine.Result, error) {
	msg, err := anyProtoUnmarshal(entry.Cmd)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling command: %w", err)
	}

	switch req := msg.(type) {
	case *gitalypb.BootstrapClusterRequest:
		return s.handleBootstrapClusterRequest(req, cluster)
	case *gitalypb.RegisterStorageRequest:
		return s.handleRegisterStorageRequest(req, cluster)
	}

	return nil, fmt.Errorf("request not supported: %s", msg.ProtoReflect().Descriptor().Name())
}

func (s *metadataStateMachine) handleBootstrapClusterRequest(req *gitalypb.BootstrapClusterRequest, cluster *gitalypb.Cluster) (*statemachine.Result, error) {
	var result statemachine.Result

	if cluster.ClusterId == "" {
		cluster.ClusterId = req.GetClusterId()
		cluster.NextStorageId = initialStorageID.ToUint64()
		result.Value = uint64(resultClusterBootstrapSuccessful)
	} else {
		result.Value = uint64(resultClusterAlreadyBootstrapped)
	}

	response, err := anyProtoMarshal(&gitalypb.BootstrapClusterResponse{Cluster: cluster})
	if err != nil {
		return nil, fmt.Errorf("marshaling bootstrap response: %w", err)
	}
	result.Data = response
	return &result, nil
}

func (s *metadataStateMachine) handleRegisterStorageRequest(req *gitalypb.RegisterStorageRequest, cluster *gitalypb.Cluster) (*statemachine.Result, error) {
	var result statemachine.Result

	if cluster.ClusterId == "" || cluster.NextStorageId == 0 {
		result.Value = uint64(resultRegisterStorageClusterNotBootstrappedYet)
		return &result, nil
	}

	if cluster.Storages == nil {
		cluster.Storages = map[uint64]*gitalypb.Storage{}
	}
	for _, storage := range cluster.Storages {
		if storage.GetName() == req.GetStorageName() {
			result.Value = uint64(resultStorageAlreadyRegistered)
			return &result, nil
		}
	}

	newStorage := &gitalypb.Storage{
		StorageId: cluster.NextStorageId,
		Name:      req.StorageName,
	}
	cluster.Storages[cluster.NextStorageId] = newStorage
	cluster.NextStorageId++

	response, err := anyProtoMarshal(&gitalypb.RegisterStorageResponse{Storage: newStorage})
	if err != nil {
		return nil, fmt.Errorf("marshaling register response: %w", err)
	}

	result.Value = uint64(resultRegisterStorageSuccessful)
	result.Data = response
	return &result, nil
}

// Lookup queries the state machine. cmd can be one of:
// - gitalypb.GetClusterRequest
func (s *metadataStateMachine) Lookup(cmd interface{}) (interface{}, error) {
	switch cmd.(type) {
	case *gitalypb.GetClusterRequest:
		cluster, err := s.Cluster()
		if err != nil {
			return nil, err
		}
		return &gitalypb.GetClusterResponse{Cluster: cluster}, nil
	}
	return nil, fmt.Errorf("request not supported: %T", cmd)
}

// Sync is a no-op because our DB flushes to disk on commit.
func (s *metadataStateMachine) Sync() error { return nil }

// PrepareSnapshot is a no-op until we start supporting snapshots.
func (s *metadataStateMachine) PrepareSnapshot() (interface{}, error) {
	return nil, fmt.Errorf("PrepareSnapshot hasn't been not supported")
}

// SaveSnapshot is a no-op until we start supporting snapshots.
func (s *metadataStateMachine) SaveSnapshot(_ interface{}, _ io.Writer, _ <-chan struct{}) error {
	return fmt.Errorf("SaveSnapshot hasn't been not supported")
}

// RecoverFromSnapshot is a no-op until we start supporting snapshots.
func (s *metadataStateMachine) RecoverFromSnapshot(_ io.Reader, _ <-chan struct{}) error {
	return fmt.Errorf("RecoverFromSnapshot hasn't been not supported")
}

// Close is a no-op because our DB is managed externally.
func (s *metadataStateMachine) Close() error { return nil }

func (s *metadataStateMachine) getLastIndex(txn keyvalue.ReadWriter) (raftID, error) {
	var appliedIndex raftID

	item, err := txn.Get(keyLastApplied)
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return 0, nil
		}
		return 0, err
	}
	return appliedIndex, item.Value(func(value []byte) error {
		appliedIndex.UnmarshalBinary(value)
		return nil
	})
}

func (s *metadataStateMachine) getCluster(txn keyvalue.ReadWriter) (*gitalypb.Cluster, error) {
	var cluster gitalypb.Cluster

	item, err := txn.Get(keyClusterInfo)
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &cluster, item.Value(func(value []byte) error { return proto.Unmarshal(value, &cluster) })
}

var _ = Statemachine(&metadataStateMachine{})

func newMetadataStatemachine(ctx context.Context, groupID raftID, replicaID raftID, accessDB dbAccessor) *metadataStateMachine {
	return &metadataStateMachine{
		ctx:       ctx,
		groupID:   groupID,
		replicaID: replicaID,
		accessDB:  accessDB,
	}
}
