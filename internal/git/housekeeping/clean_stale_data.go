package housekeeping

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/safe"
)

const (
	emptyRefsGracePeriod             = 24 * time.Hour
	deleteTempFilesOlderThanDuration = 24 * time.Hour
	brokenRefsGracePeriod            = 24 * time.Hour
	lockfileGracePeriod              = 15 * time.Minute
	packedRefsLockGracePeriod        = 1 * time.Hour
	packedRefsNewGracePeriod         = 15 * time.Minute
	packFileLockGracePeriod          = 7 * 24 * time.Hour
	// ReferenceLockfileGracePeriod is the grace period when cleaning up lock files of individual references in the
	// repository. Lock files existing less than this period are ignored.
	ReferenceLockfileGracePeriod = 1 * time.Hour
	// ReftableLockfileGracePeriod is the grace period when cleaning up lock files reftable.
	ReftableLockfileGracePeriod = 1 * time.Hour
)

var lockfiles = []string{
	"config.lock",
	"HEAD.lock",
	"info/attributes.lock",
	"objects/info/alternates.lock",
	"objects/info/commit-graphs/commit-graph-chain.lock",
	"objects/pack/multi-pack-index.lock",
}

type (
	// FindStaleFileFunc is a type of function that returns the list of stale files in the repository.
	FindStaleFileFunc func(context.Context, string) ([]string, error)
	// CleanupRepoFunc is a type of function that performs a clean up task in the repository.
	CleanupRepoFunc              func(context.Context, *localrepo.Repo) (int, error)
	cleanupRepoWithTxManagerFunc func(context.Context, *localrepo.Repo, transaction.Manager) (int, error)
)

// CleanStaleDataConfig is the configuration for running CleanStaleData. It is used to define
// the different types of cleanups we want to run.
type CleanStaleDataConfig struct {
	// StaleFileFinders contains the list of finder functions to find stale files in the repository.
	StaleFileFinders map[string]FindStaleFileFunc
	// RepoCleanups contains the list of clean-up functions for the repository.
	RepoCleanups map[string]CleanupRepoFunc
	// RepoCleanupWithTxManagers contains the list of clean-up functions with transaction manager support.
	RepoCleanupWithTxManagers map[string]cleanupRepoWithTxManagerFunc
}

// OnlyStaleReferenceLockCleanup returns a config which only contains a
// stale reference lock cleaner with the provided grace period.
func OnlyStaleReferenceLockCleanup(gracePeriod time.Duration) CleanStaleDataConfig {
	return CleanStaleDataConfig{
		StaleFileFinders: map[string]FindStaleFileFunc{
			"reflocks": FindStaleReferenceLocks(gracePeriod),
		},
	}
}

// DefaultStaleDataCleanup is the default configuration for CleanStaleData
// which contains all the cleanup functions.
func DefaultStaleDataCleanup() CleanStaleDataConfig {
	return CleanStaleDataConfig{
		StaleFileFinders: map[string]FindStaleFileFunc{
			"objects":        FindTemporaryObjects,
			"locks":          FindStaleLockfiles,
			"refs":           FindBrokenLooseReferences,
			"reflocks":       FindStaleReferenceLocks(ReferenceLockfileGracePeriod),
			"reftablelocks":  FindStaleReftableLock,
			"packfilelocks":  FindStalePackFileLocks,
			"packedrefslock": FindPackedRefsLock,
			"packedrefsnew":  FindPackedRefsNew,
			"serverinfo":     FindServerInfo,
		},
		RepoCleanups: map[string]CleanupRepoFunc{
			"refsemptydir":   RemoveRefEmptyDirs,
			"configsections": PruneEmptyConfigSections,
			"infoattributes": RemoveInfoAttributes,
		},
		RepoCleanupWithTxManagers: map[string]cleanupRepoWithTxManagerFunc{
			"configkeys": removeUnnecessaryConfig,
			// Gitaly used to set `gitlab.fullpath` via the SetFullPath RPC.
			// That RPC was removed in https://gitlab.com/groups/gitlab-org/-/epics/8953. This housekeeping
			// task removes the now unused repository config entry.
			"configfullpath": removeGitLabFullPathConfig,
		},
	}
}

// PruneEmptyConfigSections prunes all empty sections from the repo's config.
func PruneEmptyConfigSections(ctx context.Context, repo *localrepo.Repo) (_ int, returnedErr error) {
	repoPath, err := repo.Path(ctx)
	if err != nil {
		return 0, fmt.Errorf("getting repo path: %w", err)
	}
	configPath := filepath.Join(repoPath, "config")

	// The gitconfig shouldn't ever be big given that we nowadays don't write any unbounded
	// values into it anymore. Slurping it into memory should thus be fine.
	configContents, err := os.ReadFile(configPath)
	if err != nil {
		return 0, fmt.Errorf("reading config: %w", err)
	}
	configLines := strings.SplitAfter(string(configContents), "\n")
	if configLines[len(configLines)-1] == "" {
		// Strip the last line if it's empty.
		configLines = configLines[:len(configLines)-1]
	}

	skippedSections := 0

	// We now filter out any empty sections. A section is empty if the next line is a section
	// header as well, or if it is the last line in the gitconfig. This isn't quite the whole
	// story given that a section can also be empty if it just ain't got any keys, but only
	// comments or whitespace. But we only ever write the gitconfig programmatically, so we
	// shouldn't typically see any such cases at all.
	filteredLines := make([]string, 0, len(configLines))
	for i := 0; i < len(configLines)-1; i++ {
		// Skip if we have two consecutive section headers.
		if isSectionHeader(configLines[i]) && isSectionHeader(configLines[i+1]) {
			skippedSections++
			continue
		}
		filteredLines = append(filteredLines, configLines[i])
	}
	// The final line is always stripped in case it is a section header.
	if len(configLines) > 0 && !isSectionHeader(configLines[len(configLines)-1]) {
		skippedSections++
		filteredLines = append(filteredLines, configLines[len(configLines)-1])
	}

	// If we haven't filtered out anything then there is no need to update the target file.
	if len(configLines) == len(filteredLines) {
		return 0, nil
	}

	// Otherwise, we need to update the repository's configuration.
	configWriter, err := safe.NewLockingFileWriter(configPath)
	if err != nil {
		return 0, fmt.Errorf("creating config configWriter: %w", err)
	}
	defer func() {
		if err := configWriter.Close(); err != nil && returnedErr == nil {
			returnedErr = fmt.Errorf("closing config writer: %w", err)
		}
	}()

	for _, filteredLine := range filteredLines {
		if _, err := configWriter.Write([]byte(filteredLine)); err != nil {
			return 0, fmt.Errorf("writing filtered config: %w", err)
		}
	}

	// This is a sanity check to assert that we really didn't change anything as seen by
	// Git. We run `git config -l` on both old and new file and assert that they report
	// the same config entries. Because empty sections are never reported we shouldn't
	// see those, and as a result any difference in output is a difference we need to
	// worry about.
	var configOutputs []string
	for _, path := range []string{configPath, configWriter.Path()} {
		var configOutput bytes.Buffer
		if err := repo.ExecAndWait(ctx, git.Command{
			Name: "config",
			Flags: []git.Option{
				git.ValueFlag{Name: "-f", Value: path},
				git.Flag{Name: "-l"},
			},
		}, git.WithStdout(&configOutput)); err != nil {
			return 0, fmt.Errorf("listing config: %w", err)
		}

		configOutputs = append(configOutputs, configOutput.String())
	}
	if configOutputs[0] != configOutputs[1] {
		return 0, fmt.Errorf("section pruning has caused config change")
	}

	// We don't use transactional voting but commit the file directly -- we have asserted that
	// the change is idempotent anyway.
	if err := configWriter.Lock(); err != nil {
		return 0, fmt.Errorf("failed locking config: %w", err)
	}
	if err := configWriter.Commit(); err != nil {
		return 0, fmt.Errorf("failed committing pruned config: %w", err)
	}

	return skippedSections, nil
}

func isSectionHeader(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
}

// findStaleFiles determines whether any of the given files rooted at repoPath
// are stale or not. A file is considered stale if it exists and if it has not
// been modified during the gracePeriod. A nonexistent file is not considered
// to be a stale file and will not cause an error.
func findStaleFiles(repoPath string, gracePeriod time.Duration, files ...string) ([]string, error) {
	var staleFiles []string

	for _, file := range files {
		path := filepath.Join(repoPath, file)

		fileInfo, err := os.Stat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		if time.Since(fileInfo.ModTime()) < gracePeriod {
			continue
		}

		staleFiles = append(staleFiles, path)
	}

	return staleFiles, nil
}

// FindStaleLockfiles finds a subset of lockfiles which may be created by git
// commands. We're quite conservative with what we're removing, we certainly
// don't just scan the repo for `*.lock` files. Instead, we only remove a known
// set of lockfiles which have caused problems in the past.
func FindStaleLockfiles(ctx context.Context, repoPath string) ([]string, error) {
	return findStaleFiles(repoPath, lockfileGracePeriod, lockfiles...)
}

// FindStalePackFileLocks finds packfile locks (`.keep` files) that git-receive-pack(1) and
// git-fetch-pack(1) write in order to not have the newly written packfile be deleted while refs
// have not yet been updated to point to their objects. Normally, these locks would get removed by
// both Git commands once the refs have been updated. But when the command gets killed meanwhile,
// then it can happen that the locks are left behind. As Git will never delete packfiles with an
// associated `.keep` file, the end result is that we may accumulate more and more of these locked
// packfiles over time.
func FindStalePackFileLocks(ctx context.Context, repoPath string) ([]string, error) {
	locks, err := filepath.Glob(filepath.Join(repoPath, "objects", "pack", "pack-*.keep"))
	if err != nil {
		return nil, fmt.Errorf("enumerating .keep files: %w", err)
	}

	var staleLocks []string
	for _, lock := range locks {
		lockInfo, err := os.Stat(lock)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("statting .keep: %w", err)
		}

		// We use a grace period so that we don't accidentally delete a packfile for a
		// concurrent fetch or push.
		if time.Since(lockInfo.ModTime()) < packFileLockGracePeriod {
			continue
		}

		contents, err := os.ReadFile(lock)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("reading .keep: %w", err)
		}

		// Git will write either of the following messages into the `.keep` file when
		// written by either git-fetch-pack(1) or git-receive-pack(1):
		//
		// - "fetch-pack $PID on $HOSTNAME"
		// - "receive-pack $PID on $HOSTNAME"
		//
		// This allows us to identify these `.keep` files. We skip over any `.keep` files
		// that have an unknown prefix as they might've been written by an admin in order to
		// keep certain objects alive, whatever the reason.
		if !bytes.HasPrefix(contents, []byte("receive-pack ")) && !bytes.HasPrefix(contents, []byte("fetch-pack ")) {
			continue
		}

		staleLocks = append(staleLocks, lock)
	}

	return staleLocks, nil
}

// FindTemporaryObjects find temporary objects of a repository. Those objects are created by some git processes
// and are sure to be removed when the processes are done.
func FindTemporaryObjects(ctx context.Context, repoPath string) ([]string, error) {
	var temporaryObjects []string

	if err := filepath.WalkDir(filepath.Join(repoPath, "objects"), func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}

		// Git will never create temporary directories, but only temporary objects,
		// packfiles and packfile indices.
		if dirEntry.IsDir() {
			return nil
		}

		isStale, err := isStaleTemporaryObject(dirEntry)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}

			return fmt.Errorf("checking for stale temporary object: %w", err)
		}

		if !isStale {
			return nil
		}

		temporaryObjects = append(temporaryObjects, path)

		return nil
	}); err != nil {
		return nil, fmt.Errorf("walking object directory: %w", err)
	}

	return temporaryObjects, nil
}

func isStaleTemporaryObject(dirEntry fs.DirEntry) (bool, error) {
	// Check the entry's name first so that we can ideally avoid stat'ting the entry.
	if !strings.HasPrefix(dirEntry.Name(), "tmp_") {
		return false, nil
	}

	fi, err := dirEntry.Info()
	if err != nil {
		return false, err
	}

	if time.Since(fi.ModTime()) <= deleteTempFilesOlderThanDuration {
		return false, nil
	}

	return true, nil
}

// FindBrokenLooseReferences return the list of broken refs. A ref is considered to be broken when the loose ref file is
// empty. We don't validate the existence of referred objects.
func FindBrokenLooseReferences(ctx context.Context, repoPath string) ([]string, error) {
	var brokenRefs []string
	if err := filepath.WalkDir(filepath.Join(repoPath, "refs"), func(path string, dirEntry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) || errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}

		if dirEntry.IsDir() {
			return nil
		}

		fi, err := dirEntry.Info()
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}

			return fmt.Errorf("statting loose ref: %w", err)
		}

		// When git crashes or a node reboots, it may happen that it leaves behind empty
		// references. These references break various assumptions made by git and cause it
		// to error in various circumstances. We thus clean them up to work around the
		// issue.
		if fi.Size() > 0 || time.Since(fi.ModTime()) < brokenRefsGracePeriod {
			return nil
		}

		brokenRefs = append(brokenRefs, path)

		return nil
	}); err != nil {
		return nil, fmt.Errorf("walking references: %w", err)
	}

	return brokenRefs, nil
}

// FindStaleReferenceLocks provides a function which scans the refdb for stale locks
// and loose references against the provided grace period.
func FindStaleReferenceLocks(gracePeriod time.Duration) FindStaleFileFunc {
	return func(_ context.Context, repoPath string) ([]string, error) {
		var staleReferenceLocks []string

		if err := filepath.WalkDir(filepath.Join(repoPath, "refs"), func(path string, dirEntry fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
					return nil
				}

				return err
			}

			if dirEntry.IsDir() {
				return nil
			}

			if !strings.HasSuffix(dirEntry.Name(), ".lock") {
				return nil
			}

			fi, err := dirEntry.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}

				return fmt.Errorf("statting reference lock: %w", err)
			}

			if time.Since(fi.ModTime()) < gracePeriod {
				return nil
			}

			staleReferenceLocks = append(staleReferenceLocks, path)
			return nil
		}); err != nil {
			return nil, fmt.Errorf("walking refs: %w", err)
		}

		return staleReferenceLocks, nil
	}
}

// FindStaleReftableLock provides a function which scans the reftable folder
// for stale a reftable lock and deletes it.
func FindStaleReftableLock(_ context.Context, repoPath string) ([]string, error) {
	lockPath := filepath.Join(repoPath, "reftable", "tables.list.lock")
	stat, err := os.Stat(lockPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("statting reftable lock: %w", err)
	}

	if time.Since(stat.ModTime()) < ReftableLockfileGracePeriod {
		return nil, nil
	}

	return []string{lockPath}, nil
}

func removeUnnecessaryConfig(ctx context.Context, repository *localrepo.Repo, txManager transaction.Manager) (int, error) {
	unnecessaryConfigRegex := "^(http\\..+\\.extraheader|remote\\..+\\.(fetch|mirror|prune|url)|core\\.(commitgraph|sparsecheckout|splitindex))$"
	if err := repository.UnsetMatchingConfig(ctx, unnecessaryConfigRegex, txManager); err != nil {
		if !errors.Is(err, git.ErrNotFound) {
			return 0, fmt.Errorf("unsetting unnecessary config lines: %w", err)
		}

		return 0, nil
	}

	// If we didn't get an error we know that we've deleted _something_. We just set
	// this variable to `1` because we don't count how many keys we have deleted. It's
	// probably good enough: we only want to know whether we're still pruning such old
	// configuration or not, but typically don't care how many there are so that we know
	// when to delete this cleanup of legacy data.
	return 1, nil
}

func removeGitLabFullPathConfig(ctx context.Context, repository *localrepo.Repo, txManager transaction.Manager) (int, error) {
	if err := repository.UnsetMatchingConfig(ctx, "gitlab.fullpath", txManager); err != nil {
		if !errors.Is(err, git.ErrNotFound) {
			return 0, fmt.Errorf("unsetting `gitlab.fullpath` config: %w", err)
		}

		return 0, nil
	}

	return 1, nil
}

// FindPackedRefsLock returns stale lockfiles for the packed-refs file.
func FindPackedRefsLock(ctx context.Context, repoPath string) ([]string, error) {
	return findStaleFiles(repoPath, packedRefsLockGracePeriod, "packed-refs.lock")
}

// FindPackedRefsNew returns stale temporary packed-refs files.
func FindPackedRefsNew(ctx context.Context, repoPath string) ([]string, error) {
	return findStaleFiles(repoPath, packedRefsNewGracePeriod, "packed-refs.new")
}

// FindServerInfo returns files generated by git-update-server-info(1). These files are only
// required to serve Git fetches via the dumb HTTP protocol, which we don't serve at all. It's thus
// safe to remove all of those files without a grace period.
func FindServerInfo(ctx context.Context, repoPath string) ([]string, error) {
	var serverInfoFiles []string

	for directory, basename := range map[string]string{
		filepath.Join(repoPath, "info"):            "refs",
		filepath.Join(repoPath, "objects", "info"): "packs",
	} {
		entries, err := os.ReadDir(directory)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			return nil, fmt.Errorf("reading info directory: %w", err)
		}

		for _, entry := range entries {
			if !entry.Type().IsRegular() {
				continue
			}

			// An exact match is the actual file we care about, while the latter pattern
			// refers to the temporary files Git uses to write those files.
			if entry.Name() != basename && !strings.HasPrefix(entry.Name(), basename+"_") {
				continue
			}

			serverInfoFiles = append(serverInfoFiles, filepath.Join(directory, entry.Name()))
		}
	}

	return serverInfoFiles, nil
}

// RemoveRefEmptyDirs removes empty directories inside refs directory. They are left-over of ref deletion
// operations.
func RemoveRefEmptyDirs(ctx context.Context, repository *localrepo.Repo) (int, error) {
	rPath, err := repository.Path(ctx)
	if err != nil {
		return 0, err
	}
	repoRefsPath := filepath.Join(rPath, "refs")

	// we never want to delete the actual "refs" directory, so we start the
	// recursive functions for each subdirectory
	entries, err := os.ReadDir(repoRefsPath)
	if err != nil {
		return 0, err
	}

	prunedDirsTotal := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		prunedDirs, err := removeEmptyDirs(ctx, filepath.Join(repoRefsPath, e.Name()))
		if err != nil {
			return prunedDirsTotal, err
		}
		prunedDirsTotal += prunedDirs
	}

	return prunedDirsTotal, nil
}

func removeEmptyDirs(ctx context.Context, target string) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	// We need to stat the directory early on in order to get its current mtime. If we
	// did this after we have removed empty child directories, then its mtime would've
	// changed and we wouldn't consider it for deletion.
	dirStat, err := os.Stat(target)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}

	entries, err := os.ReadDir(target)
	switch {
	case os.IsNotExist(err):
		return 0, nil // race condition: someone else deleted it first
	case err != nil:
		return 0, err
	}

	prunedDirsTotal := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		prunedDirs, err := removeEmptyDirs(ctx, filepath.Join(target, e.Name()))
		if err != nil {
			return prunedDirsTotal, err
		}
		prunedDirsTotal += prunedDirs
	}

	// If the directory is older than the grace period for empty refs, then we can
	// consider it for deletion in case it's empty.
	if time.Since(dirStat.ModTime()) < emptyRefsGracePeriod {
		return prunedDirsTotal, nil
	}

	// recheck entries now that we have potentially removed some dirs
	entries, err = os.ReadDir(target)
	if err != nil && !os.IsNotExist(err) {
		return prunedDirsTotal, err
	}
	if len(entries) > 0 {
		return prunedDirsTotal, nil
	}

	switch err := os.Remove(target); {
	case os.IsNotExist(err):
		return prunedDirsTotal, nil // race condition: someone else deleted it first
	case err != nil:
		return prunedDirsTotal, err
	}

	return prunedDirsTotal + 1, nil
}

// RemoveInfoAttributes removes attributes file inside the info directory. This is because,
// From git 2.43 on, Gitaly will read from HEAD:.gitattributes instead of
// .info/attributes. The old info/attributes file must be
// removed because it has higher precedence than HEAD:.gitattributes.
func RemoveInfoAttributes(ctx context.Context, repo *localrepo.Repo) (int, error) {
	rPath, err := repo.Path(ctx)
	if err != nil {
		return 0, fmt.Errorf("find repo path: %w", err)
	}

	attrFile := filepath.Join(rPath, "info", "attributes")
	err = os.Remove(attrFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	return 1, nil
}
