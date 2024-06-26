package snapshot

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"golang.org/x/sync/errgroup"
)

func TestManager(t *testing.T) {
	ctx := testhelper.Context(t)

	umask := testhelper.Umask()

	writeFile := func(t *testing.T, storageDir string, snapshot FileSystem, relativePath string) {
		t.Helper()

		require.NoError(t, os.WriteFile(filepath.Join(storageDir, snapshot.RelativePath(relativePath)), nil, fs.ModePerm))
	}

	type metricValues struct {
		createdExclusiveSnapshotCounter   uint64
		destroyedExclusiveSnapshotCounter uint64
		createdSharedSnapshotCounter      uint64
		reusedSharedSnapshotCounter       uint64
		destroyedSharedSnapshotCounter    uint64
	}

	for _, tc := range []struct {
		desc            string
		run             func(t *testing.T, mgr *Manager)
		expectedMetrics metricValues
	}{
		{
			desc: "exclusive snapshots are not shared",
			run: func(t *testing.T, mgr *Manager) {
				fs1, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, true)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs1)

				fs2, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, true)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs2)

				require.NotEqual(t, fs1.Root(), fs2.Root())

				writeFile(t, mgr.storageDir, fs1, "repositories/a/fs1")
				writeFile(t, mgr.storageDir, fs2, "repositories/a/fs2")

				testhelper.RequireDirectoryState(t, fs1.Root(), "", testhelper.DirectoryState{
					// The snapshotting process does not use the existing permissions for
					// directories in the hierarchy before the repository directories.
					"/":                       {Mode: fs.ModeDir | umask.Mask(perm.PrivateDir)},
					"/repositories":           {Mode: fs.ModeDir | umask.Mask(perm.PrivateDir)},
					"/repositories/a":         {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/refs":    {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/objects": {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/HEAD":    {Mode: umask.Mask(fs.ModePerm), Content: []byte("a content")},
					"/repositories/a/fs1":     {Mode: umask.Mask(fs.ModePerm), Content: []byte{}},
				})

				testhelper.RequireDirectoryState(t, fs2.Root(), "", testhelper.DirectoryState{
					"/":                       {Mode: fs.ModeDir | umask.Mask(perm.PrivateDir)},
					"/repositories":           {Mode: fs.ModeDir | umask.Mask(perm.PrivateDir)},
					"/repositories/a":         {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/refs":    {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/objects": {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/HEAD":    {Mode: umask.Mask(fs.ModePerm), Content: []byte("a content")},
					"/repositories/a/fs2":     {Mode: umask.Mask(fs.ModePerm), Content: []byte{}},
				})
			},
			expectedMetrics: metricValues{
				createdExclusiveSnapshotCounter:   2,
				destroyedExclusiveSnapshotCounter: 2,
			},
		},
		{
			desc: "shared snapshots are shared",
			run: func(t *testing.T, mgr *Manager) {
				fs1, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs1)

				fs2, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs2)

				require.Equal(t, fs1.Root(), fs2.Root())

				// We shouldn't in reality write to the shared snapshots but this goes to demonstrate
				// the snapshot is shared.
				writeFile(t, mgr.storageDir, fs1, "repositories/a/shared-file")

				expectedDirectoryState := testhelper.DirectoryState{
					"/":                           {Mode: fs.ModeDir | umask.Mask(perm.PrivateDir)},
					"/repositories":               {Mode: fs.ModeDir | umask.Mask(perm.PrivateDir)},
					"/repositories/a":             {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/refs":        {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/objects":     {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/HEAD":        {Mode: umask.Mask(fs.ModePerm), Content: []byte("a content")},
					"/repositories/a/shared-file": {Mode: umask.Mask(fs.ModePerm), Content: []byte{}},
				}

				testhelper.RequireDirectoryState(t, fs1.Root(), "", expectedDirectoryState)
				testhelper.RequireDirectoryState(t, fs2.Root(), "", expectedDirectoryState)
			},
			expectedMetrics: metricValues{
				createdSharedSnapshotCounter:   1,
				reusedSharedSnapshotCounter:    1,
				destroyedSharedSnapshotCounter: 1,
			},
		},
		{
			desc: "multiple relative paths are snapshotted",
			run: func(t *testing.T, mgr *Manager) {
				fs1, err := mgr.GetSnapshot(ctx, []string{"repositories/a", "repositories/b"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs1)

				// The order of the relative paths should not prevent sharing a snapshot.
				fs2, err := mgr.GetSnapshot(ctx, []string{"repositories/b", "repositories/a"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs2)

				require.Equal(t, fs1.Root(), fs2.Root())

				// We shouldn't in reality write to the shared snapshots but this goes to demonstrate
				// the snapshot is shared.
				writeFile(t, mgr.storageDir, fs1, "repositories/a/shared-file")

				expectedDirectoryState := testhelper.DirectoryState{
					"/":                           {Mode: fs.ModeDir | umask.Mask(perm.PrivateDir)},
					"/repositories":               {Mode: fs.ModeDir | umask.Mask(perm.PrivateDir)},
					"/repositories/a":             {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/refs":        {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/objects":     {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/a/HEAD":        {Mode: umask.Mask(fs.ModePerm), Content: []byte("a content")},
					"/repositories/a/shared-file": {Mode: umask.Mask(fs.ModePerm), Content: []byte{}},
					"/repositories/b":             {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/b/refs":        {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/b/objects":     {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/b/HEAD":        {Mode: umask.Mask(fs.ModePerm), Content: []byte("b content")},
				}

				testhelper.RequireDirectoryState(t, fs1.Root(), "", expectedDirectoryState)
				testhelper.RequireDirectoryState(t, fs2.Root(), "", expectedDirectoryState)
			},
			expectedMetrics: metricValues{
				createdSharedSnapshotCounter:   1,
				reusedSharedSnapshotCounter:    1,
				destroyedSharedSnapshotCounter: 1,
			},
		},
		{
			desc: "alternate is included in snapshot",
			run: func(t *testing.T, mgr *Manager) {
				fs1, err := mgr.GetSnapshot(ctx, []string{"repositories/c"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs1)

				testhelper.RequireDirectoryState(t, fs1.Root(), "", testhelper.DirectoryState{
					"/":                                       {Mode: fs.ModeDir | umask.Mask(perm.PrivateDir)},
					"/repositories":                           {Mode: fs.ModeDir | umask.Mask(perm.PrivateDir)},
					"/repositories/b":                         {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/b/refs":                    {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/b/objects":                 {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/b/HEAD":                    {Mode: umask.Mask(fs.ModePerm), Content: []byte("b content")},
					"/repositories/c":                         {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/c/refs":                    {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/c/objects":                 {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/c/HEAD":                    {Mode: umask.Mask(fs.ModePerm), Content: []byte("c content")},
					"/repositories/c/objects/info":            {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					"/repositories/c/objects/info/alternates": {Mode: umask.Mask(fs.ModePerm), Content: []byte("../../b/objects")},
				})
			},
			expectedMetrics: metricValues{
				createdSharedSnapshotCounter:   1,
				destroyedSharedSnapshotCounter: 1,
			},
		},
		{
			desc: "shared snaphots against the relative paths with the same LSN are shared",
			run: func(t *testing.T, mgr *Manager) {
				fs1, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs1)

				fs2, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs2)

				require.Equal(t, fs1.Root(), fs2.Root())

				mgr.SetLSN(2)

				fs3, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs3)

				fs4, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs4)

				require.Equal(t, fs3.Root(), fs4.Root())
				require.NotEqual(t, fs1.Root(), fs3.Root())
			},
			expectedMetrics: metricValues{
				createdSharedSnapshotCounter:   2,
				reusedSharedSnapshotCounter:    2,
				destroyedSharedSnapshotCounter: 2,
			},
		},
		{
			desc: "shared snaphots against different relative paths are not shared",
			run: func(t *testing.T, mgr *Manager) {
				fs1, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs1)

				fs2, err := mgr.GetSnapshot(ctx, []string{"repositories/b"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs2)

				fs3, err := mgr.GetSnapshot(ctx, []string{"repositories/a", "repositories/b"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs3)

				require.NotEqual(t, fs1.Root(), fs2.Root())
				require.NotEqual(t, fs1.Root(), fs3.Root())
				require.NotEqual(t, fs2.Root(), fs3.Root())
			},
			expectedMetrics: metricValues{
				createdSharedSnapshotCounter:   3,
				destroyedSharedSnapshotCounter: 3,
			},
		},
		{
			desc: "unused shared snapshots are removed",
			run: func(t *testing.T, mgr *Manager) {
				fs1, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, false)
				require.NoError(t, err)

				fs2, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, false)
				require.NoError(t, err)

				// Shared snaphots should be equal.
				require.Equal(t, fs1.Root(), fs2.Root())

				// Clean up the other user.
				testhelper.MustClose(t, fs2)

				fs3, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, false)
				require.NoError(t, err)

				// The first user is still there using the snapshot so it should still be there
				// and be reused for the next snapshotter.
				require.Equal(t, fs1.Root(), fs3.Root())

				// Clean both of the last users of the shared snapshot.
				testhelper.MustClose(t, fs1)
				testhelper.MustClose(t, fs3)

				fs4, err := mgr.GetSnapshot(ctx, []string{"repositories/a"}, false)
				require.NoError(t, err)
				defer testhelper.MustClose(t, fs4)

				// New snapshot was created as the previous snapshot was cleaned up due to
				// the last user being done with it.
				require.NotEqual(t, fs1.Root(), fs4.Root())
			},
			expectedMetrics: metricValues{
				createdSharedSnapshotCounter:   2,
				reusedSharedSnapshotCounter:    2,
				destroyedSharedSnapshotCounter: 2,
			},
		},
		{
			desc: "concurrently taking multiple shared snapshots",
			run: func(t *testing.T, mgr *Manager) {
				// Defer the clean snapshot clean ups at the end of the test.
				var cleanGroup errgroup.Group
				defer func() { require.NoError(t, cleanGroup.Wait()) }()

				startCleaning := make(chan struct{})
				defer close(startCleaning)

				snapshotGroup, ctx := errgroup.WithContext(ctx)
				startSnapshot := make(chan struct{})
				takeSnapshots := func(relativePath string, snapshots []FileSystem) {
					for i := 0; i < len(snapshots); i++ {
						i := i
						snapshotGroup.Go(func() error {
							<-startSnapshot
							var err error
							fs, err := mgr.GetSnapshot(ctx, []string{relativePath}, false)
							if err != nil {
								return err
							}

							snapshots[i] = fs

							cleanGroup.Go(func() error {
								<-startCleaning
								return fs.Close()
							})

							return nil
						})
					}
				}

				snapshotsA := make([]FileSystem, 20)
				takeSnapshots("repositories/a", snapshotsA)

				snapshotsB := make([]FileSystem, 20)
				takeSnapshots("repositories/b", snapshotsB)

				close(startSnapshot)
				require.NoError(t, snapshotGroup.Wait())

				// All of the snapshots taken with the same relative path should be the same.
				for _, fs := range snapshotsA {
					require.Equal(t, snapshotsA[0].Root(), fs.Root())
				}

				for _, fs := range snapshotsB {
					require.Equal(t, snapshotsB[0].Root(), fs.Root())
				}

				require.NotEqual(t, snapshotsA[0].Root(), snapshotsB[0].Root())
			},
			expectedMetrics: metricValues{
				createdSharedSnapshotCounter:   2,
				reusedSharedSnapshotCounter:    38,
				destroyedSharedSnapshotCounter: 2,
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			tmpDir := t.TempDir()
			storageDir := filepath.Join(tmpDir, "storage-dir")
			workingDir := filepath.Join(storageDir, "working-dir")

			testhelper.CreateFS(t, storageDir, fstest.MapFS{
				".":            {Mode: fs.ModeDir | fs.ModePerm},
				"working-dir":  {Mode: fs.ModeDir | fs.ModePerm},
				"repositories": {Mode: fs.ModeDir | fs.ModePerm},
				// Create enough content in the repositories to pass the repository validity check.
				"repositories/a":                         {Mode: fs.ModeDir | fs.ModePerm},
				"repositories/a/HEAD":                    {Mode: fs.ModePerm, Data: []byte("a content")},
				"repositories/a/refs":                    {Mode: fs.ModeDir | fs.ModePerm},
				"repositories/a/objects":                 {Mode: fs.ModeDir | fs.ModePerm},
				"repositories/b":                         {Mode: fs.ModeDir | fs.ModePerm},
				"repositories/b/HEAD":                    {Mode: fs.ModePerm, Data: []byte("b content")},
				"repositories/b/refs":                    {Mode: fs.ModeDir | fs.ModePerm},
				"repositories/b/objects":                 {Mode: fs.ModeDir | fs.ModePerm},
				"repositories/c/HEAD":                    {Mode: fs.ModePerm, Data: []byte("c content")},
				"repositories/c":                         {Mode: fs.ModeDir | fs.ModePerm},
				"repositories/c/refs":                    {Mode: fs.ModeDir | fs.ModePerm},
				"repositories/c/objects":                 {Mode: fs.ModeDir | fs.ModePerm},
				"repositories/c/objects/info":            {Mode: fs.ModeDir | fs.ModePerm},
				"repositories/c/objects/info/alternates": {Mode: fs.ModePerm, Data: []byte("../../b/objects")},
			})

			metrics := NewMetrics()

			mgr := NewManager(storageDir, workingDir, metrics.Scope("storage-name"))

			tc.run(t, mgr)

			require.NoError(t, testutil.CollectAndCompare(metrics, strings.NewReader(fmt.Sprintf(`
# HELP gitaly_exclusive_snapshots_created_total Number of created exclusive snapshots.
# TYPE gitaly_exclusive_snapshots_created_total counter
gitaly_exclusive_snapshots_created_total{storage="storage-name"} %d
# HELP gitaly_exclusive_snapshots_destroyed_total Number of destroyed exclusive snapshots.
# TYPE gitaly_exclusive_snapshots_destroyed_total counter
gitaly_exclusive_snapshots_destroyed_total{storage="storage-name"} %d
# HELP gitaly_shared_snapshots_created_total Number of created shared snapshots.
# TYPE gitaly_shared_snapshots_created_total counter
gitaly_shared_snapshots_created_total{storage="storage-name"} %d
# HELP gitaly_shared_snapshots_reused_total Number of reused shared snapshots.
# TYPE gitaly_shared_snapshots_reused_total counter
gitaly_shared_snapshots_reused_total{storage="storage-name"} %d
# HELP gitaly_shared_snapshots_destroyed_total Number of destroyed shared snapshots.
# TYPE gitaly_shared_snapshots_destroyed_total counter
gitaly_shared_snapshots_destroyed_total{storage="storage-name"} %d
			`,
				tc.expectedMetrics.createdExclusiveSnapshotCounter,
				tc.expectedMetrics.destroyedExclusiveSnapshotCounter,
				tc.expectedMetrics.createdSharedSnapshotCounter,
				tc.expectedMetrics.reusedSharedSnapshotCounter,
				tc.expectedMetrics.destroyedSharedSnapshotCounter,
			))))

			// All snapshots should have been cleaned up.
			testhelper.RequireDirectoryState(t, workingDir, "", testhelper.DirectoryState{
				"/": {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
			})
			require.Empty(t, mgr.sharedSnapshots)
		})
	}
}
