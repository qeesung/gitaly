package info

import (
	"context"
	"errors"
	"time"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/config"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/nodes"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

// Queue is a subset of the datastore.ReplicationEventQueue functionality needed by this service
type Queue interface {
	Enqueue(ctx context.Context, event datastore.ReplicationEvent) (datastore.ReplicationEvent, error)
	CountDeadReplicationJobs(ctx context.Context, from, to time.Time) (map[string]int64, error)
}

// compile time assertion that Queue is satisfied by
// datastore.ReplicationEventQueue
var _ Queue = (datastore.ReplicationEventQueue)(nil)

// Server is a InfoService server
type Server struct {
	gitalypb.UnimplementedPraefectInfoServiceServer
	nodeMgr nodes.Manager
	conf    config.Config
	queue   Queue
}

// NewServer creates a new instance of a grpc InfoServiceServer
func NewServer(nodeMgr nodes.Manager, conf config.Config, queue Queue) gitalypb.PraefectInfoServiceServer {
	s := &Server{
		nodeMgr: nodeMgr,
		conf:    conf,
		queue:   queue,
	}

	return s
}

func (s *Server) EnableWrites(ctx context.Context, req *gitalypb.EnableWritesRequest) (*gitalypb.EnableWritesResponse, error) {
	if err := s.nodeMgr.EnableWrites(ctx, req.GetVirtualStorage()); err != nil {
		if errors.Is(err, nodes.ErrVirtualStorageNotExist) {
			return nil, helper.ErrInvalidArgument(err)
		}

		return nil, helper.ErrInternal(err)
	}

	return &gitalypb.EnableWritesResponse{}, nil
}
