package objectpool

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
)

// Link will link the given repository to the object pool. This is done by writing the object pool's
// path relative to the repository into the repository's "alternates" file. This does not trigger
// deduplication, which is the responsibility of the caller.
func (o *ObjectPool) Link(ctx context.Context, repo *localrepo.Repo) (returnedErr error) {
	altPath, err := repo.InfoAlternatesPath(ctx)
	if err != nil {
		return err
	}

	expectedRelPath, err := o.getRelativeObjectPath(ctx, repo)
	if err != nil {
		return err
	}

	linked, err := o.LinkedToRepository(ctx, repo)
	if err != nil {
		return err
	}

	if linked {
		// When the repository is already linked to the repository, cast a vote to ensure the
		// repository is consistent with the other replicas.
		if err := transaction.VoteOnContext(ctx, o.txManager, voting.VoteFromData([]byte("repository linked")), voting.Committed); err != nil {
			return fmt.Errorf("vote on linked repository: %w", err)
		}

		return nil
	}

	alternatesWriter, err := safe.NewLockingFileWriter(altPath)
	if err != nil {
		return fmt.Errorf("creating alternates writer: %w", err)
	}
	defer func() {
		if err := alternatesWriter.Close(); err != nil && returnedErr == nil {
			returnedErr = fmt.Errorf("closing alternates writer: %w", err)
		}
	}()

	if _, err := io.WriteString(alternatesWriter, expectedRelPath); err != nil {
		return fmt.Errorf("writing alternates: %w", err)
	}

	if err := transaction.CommitLockedFile(ctx, o.txManager, alternatesWriter); err != nil {
		return fmt.Errorf("committing alternates: %w", err)
	}

	storagectx.RunWithTransaction(ctx, func(tx storagectx.Transaction) {
		tx.MarkAlternateUpdated()
	})

	return o.removeMemberBitmaps(ctx, repo)
}

// removeMemberBitmaps removes packfile bitmaps from the member
// repository that just joined the pool. If Git finds two packfiles with
// bitmaps it will print a warning, which is visible to the end user
// during a Git clone. Our goal is to avoid that warning. In normal
// operation, the next 'git gc' or 'git repack -ad' on the member
// repository will remove its local bitmap file. In other words the
// situation we eventually converge to is that the pool may have a bitmap
// but none of its members will. With removeMemberBitmaps we try to
// change "eventually" to "immediately", so that users won't see the
// warning. https://gitlab.com/gitlab-org/gitaly/issues/1728
func (o *ObjectPool) removeMemberBitmaps(ctx context.Context, repo *localrepo.Repo) error {
	poolPath, err := o.Repo.Path(ctx)
	if err != nil {
		return err
	}

	poolBitmaps, err := getBitmaps(poolPath)
	if err != nil {
		return err
	}
	if len(poolBitmaps) == 0 {
		return nil
	}

	repoPath, err := repo.Path(ctx)
	if err != nil {
		return err
	}

	memberBitmaps, err := getBitmaps(repoPath)
	if err != nil {
		return err
	}
	if len(memberBitmaps) == 0 {
		return nil
	}

	for _, bitmap := range memberBitmaps {
		if err := os.Remove(bitmap); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}

func getBitmaps(repoPath string) ([]string, error) {
	packDir := filepath.Join(repoPath, "objects/pack")
	entries, err := os.ReadDir(packDir)
	if err != nil {
		return nil, err
	}

	var bitmaps []string
	for _, entry := range entries {
		if name := entry.Name(); strings.HasSuffix(name, ".bitmap") && strings.HasPrefix(name, "pack-") {
			bitmaps = append(bitmaps, filepath.Join(packDir, name))
		}
	}

	return bitmaps, nil
}

func (o *ObjectPool) getRelativeObjectPath(ctx context.Context, repo *localrepo.Repo) (string, error) {
	poolPath, err := o.Path(ctx)
	if err != nil {
		return "", fmt.Errorf("getting object pool path: %w", err)
	}

	repoPath, err := repo.Path(ctx)
	if err != nil {
		return "", fmt.Errorf("getting repository path: %w", err)
	}

	relPath, err := filepath.Rel(filepath.Join(repoPath, "objects"), poolPath)
	if err != nil {
		return "", err
	}

	return filepath.Join(relPath, "objects"), nil
}

// LinkedToRepository tests if a repository is linked to an object pool
func (o *ObjectPool) LinkedToRepository(ctx context.Context, repo *localrepo.Repo) (bool, error) {
	poolPath, err := o.Path(ctx)
	if err != nil {
		return false, fmt.Errorf("getting object pool path: %w", err)
	}

	repoPath, err := repo.Path(ctx)
	if err != nil {
		return false, fmt.Errorf("getting repo path: %w", err)
	}

	altInfo, err := stats.AlternatesInfoForRepository(repoPath)
	if err != nil {
		return false, fmt.Errorf("getting alternates info: %w", err)
	}

	if !altInfo.Exists || len(altInfo.ObjectDirectories) == 0 {
		return false, nil
	}

	relPath := altInfo.ObjectDirectories[0]
	expectedRelPath, err := o.getRelativeObjectPath(ctx, repo)
	if err != nil {
		return false, err
	}

	if relPath == expectedRelPath {
		return true, nil
	}

	if filepath.Clean(relPath) != filepath.Join(poolPath, "objects") {
		return false, fmt.Errorf("unexpected alternates content: %q", relPath)
	}

	return false, nil
}
