// +build !postgres

package datastore

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/models"
)

var (
	stor1 = models.Node{
		Address:        "tcp://address-1",
		Storage:        "praefect-storage-1",
		DefaultPrimary: true,
	}
	stor2 = models.Node{
		Address: "tcp://address-2",
		Storage: "praefect-storage-2",
	}
	proj1         = "abcd1234" // imagine this is a legit project hash
	correlationID = "my-correlation-id"
)

var (
	repo1Repository = models.Repository{
		RelativePath: proj1,
	}
)

var operations = []struct {
	desc string
	opFn func(*testing.T, Datastore)
}{
	{
		desc: "query an empty datastore",
		opFn: func(t *testing.T, ds Datastore) {
			jobs, err := ds.GetJobs([]JobState{JobStatePending, JobStateReady}, stor1.Storage, 1)
			require.NoError(t, err)
			require.Len(t, jobs, 0)
		},
	},
	{
		desc: "insert replication job for Update",
		opFn: func(t *testing.T, ds Datastore) {
			_, err := ds.CreateReplicaReplJobs(correlationID, repo1Repository.RelativePath, stor1.Storage, []string{stor2.Storage}, UpdateRepo, nil)
			require.NoError(t, err)
		},
	},
	{
		desc: "insert replication job for Rename",
		opFn: func(t *testing.T, ds Datastore) {
			_, err := ds.CreateReplicaReplJobs(correlationID, repo1Repository.RelativePath, stor1.Storage, []string{stor2.Storage}, RenameRepo, Params{"RelativePath": "/data/dir/repo"})
			require.NoError(t, err)
		},
	},
	{
		desc: "fetch inserted replication jobs",
		opFn: func(t *testing.T, ds Datastore) {
			jobs, err := ds.GetJobs([]JobState{JobStatePending}, stor2.Storage, 10)
			require.NoError(t, err)
			require.Len(t, jobs, 2)

			expectedJobs := []ReplJob{
				{
					Change:        UpdateRepo,
					ID:            1,
					RelativePath:  repo1Repository.RelativePath,
					SourceNode:    stor1,
					TargetNode:    stor2,
					State:         JobStatePending,
					Params:        nil,
					CorrelationID: correlationID,
				},
				{
					Change:        RenameRepo,
					ID:            2,
					RelativePath:  repo1Repository.RelativePath,
					SourceNode:    stor1,
					TargetNode:    stor2,
					State:         JobStatePending,
					Params:        Params{"RelativePath": "/data/dir/repo"},
					CorrelationID: correlationID,
				},
			}
			require.ElementsMatch(t, expectedJobs, jobs)
		},
	},
	{
		desc: "mark Update replication job as done",
		opFn: func(t *testing.T, ds Datastore) {
			err := ds.UpdateReplJobState(1, JobStateCompleted)
			require.NoError(t, err)
		},
	},
	{
		desc: "try fetching pending replication jobs",
		opFn: func(t *testing.T, ds Datastore) {
			jobs, err := ds.GetJobs([]JobState{JobStatePending}, stor2.Storage, 1)
			require.NoError(t, err)
			require.Len(t, jobs, 1)

			completed := ReplJob{
				Change:        RenameRepo,
				ID:            2,
				RelativePath:  repo1Repository.RelativePath,
				SourceNode:    stor1,
				TargetNode:    stor2,
				State:         JobStatePending,
				Params:        Params{"RelativePath": "/data/dir/repo"},
				CorrelationID: correlationID,
			}
			require.Equal(t, completed, jobs[0])
		},
	},
}

// TODO: add SQL datastore flavor
var flavors = map[string]func() Datastore{
	"in-memory-datastore": func() Datastore {
		return QueuedMemoryDatastore{
			MemoryDatastore: NewInMemory(config.Config{
				VirtualStorages: []*config.VirtualStorage{
					&config.VirtualStorage{
						Nodes: []*models.Node{&stor1, &stor2},
					},
				},
			})}
	},
}

// TestDatastoreInterface will verify that every implementation or "flavor" of
// datastore interface (in-Memory or SQL) behaves consistently given the same
// series of operations
func TestDatastoreInterface(t *testing.T) {
	for name, dsFactory := range flavors {
		t.Run(name, func(t *testing.T) {
			ds := dsFactory()
			for i, op := range operations {
				t.Logf("operation %d: %s", i+1, op.desc)
				op.opFn(t, ds)
			}
		})
	}
}
