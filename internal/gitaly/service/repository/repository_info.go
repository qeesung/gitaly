package repository

import (
	"context"
	"fmt"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func (s *server) RepositoryInfo(
	ctx context.Context,
	request *gitalypb.RepositoryInfoRequest,
) (*gitalypb.RepositoryInfoResponse, error) {
	if err := s.locator.ValidateRepository(request.Repository); err != nil {
		return nil, structerr.NewInvalidArgument("%w", err)
	}

	repo := s.localrepo(request.Repository)

	repoPath, err := repo.Path()
	if err != nil {
		return nil, err
	}

	repoSize, err := dirSizeInBytes(repoPath)
	if err != nil {
		return nil, fmt.Errorf("calculating repository size: %w", err)
	}

	repoInfo, err := stats.RepositoryInfoForRepository(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("deriving repository info: %w", err)
	}

	return convertRepositoryInfo(uint64(repoSize), repoInfo), nil
}

func convertRepositoryInfo(repoSize uint64, repoInfo stats.RepositoryInfo) *gitalypb.RepositoryInfoResponse {
	// The loose objects size includes objects which are older than the grace period and thus
	// stale, so we need to subtract the size of stale objects from the overall size.
	recentLooseObjectsSize := repoInfo.LooseObjects.Size - repoInfo.LooseObjects.StaleSize
	// The packfiles size includes the size of cruft packs that contain unreachable objects, so
	// we need to subtract the size of cruft packs from the overall size.
	recentPackfilesSize := repoInfo.Packfiles.Size - repoInfo.Packfiles.CruftSize

	return &gitalypb.RepositoryInfoResponse{
		Size: repoSize,
		References: &gitalypb.RepositoryInfoResponse_ReferencesInfo{
			LooseCount: repoInfo.References.LooseReferencesCount,
			PackedSize: repoInfo.References.PackedReferencesSize,
		},
		Objects: &gitalypb.RepositoryInfoResponse_ObjectsInfo{
			Size:       repoInfo.LooseObjects.Size + repoInfo.Packfiles.Size,
			RecentSize: recentLooseObjectsSize + recentPackfilesSize,
			StaleSize:  repoInfo.LooseObjects.StaleSize + repoInfo.Packfiles.CruftSize,
			KeepSize:   repoInfo.Packfiles.KeepSize,
		},
	}
}
