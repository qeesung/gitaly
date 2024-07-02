package raft

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/lni/dragonboat/v4"
	dragonboatConf "github.com/lni/dragonboat/v4/config"
	"github.com/lni/dragonboat/v4/statemachine"
	"gitlab.com/gitlab-org/gitaly/v16/internal/backoff"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type metadataRaftGroup struct {
	Group
	backoffProfile *backoff.Exponential
}

const defaultRetry = 3

func parseInitialMembers(input map[string]string) (map[uint64]string, error) {
	initialMembers := make(map[uint64]string)
	for nodeIDText, address := range input {
		nodeID, err := strconv.ParseUint(nodeIDText, 10, 64)
		if err != nil {
			return initialMembers, fmt.Errorf("converting node ID to uint64: %w", err)
		}
		initialMembers[nodeID] = address
	}
	return initialMembers, nil
}

func newMetadataRaftGroup(ctx context.Context, nodeHost *dragonboat.NodeHost, accessDB dbAccessor, clusterCfg config.Raft, logger log.Logger) (*metadataRaftGroup, error) {
	initialMembers, err := parseInitialMembers(clusterCfg.InitialMembers)
	if err != nil {
		return nil, fmt.Errorf("parsing initial members: %w", err)
	}

	groupCfg := dragonboatConf.Config{
		ReplicaID:    clusterCfg.NodeID,
		ShardID:      MetadataGroupID.ToUint64(),
		ElectionRTT:  clusterCfg.ElectionTicks,
		HeartbeatRTT: clusterCfg.HeartbeatTicks,
		CheckQuorum:  true,
		WaitReady:    true,
		Quiesce:      false,
	}

	var metadataSM Statemachine
	if err := nodeHost.StartOnDiskReplica(initialMembers, false, func(groupID, replicaID uint64) statemachine.IOnDiskStateMachine {
		return newMetadataStatemachine(ctx, raftID(groupID), raftID(replicaID), accessDB)
	}, groupCfg); err != nil {
		return nil, fmt.Errorf("starting metadata group: %w", err)
	}

	groupLogger := logger.WithFields(log.Fields{
		"raft_group":      "metadata",
		"raft_group_id":   MetadataGroupID,
		"raft_replica_id": clusterCfg.NodeID,
	})
	groupLogger.Info("joined metadata group")

	backoffProfile := backoff.NewDefaultExponential(rand.New(rand.NewSource(time.Now().UnixNano())))
	backoffProfile.BaseDelay = time.Duration(clusterCfg.ElectionTicks) * time.Duration(clusterCfg.RTTMilliseconds) * time.Microsecond

	return &metadataRaftGroup{
		Group: Group{
			ctx:           ctx,
			groupID:       MetadataGroupID,
			replicaID:     raftID(clusterCfg.NodeID),
			clusterConfig: clusterCfg,
			groupConfig:   groupCfg,
			logger:        groupLogger,
			nodeHost:      nodeHost,
			statemachine:  metadataSM,
		},
		backoffProfile: backoffProfile,
	}, nil
}

// BootstrapIfNeeded will bootstrap the metadata group's cluster if not already bootstrapped. The
// function waits until a quorum of cluster members is reached or the context is cancelled.
// The bootstrapping process persists the configured cluster information, including the cluster ID
// and initial storage ID, to the statemachine of each member. After bootstrapping, the cluster is
// functional and is ready to accept requests.
func (g *metadataRaftGroup) BootstrapIfNeeded() (*gitalypb.Cluster, error) {
	g.logger.Info("bootstrapping Raft cluster")
	for {
		cluster, err := g.tryBootstrap()
		if err != nil {
			return nil, err
		}
		if cluster != nil {
			return cluster, nil
		}

		select {
		case <-g.ctx.Done():
			return nil, fmt.Errorf("waiting to bootstrap cluster: %w", g.ctx.Err())
		case <-time.After(g.maxHeartbeatWait()):
		}
	}
}

func (g *metadataRaftGroup) tryBootstrap() (*gitalypb.Cluster, error) {
	// Fetch cluster info from metadata group.
	getResponse, err := g.requestGetCluster(0)
	if err != nil {
		if errors.Is(err, ErrTemporaryFailure) {
			g.logger.WithError(err).Info("cluster metadata group is not ready, wait for other nodes to reach quorum.")
			return nil, nil
		}
		return nil, fmt.Errorf("fetching cluster info: %w", err)
	}

	// If cluster info exists, it means the cluster is bootstrapped.
	if getResponse != nil && getResponse.GetCluster() != nil {
		return getResponse.GetCluster(), nil
	}

	// Only the leader is responsible for bootstrapping the cluster. Technically, we don't need this
	// check. If there are multiple bootstrapping request, the statemachine accepts the first one.
	// No need to send duplicated requests just to fail later.
	leaderState, err := g.getLeaderState()
	if err != nil {
		return nil, fmt.Errorf("getting leader state of metadata group: %w", err)
	}
	if !leaderState.Valid || leaderState.LeaderId != g.clusterConfig.NodeID {
		g.logger.Info("not a leader, waiting for the leader to bootstrap cluster")
		return nil, nil
	}

	// Now, the current node assumes its the leader. It should send the bootstrap request.
	result, bootstrapResponse, err := g.requestBootstrapCluster()
	if err != nil {
		return nil, fmt.Errorf("bootstrapping cluster: %w", err)
	}

	switch result {
	case resultClusterBootstrapSuccessful:
		return bootstrapResponse.GetCluster(), nil
	case resultClusterAlreadyBootstrapped:
		// Somehow, the cluster is bootstrapped by other node. We'll need to let this node re-fetch again.
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown update result: %d", result)
	}
}

// RegisterStorage requests the metadata group to allocate a unique ID for a new storage. The caller
// is expected to persist the newly allocated ID. This ID is used for future interactions with the
// Raft cluster. The storage name must be unique cluster-wide.
func (g *metadataRaftGroup) RegisterStorage(storageName string) (raftID, error) {
	storageName = strings.TrimSpace(storageName)
	cluster, err := g.ClusterInfo()
	if err != nil {
		return 0, err
	}
	for _, storage := range cluster.Storages {
		if storage.GetName() == storageName {
			return 0, fmt.Errorf("storage %q already registered", storageName)
		}
	}
	result, response, err := g.requestRegisterStorage(storageName)
	if err != nil {
		return 0, fmt.Errorf("registering storage: %w", err)
	}

	switch result {
	case resultRegisterStorageSuccessful:
		return raftID(response.GetStorage().GetStorageId()), nil
	case resultStorageAlreadyRegistered:
		// There's a chance that storage is registered by another node while firing this request. We
		// have no choice but reject this request.
		return 0, fmt.Errorf("storage %q already registered", storageName)
	case resultRegisterStorageClusterNotBootstrappedYet:
		// Extremely rare occasion. This case occurs when the cluster information is wiped out of
		// the metadata group when the register storage request is in-flight.
		return 0, fmt.Errorf("cluster has not been bootstrapped")
	default:
		return 0, fmt.Errorf("unsupported update result: %d", result)
	}
}

// ClusterInfo fetches the latest cluster info from the metadata group.
func (g *metadataRaftGroup) ClusterInfo() (*gitalypb.Cluster, error) {
	response, err := g.requestGetCluster(defaultRetry)
	if err != nil {
		return nil, fmt.Errorf("fetching cluster info: %w", err)
	}
	if response == nil || response.GetCluster() == nil {
		return nil, fmt.Errorf("cluster has not been bootstrapped")
	}
	return response.GetCluster(), nil
}

// WaitReady waits until the metadata Raft group ready.
func (g *metadataRaftGroup) WaitReady() error {
	return WaitGroupReady(g.ctx, g.nodeHost, g.groupID)
}

func (g *metadataRaftGroup) maxHeartbeatWait() time.Duration {
	return time.Millisecond * time.Duration(g.groupConfig.HeartbeatRTT*g.nodeHost.NodeHostConfig().RTTMillisecond)
}

func (g *metadataRaftGroup) maxNextElectionWait() time.Duration {
	return time.Millisecond * time.Duration(g.groupConfig.ElectionRTT*g.nodeHost.NodeHostConfig().RTTMillisecond)
}

func (g *metadataRaftGroup) requestGetCluster(retry int) (*gitalypb.GetClusterResponse, error) {
	requester := NewRequester[*gitalypb.GetClusterRequest, *gitalypb.GetClusterResponse](
		g.nodeHost, g.groupID, g.logger, requestOption{
			retry:       retry,
			timeout:     g.maxNextElectionWait(),
			exponential: g.backoffProfile,
		},
	)
	return requester.SyncRead(g.ctx, &gitalypb.GetClusterRequest{})
}

func (g *metadataRaftGroup) requestBootstrapCluster() (updateResult, *gitalypb.BootstrapClusterResponse, error) {
	requester := NewRequester[*gitalypb.BootstrapClusterRequest, *gitalypb.BootstrapClusterResponse](
		g.nodeHost, g.groupID, g.logger, requestOption{
			retry:       defaultRetry,
			timeout:     g.maxNextElectionWait(),
			exponential: g.backoffProfile,
		},
	)
	return requester.SyncWrite(g.ctx, &gitalypb.BootstrapClusterRequest{ClusterId: g.clusterConfig.ClusterID})
}

func (g *metadataRaftGroup) requestRegisterStorage(storageName string) (updateResult, *gitalypb.RegisterStorageResponse, error) {
	requester := NewRequester[*gitalypb.RegisterStorageRequest, *gitalypb.RegisterStorageResponse](
		g.nodeHost, g.groupID, g.logger, requestOption{
			retry:       defaultRetry,
			timeout:     g.maxNextElectionWait(),
			exponential: g.backoffProfile,
		},
	)
	return requester.SyncWrite(g.ctx, &gitalypb.RegisterStorageRequest{StorageName: storageName})
}

func (g *metadataRaftGroup) getLeaderState() (*gitalypb.LeaderState, error) {
	return GetLeaderState(g.nodeHost, g.groupID)
}
