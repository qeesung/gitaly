package raft

import (
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/lni/dragonboat/v4"
	dragonboatConfig "github.com/lni/dragonboat/v4/config"
	"github.com/lni/dragonboat/v4/statemachine"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/keyvalue"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"google.golang.org/protobuf/proto"
)

type testStateMachine struct {
	sync.Mutex
	t       *testing.T
	index   raftID
	shardID raftID
	replica raftID
	entries []proto.Message

	updater func(proto.Message) (uint64, proto.Message, error)
	reader  func(proto.Message) (proto.Message, error)
}

func (m *testStateMachine) Open(stopc <-chan struct{}) (uint64, error) {
	return 0, nil
}

func (m *testStateMachine) Update(entries []statemachine.Entry) ([]statemachine.Entry, error) {
	var returnedEntries []statemachine.Entry
	for _, entry := range entries {
		req, err := anyProtoUnmarshal(entry.Cmd)
		require.NoError(m.t, err)

		result, resp, err := m.updater(req)
		if err != nil {
			return nil, err
		}
		data, err := anyProtoMarshal(resp)
		require.NoError(m.t, err)

		returnedEntries = append(returnedEntries, statemachine.Entry{
			Index: entry.Index,
			Result: statemachine.Result{
				Value: result,
				Data:  data,
			},
		})
		m.index = raftID(entry.Index)
		m.Lock()
		m.entries = append(m.entries, req)
		m.Unlock()
	}
	return returnedEntries, nil
}

func (m *testStateMachine) Lookup(req interface{}) (interface{}, error) {
	return m.reader(req.(proto.Message))
}

func (m *testStateMachine) Sync() error { return nil }
func (m *testStateMachine) PrepareSnapshot() (interface{}, error) {
	m.t.Error("testStateMachine does not support snapshotting")
	return nil, nil
}

func (m *testStateMachine) SaveSnapshot(interface{}, io.Writer, <-chan struct{}) error {
	m.t.Error("testStateMachine does not support snapshotting")
	return nil
}

func (m *testStateMachine) RecoverFromSnapshot(io.Reader, <-chan struct{}) error {
	m.t.Error("testStateMachine does not support snapshotting")
	return nil
}

func (m *testStateMachine) Close() error {
	return nil
}

func (m *testStateMachine) getEntries() []proto.Message {
	m.Lock()
	defer m.Unlock()
	return m.entries
}

type (
	mockReaderFunc  func(proto.Message) (proto.Message, error)
	mockUpdaterFunc func(proto.Message) (uint64, proto.Message, error)
)

// dragonboatTestingProfile defines an engine profile that is friendly to testing environments.
var dragonboatTestingProfile = func() dragonboatConfig.ExpertConfig {
	// Reduce the capacity of log DB and engines. We don't need that capacity in tests. They do cost
	// memory and cpu resources to maintain.
	logDBConf := dragonboatConfig.GetTinyMemLogDBConfig()
	logDBConf.Shards = 2
	engineConf := dragonboatConfig.EngineConfig{
		ExecShards:     2,
		CommitShards:   2,
		ApplyShards:    2,
		SnapshotShards: 2,
		CloseShards:    2,
	}
	return dragonboatConfig.ExpertConfig{
		LogDB:  logDBConf,
		Engine: engineConf,
	}
}()

type testNode struct {
	nodeHost *dragonboat.NodeHost
	manager  *Manager
	sm       *testStateMachine
	close    func()
}

type testRaftCluster struct {
	sync.Mutex
	clusterID      string
	initialMembers map[uint64]string
	nodes          map[raftID]*testNode
}

func (c *testRaftCluster) startNode(t *testing.T, node raftID) (*testNode, error) {
	tmpDir := testhelper.TempDir(t)
	nodeHostConfig := dragonboatConfig.NodeHostConfig{
		WALDir:         tmpDir,
		NodeHostDir:    tmpDir,
		RaftAddress:    c.initialMembers[node.ToUint64()],
		RTTMillisecond: config.RaftDefaultRTT,
		Expert:         dragonboatTestingProfile,
	}
	nodeHost, err := dragonboat.NewNodeHost(nodeHostConfig)
	if err != nil {
		return nil, err
	}

	require.NoError(t, err)

	return &testNode{
		nodeHost: nodeHost,
		close:    nodeHost.Close,
	}, nil
}

func (c *testRaftCluster) closeAll() {
	for _, node := range c.nodes {
		node.close()
	}
}

func (c *testRaftCluster) closeNode(node raftID) {
	c.nodes[node].close()
}

func (c *testRaftCluster) startTestGroups(t *testing.T, nodes []raftID, groupIDs []raftID, updaters []mockUpdaterFunc, readers []mockReaderFunc) {
	var wg sync.WaitGroup
	for i := 0; i < len(nodes); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			c.startTestGroup(t, nodes[i], groupIDs[i], updaters[i], readers[i])
		}(i)
	}
	wg.Wait()
}

func (c *testRaftCluster) startTestGroup(t *testing.T, node raftID, groupID raftID, updater mockUpdaterFunc, reader mockReaderFunc) {
	c.Lock()
	clusterNode := c.nodes[node]
	raftConfig := dragonboatConfig.Config{
		ReplicaID:    node.ToUint64(),
		ShardID:      groupID.ToUint64(),
		ElectionRTT:  config.RaftDefaultElectionTicks,
		HeartbeatRTT: config.RaftDefaultHeartbeatTicks,
		WaitReady:    true,
	}
	require.NoError(t, c.nodes[node].nodeHost.StartOnDiskReplica(c.initialMembers, false, func(shardID, replicaID uint64) statemachine.IOnDiskStateMachine {
		clusterNode.sm = &testStateMachine{
			t:       t,
			shardID: raftID(shardID),
			replica: node,
			updater: updater,
			reader:  reader,
		}
		return c.nodes[node].sm
	}, raftConfig))
	c.Unlock()

	// Poll until the Raft group is ready within 30-second timeout.
	require.NoError(t, WaitGroupReady(testhelper.Context(t), c.nodes[node].nodeHost, groupID))
}

func (c *testRaftCluster) stopGroup(t *testing.T, node raftID, groupID raftID) {
	require.NoError(t, c.nodes[node].nodeHost.StopShard(groupID.ToUint64()))
}

func (c *testRaftCluster) createRaftConfig(node raftID) config.Raft {
	initialMembers := map[string]string{}
	for node, addr := range c.initialMembers {
		initialMembers[fmt.Sprintf("%d", node)] = addr
	}
	return config.Raft{
		Enabled:         true,
		ClusterID:       c.clusterID,
		NodeID:          node.ToUint64(),
		RaftAddr:        c.initialMembers[node.ToUint64()],
		InitialMembers:  initialMembers,
		RTTMilliseconds: config.RaftDefaultRTT,
		ElectionTicks:   config.RaftDefaultElectionTicks,
		HeartbeatTicks:  config.RaftDefaultHeartbeatTicks,
	}
}

type testRaftClusterConfig struct {
	startNode nodeStarter
}

type testRaftClusterOption func(*testRaftClusterConfig)

// nodeStarter should start the node with raftID in the cluster, and return its details or an error.
type nodeStarter func(*testRaftCluster, raftID) (*testNode, error)

func withNodeStarter(startNode nodeStarter) testRaftClusterOption {
	return func(cfg *testRaftClusterConfig) {
		cfg.startNode = startNode
	}
}

// newTestRaftCluster creates a Raft cluster having N nodes. Each node in the cluster is powered by
// either a test Raft group or a proper Raft manager.
func newTestRaftCluster(t *testing.T, numNodes int, options ...testRaftClusterOption) *testRaftCluster {
	testhelper.SkipWithPraefect(t, `Raft is not compatible with Praefect.`)

	cfg := &testRaftClusterConfig{
		startNode: func(cluster *testRaftCluster, node raftID) (*testNode, error) {
			return cluster.startNode(t, node)
		},
	}
	for _, option := range options {
		option(cfg)
	}

	id, err := uuid.NewUUID()
	require.NoError(t, err)

	cluster := &testRaftCluster{
		clusterID:      id.String(),
		nodes:          map[raftID]*testNode{},
		initialMembers: map[uint64]string{},
	}
	attempt := 10
	for {
		addrs := reserveEphemeralAddrs(t, numNodes)
		for i := 0; i < numNodes; i++ {
			cluster.initialMembers[uint64(i+1)] = addrs[i]
		}

		var retry atomic.Bool

		var wg sync.WaitGroup
		for i := range cluster.initialMembers {
			wg.Add(1)
			go func(i uint64) {
				defer wg.Done()
				node, err := cfg.startNode(cluster, raftID(i))
				if err != nil {
					t.Log(err)
					if strings.Contains(err.Error(), "address already in use") {
						retry.Store(true)
					} else {
						require.NoError(t, err)
					}
					return
				}

				cluster.Lock()
				cluster.nodes[raftID(i)] = node
				cluster.Unlock()
			}(i)
		}
		wg.Wait()

		// Conflict port. Re-spawn the whole cluster with a different set of ephemeral ports.
		if !retry.Load() {
			break
		}
		t.Logf("re-spawning Raft cluster as addresses in use: %+v", cluster.initialMembers)
		cluster.closeAll()
		attempt--
		if attempt <= 0 {
			t.Error("unable to allocate ephemeral ports for Raft cluster")
		}
	}

	return cluster
}

// reserveEphemeralAddrs returns ephemeral addresses used for starting Raft cluster. There is no
// elegant way to find an ephemeral port from the OS. This function creates a listener binding to a
// :0 port then extracts the actual port number. However, there is a slight chance that port is
// occupied right afterward. The caller should retry when encounter such situation.
func reserveEphemeralAddrs(t *testing.T, count int) []string {
	var addrs []string
	for i := 0; i < count; i++ {
		listener, err := net.Listen("tcp", ":0")
		require.NoError(t, err)

		addr := fmt.Sprintf("localhost:%d", listener.Addr().(*net.TCPAddr).Port)
		defer func() { require.NoError(t, listener.Close()) }()

		addrs = append(addrs, addr)
	}
	return addrs
}

func setupTestDB(t *testing.T, cfg config.Cfg) *keyvalue.DBManager {
	logger := testhelper.NewLogger(t)

	dbMgr, err := keyvalue.NewDBManager(cfg.Storages, keyvalue.NewBadgerStore, helper.NewNullTickerFactory(), logger)
	require.NoError(t, err)

	return dbMgr
}

func setupStorageDB(t *testing.T, cfg config.Cfg) (keyvalue.Transactioner, func()) {
	dbMgr := setupTestDB(t, cfg)

	storage := cfg.Storages[0]
	db, err := dbMgr.GetDB(storage.Name)
	require.NoError(t, err)

	return dbForStorage(db), dbMgr.Close
}

// fanOut executes f concurrently num times. The supplied raftID begins from 1.
func fanOut(num int, f func(raftID)) {
	var wg sync.WaitGroup
	for i := 1; i <= num; i++ {
		wg.Add(1)
		go func(i raftID) {
			defer wg.Done()

			f(i)
		}(raftID(i))
	}
	wg.Wait()
}

// fanOutNodes executes f concurrently for each node in the cluster.
func fanOutNodes(cluster *testRaftCluster, f func(node *testNode)) {
	var wg sync.WaitGroup
	for _, node := range cluster.nodes {
		wg.Add(1)
		go func(node *testNode) {
			defer wg.Done()

			f(node)
		}(node)
	}
	wg.Wait()
}

func TestMain(m *testing.M) {
	// It's unfortunate that dragonboat's logger is global. It allows to configure the logger once.
	// Thus, we create one logger here to capture all system logs. The logs are dumped out once if
	// any of the test fails. It's not ideal, but better than no logs.
	logger, buffer := testhelper.NewCapturedLogger()
	SetLogger(logger, false)
	testhelper.Run(m, testhelper.WithTeardown(func(code int) error {
		if code != 0 {
			fmt.Printf("Recorded Raft's system logs from all the tests:\n%s\n", buffer)
		}
		return nil
	}))
}
