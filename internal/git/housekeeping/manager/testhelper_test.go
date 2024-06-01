package manager

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/keyvalue"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

type objectsState struct {
	looseObjects            uint64
	packfiles               uint64
	cruftPacks              uint64
	keepPacks               uint64
	hasBitmap               bool
	hasMultiPackIndex       bool
	hasMultiPackIndexBitmap bool
}

func requireObjectsState(tb testing.TB, repo *localrepo.Repo, expectedState objectsState) {
	tb.Helper()
	ctx := testhelper.Context(tb)

	repoInfo, err := stats.RepositoryInfoForRepository(ctx, repo)
	require.NoError(tb, err)

	require.Equal(tb, expectedState, objectsState{
		looseObjects:            repoInfo.LooseObjects.Count,
		packfiles:               repoInfo.Packfiles.Count,
		cruftPacks:              repoInfo.Packfiles.CruftCount,
		keepPacks:               repoInfo.Packfiles.KeepCount,
		hasBitmap:               repoInfo.Packfiles.Bitmap.Exists,
		hasMultiPackIndex:       repoInfo.Packfiles.MultiPackIndex.Exists,
		hasMultiPackIndexBitmap: repoInfo.Packfiles.MultiPackIndexBitmap.Exists,
	})
}

func testRepoAndPool(t *testing.T, desc string, testFunc func(t *testing.T, relativePath string)) {
	t.Helper()
	t.Run(desc, func(t *testing.T) {
		t.Run("normal repository", func(t *testing.T) {
			testFunc(t, gittest.NewRepositoryName(t))
		})

		t.Run("object pool", func(t *testing.T) {
			testFunc(t, gittest.NewObjectPoolName(t))
		})
	})
}

func testWithAndWithoutTransaction(t *testing.T, desc string, testFunc func(*testing.T, config.Cfg, *storagemgr.PartitionManager)) {
	t.Helper()
	t.Run(desc, func(t *testing.T) {
		t.Run("with transaction", func(t *testing.T) {
			testhelper.SkipWithReftable(t, "reftable is not supported in transactional housekeeping: https://gitlab.com/gitlab-org/gitaly/-/issues/5867")

			cfg := testcfg.Build(t)

			logger := testhelper.SharedLogger(t)
			cmdFactory := gittest.NewCommandFactory(t, cfg)
			catfileCache := catfile.NewCache(cfg)
			t.Cleanup(catfileCache.Stop)

			localRepoFactory := localrepo.NewFactory(logger, config.NewLocator(cfg), cmdFactory, catfileCache)

			dbMgr, err := keyvalue.NewDBManager(
				cfg.Storages,
				keyvalue.DatabaseOpenerFunc(keyvalue.NewBadgerStore),
				helper.NewNullTickerFactory(),
				logger,
			)
			require.NoError(t, err)
			t.Cleanup(dbMgr.Close)

			partitionManager, err := storagemgr.NewPartitionManager(
				cfg.Storages,
				cmdFactory,
				localRepoFactory,
				logger,
				dbMgr,
				cfg.Prometheus,
				nil,
			)
			require.NoError(t, err)
			defer partitionManager.Close()

			testFunc(t, cfg, partitionManager)
		})

		t.Run("without transaction", func(t *testing.T) {
			cfg := testcfg.Build(t)
			testFunc(t, cfg, nil)
		})
	})
}

type cleanStaleDataMetrics struct {
	configkeys     int
	configsections int
	objects        int
	locks          int
	refs           int
	reflocks       int
	reftablelocks  int
	refsEmptyDir   int
	packFileLocks  int
	packedRefsLock int
	packedRefsNew  int
	serverInfo     int
	configFullpath int
	infoAttributes int
}

func requireCleanStaleDataMetrics(t *testing.T, m *RepositoryManager, metrics cleanStaleDataMetrics) {
	t.Helper()

	var builder strings.Builder

	_, err := builder.WriteString("# HELP gitaly_housekeeping_pruned_files_total Total number of files pruned\n")
	require.NoError(t, err)
	_, err = builder.WriteString("# TYPE gitaly_housekeeping_pruned_files_total counter\n")
	require.NoError(t, err)

	for metric, expectedValue := range map[string]int{
		"configkeys":     metrics.configkeys,
		"configsections": metrics.configsections,
		"objects":        metrics.objects,
		"locks":          metrics.locks,
		"refs":           metrics.refs,
		"reflocks":       metrics.reflocks,
		"reftablelocks":  metrics.reftablelocks,
		"packfilelocks":  metrics.packFileLocks,
		"packedrefslock": metrics.packedRefsLock,
		"packedrefsnew":  metrics.packedRefsNew,
		"refsemptydir":   metrics.refsEmptyDir,
		"serverinfo":     metrics.serverInfo,
		"configfullpath": metrics.configFullpath,
		"infoattributes": metrics.infoAttributes,
	} {
		_, err := builder.WriteString(fmt.Sprintf("gitaly_housekeeping_pruned_files_total{filetype=%q} %d\n", metric, expectedValue))
		require.NoError(t, err)
	}

	require.NoError(t, testutil.CollectAndCompare(m, strings.NewReader(builder.String()), "gitaly_housekeeping_pruned_files_total"))
}

func requireReferenceLockCleanupMetrics(t *testing.T, m *RepositoryManager, metrics cleanStaleDataMetrics) {
	t.Helper()

	var builder strings.Builder

	_, err := builder.WriteString("# HELP gitaly_housekeeping_pruned_files_total Total number of files pruned\n")
	require.NoError(t, err)
	_, err = builder.WriteString("# TYPE gitaly_housekeeping_pruned_files_total counter\n")
	require.NoError(t, err)

	for metric, expectedValue := range map[string]int{
		"reflocks": metrics.reflocks,
	} {
		_, err := builder.WriteString(fmt.Sprintf("gitaly_housekeeping_pruned_files_total{filetype=%q} %d\n", metric, expectedValue))
		require.NoError(t, err)
	}

	require.NoError(t, testutil.CollectAndCompare(m, strings.NewReader(builder.String()), "gitaly_housekeeping_pruned_files_total"))
}

type entry interface {
	create(t *testing.T, parent string)
	validate(t *testing.T, parent string)
}

// fileEntry is an entry implementation for a file
type fileEntry struct {
	name       string
	data       string
	mode       os.FileMode
	age        time.Duration
	finalState entryFinalState
}

func (f *fileEntry) create(t *testing.T, parent string) {
	t.Helper()

	filename := filepath.Join(parent, f.name)
	require.NoError(t, os.WriteFile(filename, []byte(f.data), f.mode))

	filetime := time.Now().Add(-f.age)
	require.NoError(t, os.Chtimes(filename, filetime, filetime))
}

func (f *fileEntry) validate(t *testing.T, parent string) {
	t.Helper()

	filename := filepath.Join(parent, f.name)
	f.checkExistence(t, filename)
}

func (f *fileEntry) checkExistence(t *testing.T, filename string) {
	t.Helper()
	_, err := os.Stat(filename)
	if err == nil && f.finalState == Delete {
		t.Errorf("Expected %v to have been deleted.", filename)
	} else if err != nil && f.finalState == Keep {
		t.Errorf("Expected %v to not have been deleted.", filename)
	}
}

// dirEntry is an entry implementation for a directory. A file with entries
type dirEntry struct {
	fileEntry
	entries []entry
}

func (d *dirEntry) create(t *testing.T, parent string) {
	t.Helper()

	dirname := filepath.Join(parent, d.name)

	if err := os.Mkdir(dirname, perm.PrivateDir); err != nil {
		require.True(t, os.IsExist(err), "mkdir failed: %v", dirname)
	}

	for _, e := range d.entries {
		e.create(t, dirname)
	}

	// Apply permissions and times after the children have been created
	require.NoError(t, os.Chmod(dirname, d.mode))
	filetime := time.Now().Add(-d.age)
	require.NoError(t, os.Chtimes(dirname, filetime, filetime))
}

func (d *dirEntry) validate(t *testing.T, parent string) {
	t.Helper()

	dirname := filepath.Join(parent, d.name)
	d.checkExistence(t, dirname)

	for _, e := range d.entries {
		e.validate(t, dirname)
	}
}

type entryOption func(entry *fileEntry)

func withAge(age time.Duration) entryOption {
	return func(entry *fileEntry) {
		entry.age = age
	}
}

func withData(data string) entryOption {
	return func(entry *fileEntry) {
		entry.data = data
	}
}

func withMode(mode os.FileMode) entryOption {
	return func(entry *fileEntry) {
		entry.mode = mode
	}
}

func expectDeletion(entry *fileEntry) {
	entry.finalState = Delete
}

func f(name string, opts ...entryOption) *fileEntry {
	entry := &fileEntry{
		name:       name,
		mode:       perm.PrivateFile,
		age:        ancient,
		finalState: Keep,
	}

	for _, opt := range opts {
		opt(entry)
	}

	return entry
}

func d(name string, entries []entry, opts ...entryOption) *dirEntry {
	opts = append([]entryOption{withMode(perm.PrivateDir)}, opts...)

	return &dirEntry{
		fileEntry: *f(name, opts...),
		entries:   entries,
	}
}
