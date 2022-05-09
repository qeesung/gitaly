package info

import (
	"context"
	"errors"

	"gitlab.com/gitlab-org/gitaly/internal/helper"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/commonerr"
	"gitlab.com/gitlab-org/gitaly/internal/praefect/datastore"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GetRepositoryMetadata returns the cluster metadata for a repository.
func (s *Server) GetRepositoryMetadata(ctx context.Context, req *gitalypb.GetRepositoryMetadataRequest) (*gitalypb.GetRepositoryMetadataResponse, error) {
	var getMetadata func() (datastore.RepositoryMetadata, error)
	switch query := req.Query.(type) {
	case *gitalypb.GetRepositoryMetadataRequest_RepositoryId:
		getMetadata = func() (datastore.RepositoryMetadata, error) {
			return s.rs.GetRepositoryMetadata(ctx, query.RepositoryId)
		}
	case *gitalypb.GetRepositoryMetadataRequest_Path_:
		getMetadata = func() (datastore.RepositoryMetadata, error) {
			return s.rs.GetRepositoryMetadataByPath(ctx, query.Path.VirtualStorage, query.Path.RelativePath)
		}
	default:
		return nil, helper.ErrInternalf("unknown query type: %T", query)
	}

	metadata, err := getMetadata()
	if err != nil {
		if errors.Is(err, commonerr.ErrRepositoryNotFound) {
			return nil, helper.ErrNotFound(err)
		}

		return nil, helper.ErrInternalf("get metadata: %w", err)
	}

	replicas := make([]*gitalypb.GetRepositoryMetadataResponse_Replica, 0, len(metadata.Replicas))
	for _, replica := range metadata.Replicas {
		var verifiedAt *timestamppb.Timestamp
		if !replica.VerifiedAt.IsZero() {
			verifiedAt = timestamppb.New(replica.VerifiedAt)
		}

		replicas = append(replicas, &gitalypb.GetRepositoryMetadataResponse_Replica{
			Storage:      replica.Storage,
			Assigned:     replica.Assigned,
			Generation:   replica.Generation,
			Healthy:      replica.Healthy,
			ValidPrimary: replica.ValidPrimary,
			VerifiedAt:   verifiedAt,
		})
	}

	return &gitalypb.GetRepositoryMetadataResponse{
		RepositoryId:   metadata.RepositoryID,
		VirtualStorage: metadata.VirtualStorage,
		RelativePath:   metadata.RelativePath,
		ReplicaPath:    metadata.ReplicaPath,
		Primary:        metadata.Primary,
		Generation:     metadata.Generation,
		Replicas:       replicas,
	}, nil
}
