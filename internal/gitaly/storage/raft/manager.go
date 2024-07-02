package raft

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"

	"github.com/lni/dragonboat/v4"
	dragonboatConf "github.com/lni/dragonboat/v4/config"
	"github.com/lni/dragonboat/v4/statemachine"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

// storageManager is responsible for managing the Raft storage for a single storage. It provides a
// keyvalue.Transactioner for each Raft group, allowing the Raft groups to store their data in the
// underlying keyvalue store.
type storageManager struct {
	id       raftID
	name     string
	ptnMgr   *storagemgr.PartitionManager
	nodeHost *dragonboat.NodeHost
}

// Group is an abstract data structure that stores information of a Raft group.
type Group struct {
	ctx           context.Context
	groupID       raftID
	replicaID     raftID
	clusterConfig config.Raft
	groupConfig   dragonboatConf.Config
	logger        log.Logger
	nodeHost      *dragonboat.NodeHost
	statemachine  Statemachine
}

// Statemachine is an interface that wraps dragonboat's statemachine. It is a superset of
// dragonboat's IOnDiskStateMachine interface.
type Statemachine interface {
	// This interface has the following functions.
	statemachine.IOnDiskStateMachine
	// Open implements Open function of IOnDiskStateMachine interface. It opens the existing on disk
	// state machine to be used or it creates a new state machine with empty state if it does not exist.
	// Open returns the most recent index value of the Raft log that has been persisted, or it returns 0
	// when the state machine is a new one.
	// Open(<-chan struct{}) (uint64, error)

	// Update implements Update function of IOnDiskStateMachine instance. The input Entry slice is a
	// list of continuous proposed and committed commands from clients. At this point, the input entries
	// are finalized and acknowledged by all replicas. The application is responsible for validating the
	// data before submitting the log for replication. The statemachine must handle known application
	// errors, races, conflicts, etc. and return the result back. The log entry is still applied but
	// not necessarily leads to any changes. Otherwise, the cluster is unable to move on without
	// manual interventions. The error is returned if there is a non-recoverable problem with
	// underlying storage so that the log entries will be retried later.
	// The library guarantees linearizable access to this function and the monotonic index
	// increment. It's worth nothing that the indices are not necessarily continuous because the
	// library might include some internal operations which are transparent to the statemachine.
	// Update([]statemachine.Entry) ([]statemachine.Entry, error)

	// Lookup queries the state of the IOnDiskStateMachine instance. The caller is guaranteed to be
	// on the same node of this statemachine. So, the request and response are in protobuf format.
	// No need to marshal it back and forth.
	// Lookup(interface{}) (interface{}, error)

	// Sync synchronizes all in-core state of the state machine to persisted storage so the state
	// machine can continue from its latest state after reboot. Our underlying DB flushes to disk right
	// after a transaction finishes.
	// Sync() error

	// PrepareSnapshot prepares the snapshot to be concurrently captured and streamed. The
	// implemented struct must create a snapshot including all entries before the last index at the
	// time this function is called exclusively. The statemachine will continue to accept new
	// updates while the snapshot is being created.
	// PrepareSnapshot() (interface{}, error)

	// SaveSnapshot saves the point in time state of the IOnDiskStateMachine
	// instance identified by the input state identifier, which is usually not
	// the latest state of the IOnDiskStateMachine instance, to the provided
	// io.Writer.
	// SaveSnapshot(interface{}, io.Writer, <-chan struct{}) error

	// RecoverFromSnapshot recovers the state of the IOnDiskStateMachine instance
	// from a snapshot captured by the SaveSnapshot() method on a remote node.
	// RecoverFromSnapshot(io.Reader, <-chan struct{}) error

	// Close closes the IOnDiskStateMachine instance. Close is invoked when the
	// state machine is in a ready-to-exit state in which there will be no further
	// call to the Update, Sync, PrepareSnapshot, SaveSnapshot and the
	// RecoverFromSnapshot method.
	// Our DB is managed by an outsider manager. So, this is a no-op.
	// Close() error

	// LastApplied returns the last applied index of the state machine.
	LastApplied() (raftID, error)
	// SupportRead returns if the statemachine supports input read operation.
	SupportRead(proto.Message) bool
	// SupportWrite returns if the statemachine supports input write operation.
	SupportWrite(proto.Message) bool
}

// ManagerConfig contains the configuration options for the Raft manager.
type ManagerConfig struct {
	// BootstrapCluster tells the manager to bootstrap the cluster when it starts.
	BootstrapCluster bool
	// expertConfig contains advanced configuration for dragonboat. Used for testing only.
	expertConfig dragonboatConf.ExpertConfig
	// testBeforeRegister triggers a callback before registering a storage. Used for testing only.
	testBeforeRegister func()
}

// Manager is responsible for managing the Raft cluster for all storages.
type Manager struct {
	ctx           context.Context
	clusterConfig config.Raft
	managerConfig ManagerConfig
	logger        log.Logger
	started       atomic.Bool
	closed        atomic.Bool

	storageManagers map[string]*storageManager
	firstStorage    *storageManager
	metadataGroup   *metadataRaftGroup
}

func walDir(storageConfig config.Storage) string {
	return filepath.Join(storageConfig.Path, config.GitalyDataPrefix, "raft", "wal")
}

func nodeHostDir(storageConfig config.Storage) string {
	return filepath.Join(storageConfig.Path, config.GitalyDataPrefix, "raft", "node")
}

// NewManager creates a new Raft manager that manages the Raft storage for all configured storages.
func NewManager(
	ctx context.Context,
	storages []config.Storage,
	clusterCfg config.Raft,
	managerCfg ManagerConfig,
	ptnMgr *storagemgr.PartitionManager,
	logger log.Logger,
) (*Manager, error) {
	SetLogger(logger, true)

	if len(storages) > 1 {
		return nil, fmt.Errorf("the support for multiple storages is temporarily disabled")
	}

	m := &Manager{
		ctx:           ctx,
		clusterConfig: clusterCfg,
		managerConfig: managerCfg,
		logger: logger.WithFields(log.Fields{
			"component":       "raft",
			"raft_component":  "manager",
			"raft_cluster_id": clusterCfg.ClusterID,
			"raft_node_id":    clusterCfg.NodeID,
		}),
		storageManagers: map[string]*storageManager{},
	}

	storage := storages[0]
	nodeHost, err := dragonboat.NewNodeHost(dragonboatConf.NodeHostConfig{
		WALDir:                     walDir(storage),
		NodeHostDir:                nodeHostDir(storage),
		RTTMillisecond:             m.clusterConfig.RTTMilliseconds,
		RaftAddress:                m.clusterConfig.RaftAddr,
		ListenAddress:              m.clusterConfig.RaftAddr,
		DefaultNodeRegistryEnabled: false,
		EnableMetrics:              true,
		RaftEventListener: &raftLogger{
			Logger: m.logger.WithField("raft_component", "system"),
		},
		Expert: managerCfg.expertConfig,
	})
	if err != nil {
		return nil, fmt.Errorf("creating dragonboat nodehost: %w", err)
	}

	m.storageManagers[storage.Name] = newStorageManager(storage.Name, ptnMgr, nodeHost)
	if m.firstStorage == nil {
		m.firstStorage = m.storageManagers[storage.Name]
	}

	return m, nil
}

// Start starts the Raft cluster by:
// - Initializing the node-level Raft object for each storage. It initializes underlying engines,
// networking Raft servers, log databases, etc.
// - Joining the metadata Raft group. This Raft group contains cluster-wide metadata, storage
// registry, replica groups, etc. In the first iteration, all initial members always participate
// in this group.
// - Bootstrapping the Raft cluster if configured to do so. Bootstrapping persists initial cluster
// information via metadata Raft group. These steps require a synchronization between initial nodes.
// The bootstrapping waits until the quorum reaches. Afterward, the cluster is ready; nodes
// (including initial members) are allowed to join. The bootstrapping step is skipped if the node
// detects an existing cluster.
// - Register the node's storage with the metadata Raft group. The metadata Raft group allocates a
// new storage ID for each of them. They persist in their IDs. This type of ID is used for future
// interaction with the cluster.
func (m *Manager) Start() (returnedErr error) {
	if m.started.Load() {
		return fmt.Errorf("raft manager already started")
	}
	defer func() {
		m.started.Store(true)
		if returnedErr != nil {
			m.Close()
		}
	}()

	m.logger.WithFields(log.Fields{
		"raft_config":       m.clusterConfig,
		"raft_manager_conf": m.managerConfig,
	}).Info("Raft cluster is starting")

	// A Gitaly node contains multiple independent storages, and each storage maps to a dragonboat
	// NodeHost instance. A Gitaly node must only host a single metadata group, so by default we use
	// the first storage of the node.
	// We also currently don't support new Gitaly nodes joining the cluster (see
	// https://gitlab.com/groups/gitlab-org/-/epics/13562 for more information), so the initial
	// members of the cluster are also the authority of the metadata group.
	if err := m.initMetadataGroup(m.firstStorage); err != nil {
		return fmt.Errorf("initializing Raft metadata group: %w", err)
	}

	if m.managerConfig.BootstrapCluster {
		cluster, err := m.metadataGroup.BootstrapIfNeeded()
		if err != nil {
			return fmt.Errorf("bootstrapping Raft cluster: %w", err)
		}
		m.logger.WithField("cluster", cluster).Info("Raft cluster bootstrapped")
	}

	// Temporarily, we fetch the cluster info from the metadata Raft group directly. In the future,
	// this node needs to contact a metadata authority.
	// For more information: https://gitlab.com/groups/gitlab-org/-/epics/10864
	cluster, err := m.metadataGroup.ClusterInfo()
	if err != nil {
		return fmt.Errorf("getting cluster info: %w", err)
	}
	if cluster.ClusterId != m.clusterConfig.ClusterID {
		return fmt.Errorf("joining the wrong cluster, expected to join %q but joined %q", m.clusterConfig.ClusterID, cluster.ClusterId)
	}

	if m.managerConfig.testBeforeRegister != nil {
		m.managerConfig.testBeforeRegister()
	}

	// Register storage ID if not exist. Similarly, this operation is handled by the metadata group.
	// It will be handled by the metadata authority in the future.
	for storageName, storageMgr := range m.storageManagers {
		if err := storageMgr.loadStorageID(); err != nil {
			return fmt.Errorf("loading storage ID: %w", err)
		}
		if storageMgr.id == 0 {
			id, err := m.metadataGroup.RegisterStorage(storageName)
			if err != nil {
				return fmt.Errorf("registering storage ID: %w", err)
			}
			if err := storageMgr.saveStorageID(id); err != nil {
				return fmt.Errorf("saving storage ID: %w", err)
			}
		}
		m.logger.WithFields(log.Fields{"storage_name": storageName, "storage_id": storageMgr.id}).Info("storage joined the cluster")
	}

	m.logger.Info("Raft cluster has started")
	return nil
}

func (m *Manager) initMetadataGroup(storageMgr *storageManager) error {
	metadataGroup, err := newMetadataRaftGroup(
		m.ctx,
		storageMgr.nodeHost,
		storageMgr.dbForGroup(MetadataGroupID, raftID(m.clusterConfig.NodeID)),
		m.clusterConfig,
		m.logger,
	)
	if err != nil {
		return err
	}
	m.metadataGroup = metadataGroup

	return m.metadataGroup.WaitReady()
}

// Ready returns if the Raft manager is ready.
func (m *Manager) Ready() bool {
	return m.started.Load()
}

// Close closes the Raft cluster by closing all Raft objects under management.
func (m *Manager) Close() {
	if m.closed.Load() {
		return
	}
	defer m.closed.Store(true)

	for _, storageMgr := range m.storageManagers {
		storageMgr.Close()
	}
	m.logger.Info("Raft cluster has stopped")
}

// ClusterInfo returns the cluster information.
func (m *Manager) ClusterInfo() (*gitalypb.Cluster, error) {
	if !m.started.Load() {
		return nil, fmt.Errorf("raft manager has not started")
	}
	if m.closed.Load() {
		return nil, fmt.Errorf("raft manager already closed")
	}
	return m.metadataGroup.ClusterInfo()
}
