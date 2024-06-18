package raft

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lni/dragonboat/v4"
	dragonboatConfig "github.com/lni/dragonboat/v4/config"
	"github.com/lni/dragonboat/v4/statemachine"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
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

type testNode struct {
	nodeHost *dragonboat.NodeHost
	sm       *testStateMachine
	close    func()
}

type testRaftCluster struct {
	sync.Mutex
	clusterID      string
	initialMembers map[uint64]string
	nodes          map[raftID]*testNode
}

func (c *testRaftCluster) startNode(t *testing.T, node raftID) bool {
	tmpDir := testhelper.TempDir(t)
	// Reduce the capacity of log DB and engines. We don't need that capacity in tests. They do cost
	// memory and cpu resources to maintain.
	logDBConf := dragonboatConfig.GetTinyMemLogDBConfig()
	logDBConf.Shards = 4
	engineConf := dragonboatConfig.EngineConfig{
		ExecShards:     4,
		CommitShards:   4,
		ApplyShards:    4,
		SnapshotShards: 4,
		CloseShards:    4,
	}
	nodeHostConfig := dragonboatConfig.NodeHostConfig{
		WALDir:         tmpDir,
		NodeHostDir:    tmpDir,
		RaftAddress:    c.initialMembers[node.ToUint64()],
		RTTMillisecond: config.RaftDefaultRTT,
		Expert: dragonboatConfig.ExpertConfig{
			LogDB:  logDBConf,
			Engine: engineConf,
		},
	}
	nodeHost, err := dragonboat.NewNodeHost(nodeHostConfig)
	if err != nil {
		t.Log(err)
		if strings.Contains(err.Error(), "address already in use") {
			return true
		}
		require.NoError(t, err)
	}

	require.NoError(t, err)

	c.Lock()
	c.nodes[node] = &testNode{
		nodeHost: nodeHost,
		close:    nodeHost.Close,
	}
	c.Unlock()

	return false
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
	raftConfig := dragonboatConfig.Config{
		ReplicaID:    node.ToUint64(),
		ShardID:      groupID.ToUint64(),
		ElectionRTT:  config.RaftDefaultElectionTicks,
		HeartbeatRTT: config.RaftDefaultHeartbeatTicks,
		WaitReady:    true,
	}
	require.NoError(t, c.nodes[node].nodeHost.StartOnDiskReplica(c.initialMembers, false, func(shardID, replicaID uint64) statemachine.IOnDiskStateMachine {
		c.nodes[node].sm = &testStateMachine{
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
	c.waitUntilReady(t, node, groupID)
}

func (c *testRaftCluster) stopGroup(t *testing.T, node raftID, groupID raftID) {
	require.NoError(t, c.nodes[node].nodeHost.StopShard(groupID.ToUint64()))
}

func (c *testRaftCluster) waitUntilReady(t *testing.T, node raftID, groupID raftID) {
	c.Lock()
	clusterNode := c.nodes[node]
	c.Unlock()
	ctx := testhelper.Context(t)
	for {
		_, err := func(ctx context.Context) (any, error) {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			return clusterNode.nodeHost.SyncGetShardMembership(ctx, groupID.ToUint64())
		}(ctx)

		if err == nil {
			break
		} else if !dragonboat.IsTempError(err) {
			require.Error(t, err)
		}
		select {
		case <-ctx.Done():
			t.Error("timeout waiting for Raft cluster's readiness")
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (c *testRaftCluster) toRaftConfig(node raftID) config.Raft {
	initialMembers := map[string]string{}
	for node, addr := range c.initialMembers {
		initialMembers[fmt.Sprintf("%d", node)] = addr
	}
	return config.Raft{
		Enabled:         true,
		ClusterID:       c.clusterID,
		NodeID:          node.ToUint64(),
		RaftAddr:        c.nodes[node].nodeHost.RaftAddress(),
		InitialMembers:  initialMembers,
		RTTMilliseconds: config.RaftDefaultRTT,
		ElectionTicks:   config.RaftDefaultElectionTicks,
		HeartbeatTicks:  config.RaftDefaultHeartbeatTicks,
	}
}

func newTestRaftCluster(t *testing.T, numNodes int) *testRaftCluster {
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
				conflict := cluster.startNode(t, raftID(i))
				if conflict {
					retry.Store(true)
				}
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
