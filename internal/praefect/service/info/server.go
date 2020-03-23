package info

import (
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// Datastore is a subset of the datastore functionality needed by this service
type Datastore interface {
	CreateReplicaReplJobs(correlationID, relativePath, primaryStorage string, secondaryStorages []string, change datastore.ChangeType, params datastore.Params) ([]uint64, error)
	UpdateReplJobState(jobID uint64, newState datastore.JobState) error
}

// compile time assertion that Datastore is satisfied by
// datastore.ReplJobsDatastore
var _ Datastore = (datastore.ReplJobsDatastore)(nil)

// Server is a InfoService server
type Server struct {
	gitalypb.UnimplementedPraefectInfoServiceServer
	nodeMgr   nodes.Manager
	conf      config.Config
	datastore Datastore
}

// NewServer creates a new instance of a grpc InfoServiceServer
func NewServer(nodeMgr nodes.Manager, conf config.Config, datastore Datastore) gitalypb.PraefectInfoServiceServer {
	s := &Server{
		nodeMgr:   nodeMgr,
		conf:      conf,
		datastore: datastore,
	}

	return s
}
