package raft

import (
	"context"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"github.com/lni/dragonboat/v4"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"

	dragonboatConf "github.com/lni/dragonboat/v4/config"
)

const (
	MetadataGroupShardID = 1
	DefaultRTT           = 200
	DefaultElectionRTT   = 20
	DefaultHeartbeatRTT  = 2
)

type ManagerConfig struct {
	BootstrapCluster bool
}

type Manager struct {
	ctx      context.Context
	storages []config.Storage
	raft     config.Raft
	config   ManagerConfig
	logger   log.Logger

	initialMembers map[uint64]string
	firstStorage   string
	nodeHosts      map[string]*dragonboat.NodeHost
}

func walDir(storageConfig config.Storage) string {
	return filepath.Join(storageConfig.Path, config.GitalyDataPrefix, "raft")
}

func nodeHostDir(storageConfig config.Storage) string {
	return filepath.Join(storageConfig.Path, config.GitalyDataPrefix, "raft")
}

func parseInitialMembers(input map[string]string) (map[uint64]string, error) {
	initialMembers := make(map[uint64]string)
	for nodeIDText, address := range input {
		nodeID, err := strconv.ParseUint(string(nodeIDText), 10, 64)
		if err != nil {
			return initialMembers, fmt.Errorf("converting node ID to uint64: %w", err)
		}
		initialMembers[nodeID] = address
	}
	return initialMembers, nil
}

func fulfillRaftConfig(raftConfig config.Raft) config.Raft {
	if raftConfig.RTTMillisecond == 0 {
		raftConfig.RTTMillisecond = DefaultRTT
	}
	if raftConfig.ElectionRTT == 0 {
		raftConfig.ElectionRTT = DefaultElectionRTT
	}
	if raftConfig.HeartbeatRTT == 0 {
		raftConfig.HeartbeatRTT = DefaultHeartbeatRTT
	}
	return raftConfig
}

func NewManager(
	ctx context.Context,
	storages []config.Storage,
	raft config.Raft,
	logger log.Logger,
	mgrConf ManagerConfig,
) (*Manager, error) {
	SetLogger(logger)
	raft = fulfillRaftConfig(raft)

	slices.SortFunc(storages, func(a, b config.Storage) int {
		if a.Name < b.Name {
			return -1
		} else if a.Name > b.Name {
			return 1
		}
		return 0
	})

	initialMembers, err := parseInitialMembers(raft.InitialMembers)
	if err != nil {
		return nil, fmt.Errorf("parsing initial members: %w", err)
	}

	return &Manager{
		ctx:            ctx,
		storages:       storages,
		raft:           raft,
		config:         mgrConf,
		initialMembers: initialMembers,
		nodeHosts:      map[string]*dragonboat.NodeHost{},
		logger: logger.WithFields(log.Fields{
			"component":       "raft",
			"raft_component":  "manager",
			"raft_cluster_id": raft.ClusterID,
			"raft_node_id":    raft.NodeID,
		}),
	}, nil
}

func (m *Manager) Start() error {
	m.logger.WithFields(log.Fields{
		"raft_storages":     m.storages,
		"raft_config":       m.raft,
		"raft_manager_conf": m.config,
	}).Info("starting Raft manager")

	for _, storage := range m.storages {
		nodeHost, err := dragonboat.NewNodeHost(dragonboatConf.NodeHostConfig{
			WALDir:                     walDir(storage),
			NodeHostDir:                nodeHostDir(storage),
			RTTMillisecond:             m.raft.RTTMillisecond,
			RaftAddress:                fmt.Sprintf("localhost:400%d", m.raft.NodeID),
			ListenAddress:              fmt.Sprintf("localhost:400%d", m.raft.NodeID),
			DefaultNodeRegistryEnabled: false,
			EnableMetrics:              true,
		})
		if err != nil {
			return fmt.Errorf("creating dragonboat nodehost: %w", err)
		}
		m.nodeHosts[storage.Name] = nodeHost
		if m.firstStorage == "" {
			m.firstStorage = storage.Name
		}
	}

	// All data in Gitaly are stored in storages independently. A storage might be detached and
	// moved to another node freely. A node might have more than one storage. We create one nodehost
	// instance per storage. However, each nodes should only host one Raft group per node. So, the
	// first storage, sorted by name, is responsible.
	nodeHost := m.nodeHosts[m.firstStorage]

	// As in the scope of https://gitlab.com/groups/gitlab-org/-/epics/13562, we aim to make Gitaly
	// Raft cluster works in a 3-node setting. We haven't supported node joining the cluster, yet.
	// As a result, initial members are also the authority of Metadata Raft group, which is a
	// special group that stores and manages cluster-wise information.
	if err := m.initMetadataGroup(nodeHost); err != nil {
		return fmt.Errorf("initializing Raft metadata group: %w", err)
	}
	if m.config.BootstrapCluster {
		if err := m.bootstrapIfNeeded(nodeHost); err != nil {
			return fmt.Errorf("bootstrapping Raft cluster: %w", err)
		}
	}

	m.logger.Info("Raft manager has started")
	return nil
}

func (m *Manager) initMetadataGroup(nodeHost *dragonboat.NodeHost) error {
	raftConfig := dragonboatConf.Config{
		ReplicaID:    m.raft.NodeID,
		ShardID:      MetadataGroupShardID,
		ElectionRTT:  m.raft.ElectionRTT,
		HeartbeatRTT: m.raft.HeartbeatRTT,
		CheckQuorum:  true,
		Quiesce:      true,
	}
	if err := nodeHost.StartOnDiskReplica(m.initialMembers, false, NewMetadataStatemachine, raftConfig); err != nil {
		return fmt.Errorf("starting metadata group: %w", err)
	}
	return nil
}

func (m *Manager) bootstrapIfNeeded(nodeHost *dragonboat.NodeHost) error {
	m.logger.Info("bootstrapping Raft cluster")

	for {
		bootstrapped, err := m.tryBootstrap(nodeHost)
		if err != nil {
			return err
		}
		if bootstrapped {
			return nil
		}

		select {
		case <-m.ctx.Done():
			return fmt.Errorf("waiting to bootstrap cluster: %w", m.ctx.Err())
		case <-time.After(m.estimatedNextElection()):
			continue
		}
	}
}

func (m *Manager) tryBootstrap(nodeHost *dragonboat.NodeHost) (bool, error) {
	ctx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	var count uint64
	result, err := m.getFirstNodeHost().SyncRead(ctx, MetadataGroupShardID, []byte{})
	if err == nil {
		count = binary.LittleEndian.Uint64(result.([]byte))
		if count != 0 {
			m.logger.Info("Raft cluster bootstrapped")
			return true, nil
		}

		leaderID, _, valid, err := nodeHost.GetLeaderID(MetadataGroupShardID)
		if err != nil {
			if dragonboat.IsTempError(err) {
				return false, nil
			}
			return false, err
		}

		if !valid || leaderID != m.raft.NodeID {
			m.logger.Warn("Raft cluster has not been bootstrapped, waiting for leader to bootstrap the cluster")
			return false, nil
		}
	}

	session, err := nodeHost.SyncGetSession(ctx, MetadataGroupShardID)
	if err != nil {
		if dragonboat.IsTempError(err) {
			return false, nil
		}
		return false, fmt.Errorf("getting bootstrapping session: %w", err)
	}
	defer nodeHost.SyncCloseSession(ctx, session)

	_, err = nodeHost.SyncPropose(ctx, session, []byte{})
	if err != nil {
		if dragonboat.IsTempError(err) {
			return false, nil
		}
		return false, fmt.Errorf("bootstrapping Raft cluster: %w", err)
	}

	m.logger.Info("Raft cluster has been bootstrapped successfully")
	return true, nil
}

func (m *Manager) Close() {
	m.logger.Info("closing Raft cluster")
	for _, nodeHost := range m.nodeHosts {
		nodeHost.Close()
	}
	m.logger.Info("Raft cluster has stopped")
}

func (m *Manager) getFirstNodeHost() *dragonboat.NodeHost {
	return m.nodeHosts[m.firstStorage]
}

func (m *Manager) estimatedNextElection() time.Duration {
	return time.Millisecond * time.Duration(m.raft.RTTMillisecond*m.raft.ElectionRTT)
}
