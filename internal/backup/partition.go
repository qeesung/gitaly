package backup

import (
	"context"
	"errors"
	"fmt"
	"path"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

type PartitionManager struct {
	sink           Sink
	manifestLoader ManifestLoader
	repoFactory    localrepo.Factory
}

// NewPartitionManager creates and returns a *PartitionManager instance for operating on WAL partitions.
func NewPartitionManager(sink Sink, repoFactory localrepo.Factory) *PartitionManager {
	return &PartitionManager{
		manifestLoader: NewManifestLoader(sink),
		sink:           sink,
		repoFactory:    repoFactory,
	}
}

func (m *PartitionManager) Create(ctx context.Context, req *CreateRequest) error {
	if req.Incremental {
		return errors.New("partition: incremental backups are not supported")
	}

	var (
		originalRepo *gitalypb.Repository
		lsn          storage.LSN
		partitionID  storage.PartitionID
	)

	storagectx.RunWithTransaction(ctx, func(txn storagectx.Transaction) {
		originalRepo = txn.OriginalRepository(req.Repository)
		lsn = txn.SnapshotLSN()
		partitionID = txn.PartitionID()
	})

	if originalRepo == nil {
		return errors.New("partition: backups can only be created with transactions enabled")
	}

	backup := Backup{
		ID:         req.BackupID,
		Repository: originalRepo,
		WALPartition: WALPartition{
			ID:          partitionID.String(),
			LSN:         lsn.String(),
			ArchivePath: path.Join("partitions", partitionID.String(), lsn.String()+".tar"),
		},
	}

	// if originalRepo.GetRelativePath() != req.VanityRepository.GetRelativePath() {
	// 	return fmt.Errorf("partition: mismatch %s vs %s", originalRepo.GetRelativePath(), req.VanityRepository.GetRelativePath())
	// }

	repo := m.repo(req.Repository)

	if err := m.createArchive(ctx, repo, backup.WALPartition.ArchivePath); err != nil {
		return fmt.Errorf("partition: %w", err)
	}

	if err := m.manifestLoader.WriteManifest(ctx, &backup, backup.ID); err != nil {
		return fmt.Errorf("partition: %w", err)
	}

	return nil
}

func (m *PartitionManager) createArchive(ctx context.Context, repo *localrepo.Repo, archivePath string) (returnErr error) {
	w, err := m.sink.GetWriter(ctx, archivePath)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	defer func() {
		if err := w.Close(); err != nil && returnErr == nil {
			returnErr = fmt.Errorf("create archive: %w", err)
		}
	}()

	if err := repo.CreateSnapshot(ctx, w); err != nil {
		return fmt.Errorf("create archive: %w", err)
	}

	return nil
}

func (m *PartitionManager) Restore(context.Context, *RestoreRequest) error {
	return errors.New("not implemented")
}

func (m *PartitionManager) repo(repo *gitalypb.Repository) *localrepo.Repo {
	return m.repoFactory.Build(repo)
}
