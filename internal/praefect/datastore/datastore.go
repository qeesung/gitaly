// Package datastore provides data models and datastore persistence abstractions
// for tracking the state of repository replicas.
//
// See original design discussion:
// https://gitlab.com/gitlab-org/gitaly/issues/1495
package datastore

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
)

// JobState is an enum that indicates the state of a job
type JobState string

func (js JobState) String() string {
	return string(js)
}

const (
	// JobStateReady indicates the job is now ready to proceed.
	JobStateReady = JobState("ready")
	// JobStateInProgress indicates the job is being processed by a worker.
	JobStateInProgress = JobState("in_progress")
	// JobStateCompleted indicates the job is now complete.
	JobStateCompleted = JobState("completed")
	// JobStateCancelled indicates the job was cancelled. This can occur if the
	// job is no longer relevant (e.g. a node is moved out of a repository).
	JobStateCancelled = JobState("cancelled")
	// JobStateFailed indicates the job did not succeed. The Replicator will retry
	// failed jobs.
	JobStateFailed = JobState("failed")
	// JobStateDead indicates the job was retried up to the maximum retries.
	JobStateDead = JobState("dead")
)

// ChangeType indicates what kind of change the replication is propagating
type ChangeType string

const (
	// UpdateRepo is when a replication updates a repository in place
	UpdateRepo = ChangeType("update")
	// DeleteRepo is when a replication deletes a repo
	DeleteRepo = ChangeType("delete")
	// RenameRepo is when a replication renames repo
	RenameRepo = ChangeType("rename")
	// GarbageCollect is when replication runs gc
	GarbageCollect = ChangeType("gc")
	// RepackFull is when replication runs a full repack
	RepackFull = ChangeType("repack_full")
	// RepackIncremental is when replication runs an incremental repack
	RepackIncremental = ChangeType("repack_incremental")
)

func (ct ChangeType) String() string {
	return string(ct)
}

// Params represent additional information required to process event after fetching it from storage.
// It must be JSON encodable/decodable to persist it without problems.
type Params map[string]interface{}

// Scan assigns a value from a database driver.
func (p *Params) Scan(value interface{}) error {
	if value == nil {
		return nil
	}

	d, ok := value.([]byte)
	if !ok {
		return fmt.Errorf("unexpected type received: %T", value)
	}

	return json.Unmarshal(d, p)
}

// Value returns a driver Value.
func (p Params) Value() (driver.Value, error) {
	data, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

// ReplJob is an instance of a queued replication job. A replication job is
// meant for updating the repository so that it is synced with the primary
// copy. Scheduled indicates when a replication job should be performed.
type ReplJob struct {
	Change                 ChangeType
	ID                     uint64      // autoincrement ID
	TargetNode, SourceNode models.Node // which node to replicate to?
	RelativePath           string      // source for replication
	State                  JobState
	Attempts               int
	Params                 Params // additional information required to run the job
	CorrelationID          string // from original request
}

// Datastore is a data persistence abstraction for all of Praefect's
// persistence needs
type Datastore interface {
	ReplicasDatastore
	ReplicationEventQueue
}

// ReplicasDatastore manages accessing and setting which secondary replicas
// backup a repository
type ReplicasDatastore interface {
	GetPrimary(virtualStorage string) (models.Node, error)

	GetSecondaries(virtualStorage string) ([]models.Node, error)

	GetReplicas(relativePath string) ([]models.Node, error)

	GetStorageNode(nodeStorage string) (models.Node, error)

	GetStorageNodes() ([]models.Node, error)
}

// MemoryQueue is an intermediate struct used for introduction of ReplicationEventQueue into usage.
type MemoryQueue struct {
	*MemoryDatastore
	ReplicationEventQueue
}

// MemoryDatastore is a simple datastore that isn't persisted to disk. It is
// only intended for early beta requirements and as a reference implementation
// for the eventual SQL implementation
type MemoryDatastore struct {
	// storageNodes is read-only after initialization
	// if modification needed there must be synchronization for concurrent access to it
	storageNodes map[string]models.Node

	repositories *struct {
		sync.RWMutex
		m map[string]models.Repository
	}

	// virtualStorages is read-only after initialization
	// if modification needed there must be synchronization for concurrent access to it
	virtualStorages map[string][]*models.Node
}

// NewInMemory returns an initialized in-memory datastore
func NewInMemory(cfg config.Config) *MemoryDatastore {
	m := &MemoryDatastore{
		storageNodes: map[string]models.Node{},
		repositories: &struct {
			sync.RWMutex
			m map[string]models.Repository
		}{
			m: map[string]models.Repository{},
		},
		virtualStorages: map[string][]*models.Node{},
	}

	for _, virtualStorage := range cfg.VirtualStorages {
		m.virtualStorages[virtualStorage.Name] = virtualStorage.Nodes

		for _, node := range virtualStorage.Nodes {
			// TODO: if there is two nodes with same storage name defined for different virtual storages
			// only one definition will be used: https://gitlab.com/gitlab-org/gitaly/-/issues/2613
			if _, ok := m.storageNodes[node.Storage]; ok {
				continue
			}
			m.storageNodes[node.Storage] = *node
		}
	}

	return m
}

// ErrNoPrimaryForStorage indicates a virtual storage has no primary associated with it
var ErrNoPrimaryForStorage = errors.New("no primary for storage")

// GetPrimary returns the primary configured in the config file
func (md *MemoryDatastore) GetPrimary(virtualStorage string) (models.Node, error) {
	for _, node := range md.virtualStorages[virtualStorage] {
		if node.DefaultPrimary {
			return *node, nil
		}
	}

	return models.Node{}, ErrNoPrimaryForStorage
}

// GetSecondaries gets the secondary nodes associated with a virtual storage
func (md *MemoryDatastore) GetSecondaries(virtualStorage string) ([]models.Node, error) {
	var secondaries []models.Node

	for _, node := range md.virtualStorages[virtualStorage] {
		if !node.DefaultPrimary {
			secondaries = append(secondaries, *node)
		}
	}

	return secondaries, nil
}

// GetReplicas gets the secondaries for a repository based on the relative path
func (md *MemoryDatastore) GetReplicas(relativePath string) ([]models.Node, error) {
	md.repositories.RLock()
	defer md.repositories.RUnlock()

	repository, ok := md.repositories.m[relativePath]
	if !ok {
		return nil, errors.New("repository not found")
	}

	// to prevent possible modification of element of the slice
	copied := repository.Clone()
	return copied.Replicas, nil
}

// GetStorageNode gets all storage nodes
func (md *MemoryDatastore) GetStorageNode(nodeStorage string) (models.Node, error) {
	node, ok := md.storageNodes[nodeStorage]
	if !ok {
		return models.Node{}, errors.New("node not found")
	}

	return node, nil
}

// GetStorageNodes gets all storage nodes
func (md *MemoryDatastore) GetStorageNodes() ([]models.Node, error) {
	var storageNodes []models.Node
	for _, storageNode := range md.storageNodes {
		storageNodes = append(storageNodes, storageNode)
	}

	return storageNodes, nil
}
