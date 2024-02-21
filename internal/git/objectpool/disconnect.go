package objectpool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagectx"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/log"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
)

// Disconnect disconnects the specified repository from its object pool. If the repository does not
// utilize an alternate object database, no error is returned. For repositories that depend on
// alternate objects, the following steps are performed:
//   - Alternate objects are hard-linked to the main repository.
//   - The repository's Git alternates file is backed up and object pool disconnected.
//   - A connectivity check is performed to ensure the repository is complete. If this check fails,
//     the repository is reconnected to the object pool via the backup and an error returned.
//
// This operation carries some risk. If the repository is in a broken state, it will not be restored
// until after the connectivity check completes. If Gitaly crashes before the backup is restored,
// the repository may be in a broken state until an administrator intervenes and restores the backed
// up copy of objects/info/alternates.
func Disconnect(ctx context.Context, repo *localrepo.Repo, logger log.Logger, txManager transaction.Manager) error {
	repoPath, err := repo.Path()
	if err != nil {
		return err
	}

	altInfo, err := stats.AlternatesInfoForRepository(repoPath)
	if err != nil {
		return err
	}

	// If the repository does not have an alternates file or if alternate file does not contain
	// object directories, there is not an object pool repository to disconnect from and no need to
	// continue.
	if !altInfo.Exists || len(altInfo.ObjectDirectories) == 0 {
		// When a repository is not linked to any alternates, ensure the repository is consistent
		// with other replicas.
		if err := transaction.VoteOnContext(ctx, txManager, voting.VoteFromData([]byte("no alternates")), voting.Committed); err != nil {
			return fmt.Errorf("vote on missing alternates: %w", err)
		}

		return nil
	}

	if err := transaction.VoteOnContext(ctx, txManager, voting.VoteFromData([]byte("migrate objects")), voting.Prepared); err != nil {
		return fmt.Errorf("preparatory vote for migrating objects: %w", err)
	}

	// A repository should only ever be linked to a single alternate object directory. If the
	// repository links to multiple object directories, the repository is in an invalid state.
	if len(altInfo.ObjectDirectories) > 1 {
		return errors.New("multiple alternate object directories")
	}

	// If the alternate object directory entry does not exist on disk, the repository's Git
	// alternates file is in an invalid state.
	altObjectDir := altInfo.AbsoluteObjectDirectories()[0]
	altObjectDirStats, err := os.Stat(altObjectDir)
	if err != nil {
		return err
	}

	// If the alternate object directory is not a directory, the repository's Git alternates file is
	// in an invalid state.
	if !altObjectDirStats.IsDir() {
		return errors.New("alternate object entry is not a directory")
	}

	objectFiles, err := findObjectFiles(altObjectDir)
	if err != nil {
		return err
	}

	for _, path := range objectFiles {
		source := filepath.Join(altObjectDir, path)
		target := filepath.Join(repoPath, "objects", path)

		if err := os.MkdirAll(filepath.Dir(target), perm.SharedDir); err != nil {
			return err
		}

		if err := os.Link(source, target); err != nil {
			if os.IsExist(err) {
				continue
			}

			return err
		}
	}

	if err := transaction.VoteOnContext(ctx, txManager, voting.VoteFromData([]byte("migrate objects")), voting.Committed); err != nil {
		return fmt.Errorf("committed vote for migrating objects: %w", err)
	}

	altFile, err := repo.InfoAlternatesPath()
	if err != nil {
		return err
	}

	backupFile, err := newBackupFile(altFile)
	if err != nil {
		return err
	}

	return removeAlternatesIfOk(ctx, repo, altFile, backupFile, logger, txManager)
}

func findObjectFiles(altDir string) ([]string, error) {
	var objectFiles []string
	if walkErr := filepath.Walk(altDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(altDir, path)
		if err != nil {
			return err
		}

		if strings.HasPrefix(rel, "info/") {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		objectFiles = append(objectFiles, rel)

		return nil
	}); walkErr != nil {
		return nil, walkErr
	}

	sort.Sort(objectPaths(objectFiles))

	return objectFiles, nil
}

type objectPaths []string

func (o objectPaths) Len() int      { return len(o) }
func (o objectPaths) Swap(i, j int) { o[i], o[j] = o[j], o[i] }

func (o objectPaths) Less(i, j int) bool {
	return objectPriority(o[i]) <= objectPriority(o[j])
}

// Based on pack_copy_priority in git/tmp-objdir.c
func objectPriority(name string) int {
	if !strings.HasPrefix(name, "pack") {
		return 0
	}
	if strings.HasSuffix(name, ".keep") {
		return 1
	}
	if strings.HasSuffix(name, ".pack") {
		return 2
	}
	if strings.HasSuffix(name, ".idx") {
		return 3
	}
	return 4
}

func newBackupFile(altFile string) (string, error) {
	randSuffix, err := text.RandomHex(6)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s.%d.%s", altFile, time.Now().Unix(), randSuffix), nil
}

// removeAlternatesIfOk is dangerous. We optimistically temporarily
// rename objects/info/alternates, and run `git fsck` to see if the
// resulting repo is connected. If this fails we restore
// objects/info/alternates. If the repo is not connected for whatever
// reason, then until this function returns, probably **all concurrent
// RPC calls to the repo will fail**. Also, if Gitaly crashes in the
// middle of this function, the repo is left in a broken state. We do
// take care to leave a copy of the alternates file, so that it can be
// manually restored by an administrator if needed.
func removeAlternatesIfOk(ctx context.Context, repo *localrepo.Repo, altFile, backupFile string, logger log.Logger, txManager transaction.Manager) error {
	if err := transaction.VoteOnContext(ctx, txManager, voting.VoteFromData([]byte("disconnect alternate")), voting.Prepared); err != nil {
		return fmt.Errorf("preparatory vote for disconnecting alternate: %w", err)
	}

	if err := os.Rename(altFile, backupFile); err != nil {
		return err
	}

	rollback := true
	defer func() {
		if !rollback {
			return
		}

		// If we would do a os.Rename, and then someone else comes and clobbers
		// our file, it's gone forever. This trick with os.Link and os.Rename
		// is equivalent to "cp $backupFile $altFile", meaning backupFile is
		// preserved for possible forensic use.
		tmp := backupFile + ".2"

		if err := os.Link(backupFile, tmp); err != nil {
			logger.WithError(err).ErrorContext(ctx, "copy backup alternates file")
			return
		}

		if err := os.Rename(tmp, altFile); err != nil {
			logger.WithError(err).ErrorContext(ctx, "restore backup alternates file")
		}
	}()

	// The choice here of git rev-list is for performance reasons.
	// git fsck --connectivity-only performed badly for large
	// repositories. The reasons are detailed in https://lore.kernel.org/git/9304B938-4A59-456B-B091-DBBCAA1823B2@gmail.com/
	cmd, err := repo.Exec(ctx, git.Command{
		Name: "rev-list",
		Flags: []git.Option{
			git.Flag{Name: "--objects"},
			git.Flag{Name: "--all"},
			git.Flag{Name: "--quiet"},
		},
	})
	if err != nil {
		return err
	}

	if err := cmd.Wait(); err != nil {
		return &connectivityError{error: err}
	}

	if err := transaction.VoteOnContext(ctx, txManager, voting.VoteFromData([]byte("disconnect alternate")), voting.Committed); err != nil {
		return fmt.Errorf("committing vote for disconnecting alternate: %w", err)
	}

	storagectx.RunWithTransaction(ctx, func(tx storagectx.Transaction) {
		tx.MarkAlternateUpdated()
	})

	// The repository should only be disconnected from its object pool if validation is successful.
	// If validation fails or transaction quorum is not achieved, alternates rollback is performed.
	rollback = false
	return nil
}

type connectivityError struct{ error }

func (fe *connectivityError) Error() string {
	return fmt.Sprintf("git connectivity error while disconnected: %v", fe.error)
}
