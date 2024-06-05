package wal

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
)

func setupTestDirectory(t *testing.T, path string) {
	require.NoError(t, os.MkdirAll(path, perm.PrivateDir))
	require.NoError(t, os.WriteFile(filepath.Join(path, "file-1"), []byte("file-1"), perm.PrivateExecutable))
	privateSubDir := filepath.Join(filepath.Join(path, "subdir-private"))
	require.NoError(t, os.Mkdir(privateSubDir, perm.PrivateDir))
	require.NoError(t, os.WriteFile(filepath.Join(privateSubDir, "file-2"), []byte("file-2"), perm.SharedFile))
	sharedSubDir := filepath.Join(path, "subdir-shared")
	require.NoError(t, os.Mkdir(sharedSubDir, perm.SharedDir))
	require.NoError(t, os.WriteFile(filepath.Join(sharedSubDir, "file-3"), []byte("file-3"), perm.PrivateFile))
}

func TestEntry(t *testing.T) {
	t.Parallel()

	storageRoot := t.TempDir()

	firstLevelDir := "test-dir"
	secondLevelDir := "second-level/test-dir"
	require.NoError(t, os.WriteFile(filepath.Join(storageRoot, "root-file"), []byte("root file"), perm.PrivateFile))
	setupTestDirectory(t, filepath.Join(storageRoot, firstLevelDir))
	setupTestDirectory(t, filepath.Join(storageRoot, secondLevelDir))

	rootDirPerm := testhelper.Umask().Mask(fs.ModePerm)

	for _, tc := range []struct {
		desc               string
		run                func(*testing.T, *Entry)
		expectedOperations operations
		expectedFiles      testhelper.DirectoryState
	}{
		{
			desc: "RecordFileCreation",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordFileCreation(
					filepath.Join(storageRoot, "root-file"),
					"test-dir/file-1",
				))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.removeDirectoryEntry("sentinel-op")
				ops.createHardLink("1", "test-dir/file-1", false)
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/":  {Mode: fs.ModeDir | rootDirPerm},
				"/1": {Mode: perm.PrivateFile, Content: []byte("root file")},
			},
		},
		{
			desc: "RecordDirectoryEntryRemoval",
			run: func(t *testing.T, entry *Entry) {
				entry.RecordDirectoryEntryRemoval("test-dir/file-1")
			},
			expectedOperations: func() operations {
				var ops operations
				ops.removeDirectoryEntry("sentinel-op")
				ops.removeDirectoryEntry("test-dir/file-1")
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/": {Mode: fs.ModeDir | rootDirPerm},
			},
		},
		{
			desc: "RecordFileUpdate on root level file",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordFileUpdate(storageRoot, "root-file"))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.removeDirectoryEntry("sentinel-op")
				ops.removeDirectoryEntry("root-file")
				ops.createHardLink("1", "root-file", false)
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/":  {Mode: fs.ModeDir | rootDirPerm},
				"/1": {Mode: perm.PrivateFile, Content: []byte("root file")},
			},
		},
		{
			desc: "RecordFileUpdate on first level file",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordFileUpdate(storageRoot, filepath.Join(firstLevelDir, "file-1")))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.removeDirectoryEntry("sentinel-op")
				ops.removeDirectoryEntry("test-dir/file-1")
				ops.createHardLink("1", "test-dir/file-1", false)
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/":  {Mode: fs.ModeDir | rootDirPerm},
				"/1": {Mode: perm.PrivateExecutable, Content: []byte("file-1")},
			},
		},
		{
			desc: "RecordDirectoryCreation on first level directory",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordDirectoryCreation(storageRoot, firstLevelDir))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.removeDirectoryEntry("sentinel-op")
				ops.createDirectory("test-dir", perm.PrivateDir)
				ops.createHardLink("1", "test-dir/file-1", false)
				ops.createDirectory("test-dir/subdir-private", perm.PrivateDir)
				ops.createHardLink("2", "test-dir/subdir-private/file-2", false)
				ops.createDirectory("test-dir/subdir-shared", perm.SharedDir)
				ops.createHardLink("3", "test-dir/subdir-shared/file-3", false)
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/":  {Mode: fs.ModeDir | rootDirPerm},
				"/1": {Mode: perm.PrivateExecutable, Content: []byte("file-1")},
				"/2": {Mode: perm.SharedFile, Content: []byte("file-2")},
				"/3": {Mode: perm.PrivateFile, Content: []byte("file-3")},
			},
		},
		{
			desc: "RecordDirectoryCreation on second level directory",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordDirectoryCreation(storageRoot, secondLevelDir))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.removeDirectoryEntry("sentinel-op")
				ops.createDirectory("second-level/test-dir", perm.PrivateDir)
				ops.createHardLink("1", "second-level/test-dir/file-1", false)
				ops.createDirectory("second-level/test-dir/subdir-private", perm.PrivateDir)
				ops.createHardLink("2", "second-level/test-dir/subdir-private/file-2", false)
				ops.createDirectory("second-level/test-dir/subdir-shared", perm.SharedDir)
				ops.createHardLink("3", "second-level/test-dir/subdir-shared/file-3", false)
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/":  {Mode: fs.ModeDir | rootDirPerm},
				"/1": {Mode: perm.PrivateExecutable, Content: []byte("file-1")},
				"/2": {Mode: perm.SharedFile, Content: []byte("file-2")},
				"/3": {Mode: perm.PrivateFile, Content: []byte("file-3")},
			},
		},
		{
			desc: "RecordDirectoryRemoval on first level directory",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordDirectoryRemoval(storageRoot, firstLevelDir))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.removeDirectoryEntry("test-dir/file-1")
				ops.removeDirectoryEntry("test-dir/subdir-private/file-2")
				ops.removeDirectoryEntry("test-dir/subdir-private")
				ops.removeDirectoryEntry("test-dir/subdir-shared/file-3")
				ops.removeDirectoryEntry("test-dir/subdir-shared")
				ops.removeDirectoryEntry("test-dir")
				ops.removeDirectoryEntry("sentinel-op")
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/": {Mode: fs.ModeDir | rootDirPerm},
			},
		},
		{
			desc: "RecordDirectoryRemoval on second level directory",
			run: func(t *testing.T, entry *Entry) {
				require.NoError(t, entry.RecordDirectoryRemoval(storageRoot, secondLevelDir))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.removeDirectoryEntry("second-level/test-dir/file-1")
				ops.removeDirectoryEntry("second-level/test-dir/subdir-private/file-2")
				ops.removeDirectoryEntry("second-level/test-dir/subdir-private")
				ops.removeDirectoryEntry("second-level/test-dir/subdir-shared/file-3")
				ops.removeDirectoryEntry("second-level/test-dir/subdir-shared")
				ops.removeDirectoryEntry("second-level/test-dir")
				ops.removeDirectoryEntry("sentinel-op")
				return ops
			}(),
			expectedFiles: testhelper.DirectoryState{
				"/": {Mode: fs.ModeDir | rootDirPerm},
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			stateDir := t.TempDir()
			entry := NewEntry(stateDir)
			entry.operations.removeDirectoryEntry("sentinel-op")

			tc.run(t, entry)

			testhelper.ProtoEqual(t, tc.expectedOperations, entry.operations)
			testhelper.RequireDirectoryState(t, stateDir, "", tc.expectedFiles)
		})
	}
}

func TestRecordAlternateUnlink(t *testing.T) {
	t.Parallel()

	createSourceHierarchy := func(tb testing.TB, path string) {
		testhelper.CreateFS(tb, path, fstest.MapFS{
			".":                      {Mode: fs.ModeDir | perm.PrivateDir},
			"objects":                {Mode: fs.ModeDir | perm.PrivateDir},
			"objects/info":           {Mode: fs.ModeDir | perm.PrivateDir},
			"objects/3f":             {Mode: fs.ModeDir | perm.PrivateDir},
			"objects/3f/1":           {Mode: perm.PrivateFile},
			"objects/3f/2":           {Mode: perm.SharedFile},
			"objects/4f":             {Mode: fs.ModeDir | perm.SharedDir},
			"objects/4f/3":           {Mode: perm.SharedFile},
			"objects/pack":           {Mode: fs.ModeDir | perm.PrivateDir},
			"objects/pack/pack.pack": {Mode: perm.PrivateFile},
			"objects/pack/pack.idx":  {Mode: perm.SharedFile},
		})
	}

	for _, tc := range []struct {
		desc               string
		createTarget       func(tb testing.TB, path string)
		expectedOperations operations
	}{
		{
			desc: "empty target",
			createTarget: func(tb testing.TB, path string) {
				require.NoError(tb, os.Mkdir(path, perm.PrivateDir))
				require.NoError(tb, os.Mkdir(filepath.Join(path, "objects"), perm.PrivateDir))
				require.NoError(tb, os.Mkdir(filepath.Join(path, "objects/pack"), perm.PrivateDir))
			},
			expectedOperations: func() operations {
				var ops operations
				ops.createDirectory("target/objects/3f", perm.PrivateDir)
				ops.createHardLink("source/objects/3f/1", "target/objects/3f/1", true)
				ops.createHardLink("source/objects/3f/2", "target/objects/3f/2", true)
				ops.createDirectory("target/objects/4f", perm.SharedDir)
				ops.createHardLink("source/objects/4f/3", "target/objects/4f/3", true)
				ops.createHardLink("source/objects/pack/pack.idx", "target/objects/pack/pack.idx", true)
				ops.createHardLink("source/objects/pack/pack.pack", "target/objects/pack/pack.pack", true)
				ops.removeDirectoryEntry("target/objects/info/alternates")
				return ops
			}(),
		},
		{
			desc: "target with some existing state",
			createTarget: func(tb testing.TB, path string) {
				testhelper.CreateFS(tb, path, fstest.MapFS{
					".":                     {Mode: fs.ModeDir | perm.PrivateDir},
					"objects":               {Mode: fs.ModeDir | perm.PrivateDir},
					"objects/3f":            {Mode: fs.ModeDir | perm.PrivateDir},
					"objects/3f/1":          {Mode: perm.PrivateFile},
					"objects/4f":            {Mode: fs.ModeDir | perm.SharedDir},
					"objects/4f/3":          {Mode: perm.SharedFile},
					"objects/pack":          {Mode: fs.ModeDir | perm.PrivateDir},
					"objects/pack/pack.idx": {Mode: perm.SharedFile},
				})
			},
			expectedOperations: func() operations {
				var ops operations
				ops.createHardLink("source/objects/3f/2", "target/objects/3f/2", true)
				ops.createHardLink("source/objects/pack/pack.pack", "target/objects/pack/pack.pack", true)
				ops.removeDirectoryEntry("target/objects/info/alternates")
				return ops
			}(),
		},
		{
			desc:         "target with fully matching object state",
			createTarget: createSourceHierarchy,
			expectedOperations: func() operations {
				var ops operations
				ops.removeDirectoryEntry("target/objects/info/alternates")
				return ops
			}(),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			storageRoot := t.TempDir()
			createSourceHierarchy(t, filepath.Join(storageRoot, "source"))

			tc.createTarget(t, filepath.Join(storageRoot, "target"))

			stateDirectory := t.TempDir()
			entry := NewEntry(stateDirectory)
			require.NoError(t, entry.RecordAlternateUnlink(storageRoot, "target", "../../source/objects"))

			testhelper.ProtoEqual(t, tc.expectedOperations, entry.operations)
		})
	}
}

func TestRecordReferenceUpdates(t *testing.T) {
	testhelper.SkipWithReftable(t, "TransactionManager doesn't support reftables yet. See: https://gitlab.com/gitlab-org/gitaly/-/issues/5867")

	t.Parallel()

	type referenceTransaction map[git.ReferenceName]git.ObjectID

	referenceTransactionToProto := func(refTX referenceTransaction) *gitalypb.LogEntry_ReferenceTransaction {
		var protoRefTX gitalypb.LogEntry_ReferenceTransaction
		for reference, newOID := range refTX {
			protoRefTX.Changes = append(protoRefTX.Changes, &gitalypb.LogEntry_ReferenceTransaction_Change{
				ReferenceName: []byte(reference),
				NewOid:        []byte(newOID),
			})
		}
		return &protoRefTX
	}

	performChanges := func(t *testing.T, updater *updateref.Updater, changes []*gitalypb.LogEntry_ReferenceTransaction_Change) {
		t.Helper()

		require.NoError(t, updater.Start())
		for _, change := range changes {
			require.NoError(t,
				updater.Update(
					git.ReferenceName(change.ReferenceName),
					git.ObjectID(change.NewOid),
					"",
				),
			)
		}
		require.NoError(t, updater.Commit())
	}

	umask := testhelper.Umask()

	type setupData struct {
		existingReferences    referenceTransaction
		referenceTransactions []referenceTransaction
		expectedOperations    operations
		expectedDirectory     testhelper.DirectoryState
		expectedError         error
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T, oids []git.ObjectID) setupData
	}{
		{
			desc: "empty transaction",
			setup: func(t *testing.T, oids []git.ObjectID) setupData {
				return setupData{
					referenceTransactions: []referenceTransaction{{}},
					expectedDirectory: testhelper.DirectoryState{
						"/": {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					},
				}
			},
		},
		{
			desc: "various references created",
			setup: func(t *testing.T, oids []git.ObjectID) setupData {
				return setupData{
					referenceTransactions: []referenceTransaction{
						{"refs/heads/branch-1": oids[0]},
						{"refs/heads/branch-2": oids[1]},
						{"refs/heads/subdir/branch-3": oids[2]},
						{"refs/heads/subdir/branch-4": oids[3]},
						{"refs/heads/subdir/no-refs/branch-5": oids[4]},
					},
					expectedOperations: func() operations {
						var ops operations
						ops.createHardLink("1", "relative-path/refs/heads/branch-1", false)
						ops.createHardLink("2", "relative-path/refs/heads/branch-2", false)
						ops.createDirectory("relative-path/refs/heads/subdir", umask.Mask(fs.ModePerm))
						ops.createHardLink("3", "relative-path/refs/heads/subdir/branch-3", false)
						ops.createHardLink("4", "relative-path/refs/heads/subdir/branch-4", false)
						ops.createDirectory("relative-path/refs/heads/subdir/no-refs", umask.Mask(fs.ModePerm))
						ops.createHardLink("5", "relative-path/refs/heads/subdir/no-refs/branch-5", false)
						return ops
					}(),
					expectedDirectory: testhelper.DirectoryState{
						"/":  {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
						"/1": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[0] + "\n")},
						"/2": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[1] + "\n")},
						"/3": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[2] + "\n")},
						"/4": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[3] + "\n")},
						"/5": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[4] + "\n")},
					},
				}
			},
		},
		{
			desc: "heads and tags remain empty",
			setup: func(t *testing.T, oids []git.ObjectID) setupData {
				return setupData{
					existingReferences: referenceTransaction{
						"refs/heads/branch-1": oids[0],
						"refs/tags/tag-1":     oids[1],
					},
					referenceTransactions: []referenceTransaction{
						{
							"refs/heads/branch-1": gittest.DefaultObjectHash.ZeroOID,
							"refs/tags/tag-1":     gittest.DefaultObjectHash.ZeroOID,
						},
					},
					expectedOperations: func() operations {
						var ops operations
						// Git does not remove the heads and tags directories even if they are empty.
						// Since we just record what Git is doing, we don't remove them either.
						ops.removeDirectoryEntry("relative-path/refs/heads/branch-1")
						ops.removeDirectoryEntry("relative-path/refs/tags/tag-1")
						return ops
					}(),
					expectedDirectory: testhelper.DirectoryState{
						"/": {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					},
				}
			},
		},
		{
			desc: "various references changes",
			setup: func(t *testing.T, oids []git.ObjectID) setupData {
				return setupData{
					existingReferences: referenceTransaction{
						"refs/heads/branch-1":                    oids[0],
						"refs/heads/branch-2":                    oids[0],
						"refs/heads/subdir/branch-3":             oids[0],
						"refs/heads/subdir/branch-4":             oids[0],
						"refs/heads/subdir/secondlevel/branch-5": oids[0],
					},
					referenceTransactions: []referenceTransaction{
						{
							"refs/heads/branch-1":                    gittest.DefaultObjectHash.ZeroOID,
							"refs/heads/branch-2":                    oids[1],
							"refs/heads/branch-6":                    oids[2],
							"refs/heads/subdir/branch-3":             oids[1],
							"refs/heads/subdir/branch-4":             gittest.DefaultObjectHash.ZeroOID,
							"refs/heads/subdir/branch-7":             oids[2],
							"refs/heads/subdir/secondlevel/branch-5": gittest.DefaultObjectHash.ZeroOID,
							"refs/heads/subdir/secondlevel/branch-8": oids[3],
						},
					},
					expectedOperations: func() operations {
						var ops operations
						ops.removeDirectoryEntry("relative-path/refs/heads/branch-2")
						ops.createHardLink("1", "relative-path/refs/heads/branch-2", false)
						ops.createHardLink("2", "relative-path/refs/heads/branch-6", false)
						ops.removeDirectoryEntry("relative-path/refs/heads/subdir/branch-3")
						ops.createHardLink("3", "relative-path/refs/heads/subdir/branch-3", false)
						ops.createHardLink("4", "relative-path/refs/heads/subdir/branch-7", false)
						ops.createHardLink("5", "relative-path/refs/heads/subdir/secondlevel/branch-8", false)
						ops.removeDirectoryEntry("relative-path/refs/heads/branch-1")
						ops.removeDirectoryEntry("relative-path/refs/heads/subdir/branch-4")
						ops.removeDirectoryEntry("relative-path/refs/heads/subdir/secondlevel/branch-5")
						return ops
					}(),
					expectedDirectory: testhelper.DirectoryState{
						"/":  {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
						"/1": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[1] + "\n")},
						"/2": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[2] + "\n")},
						"/3": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[1] + "\n")},
						"/4": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[2] + "\n")},
						"/5": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[3] + "\n")},
					},
				}
			},
		},
		{
			desc: "delete references",
			setup: func(t *testing.T, oids []git.ObjectID) setupData {
				return setupData{
					existingReferences: referenceTransaction{
						"refs/heads/branch-1":                oids[0],
						"refs/heads/branch-2":                oids[1],
						"refs/heads/subdir/branch-3":         oids[2],
						"refs/heads/subdir/branch-4":         oids[3],
						"refs/heads/subdir/no-refs/branch-5": oids[4],
					},
					referenceTransactions: []referenceTransaction{
						{
							"refs/heads/branch-1":        gittest.DefaultObjectHash.ZeroOID,
							"refs/heads/branch-2":        gittest.DefaultObjectHash.ZeroOID,
							"refs/heads/subdir/branch-3": gittest.DefaultObjectHash.ZeroOID,
							// "refs/heads/subdir/branch-4" is not deleted so we expect the directory
							// to not be deleted.
							"refs/heads/subdir/no-refs/branch-5": gittest.DefaultObjectHash.ZeroOID,
						},
					},
					expectedOperations: func() operations {
						var ops operations
						ops.removeDirectoryEntry("relative-path/refs/heads/branch-1")
						ops.removeDirectoryEntry("relative-path/refs/heads/branch-2")
						ops.removeDirectoryEntry("relative-path/refs/heads/subdir/branch-3")
						ops.removeDirectoryEntry("relative-path/refs/heads/subdir/no-refs/branch-5")
						ops.removeDirectoryEntry("relative-path/refs/heads/subdir/no-refs")
						return ops
					}(),
					expectedDirectory: testhelper.DirectoryState{
						"/": {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
					},
				}
			},
		},
		{
			desc: "directory-file conflict resolved",
			setup: func(t *testing.T, oids []git.ObjectID) setupData {
				return setupData{
					existingReferences: referenceTransaction{
						"refs/heads/parent": oids[0],
					},
					referenceTransactions: []referenceTransaction{
						{
							"refs/heads/parent": gittest.DefaultObjectHash.ZeroOID,
						},
						{
							"refs/heads/branch-1":               oids[0],
							"refs/heads/parent/branch-2":        oids[1],
							"refs/heads/parent/subdir/branch-3": oids[2],
						},
					},
					expectedOperations: func() operations {
						var ops operations
						ops.removeDirectoryEntry("relative-path/refs/heads/parent")
						ops.createHardLink("1", "relative-path/refs/heads/branch-1", false)
						ops.createDirectory("relative-path/refs/heads/parent", umask.Mask(fs.ModePerm))
						ops.createHardLink("2", "relative-path/refs/heads/parent/branch-2", false)
						ops.createDirectory("relative-path/refs/heads/parent/subdir", umask.Mask(fs.ModePerm))
						ops.createHardLink("3", "relative-path/refs/heads/parent/subdir/branch-3", false)
						return ops
					}(),
					expectedDirectory: testhelper.DirectoryState{
						"/":  {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
						"/1": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[0] + "\n")},
						"/2": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[1] + "\n")},
						"/3": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[2] + "\n")},
					},
				}
			},
		},
		{
			desc: "directory-file conflict",
			setup: func(t *testing.T, oids []git.ObjectID) setupData {
				return setupData{
					existingReferences: referenceTransaction{
						"refs/heads/parent": oids[0],
					},
					referenceTransactions: []referenceTransaction{
						{
							"refs/heads/parent/branch-1": oids[0],
						},
					},
					expectedError: updateref.FileDirectoryConflictError{
						ExistingReferenceName:    "refs/heads/parent",
						ConflictingReferenceName: "refs/heads/parent/branch-1",
					},
				}
			},
		},
		{
			desc: "deletion creates a directory",
			setup: func(t *testing.T, oids []git.ObjectID) setupData {
				return setupData{
					referenceTransactions: []referenceTransaction{
						{
							"refs/remotes/upstream/deleted-branch": gittest.DefaultObjectHash.ZeroOID,
						},
						{
							"refs/remotes/upstream/created-branch": oids[0],
						},
					},
					expectedOperations: func() operations {
						var ops operations
						// refs/remotes does not exist in the repository at the beginning of the test.
						// The deletion performed however creates it. As Git has special cased the refs/remotes
						// directory, it doesn't delete it unlike the `refs/remotes/upstream`.
						//
						// We assert here the directory created by the deletion is properly logged to ensure
						// it exists when we attempt to create the child directory.
						//
						// BUG: We're currently not logging the creation of the 'refs/remotes' directory.
						ops.createDirectory("relative-path/refs/remotes/upstream", umask.Mask(fs.ModePerm))
						ops.createHardLink("1", "relative-path/refs/remotes/upstream/created-branch", false)
						return ops
					}(),
					expectedDirectory: testhelper.DirectoryState{
						"/":  {Mode: fs.ModeDir | umask.Mask(fs.ModePerm)},
						"/1": {Mode: umask.Mask(perm.PublicFile), Content: []byte(oids[0] + "\n")},
					},
				}
			},
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			ctx := testhelper.Context(t)

			cfg := testcfg.Build(t)
			storageRoot := cfg.Storages[0].Path
			snapshotPrefix := "snapshot"
			relativePath := "relative-path"
			require.NoError(t, os.Mkdir(filepath.Join(storageRoot, snapshotPrefix), perm.PrivateDir))

			_, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
				RelativePath:           filepath.Join(snapshotPrefix, relativePath),
			})

			commitIDs := make([]git.ObjectID, 5)
			for i := range commitIDs {
				commitIDs[i] = gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage(fmt.Sprintf("commit-%d", i)))
			}

			updater, err := updateref.New(ctx, gittest.NewRepositoryPathExecutor(t, cfg, repoPath))
			require.NoError(t, err)
			defer testhelper.MustClose(t, updater)

			setupData := tc.setup(t, commitIDs)

			performChanges(t, updater, referenceTransactionToProto(setupData.existingReferences).Changes)

			stateDir := t.TempDir()
			entry := NewEntry(stateDir)
			for _, refTX := range setupData.referenceTransactions {
				if err := entry.RecordReferenceUpdates(ctx, storageRoot, snapshotPrefix, relativePath, referenceTransactionToProto(refTX), gittest.DefaultObjectHash, func(changes []*gitalypb.LogEntry_ReferenceTransaction_Change) error {
					performChanges(t, updater, changes)
					return nil
				}); err != nil {
					require.ErrorIs(t, err, setupData.expectedError)
					return
				}
			}

			require.Nil(t, setupData.expectedError)
			testhelper.ProtoEqual(t, setupData.expectedOperations, entry.operations)
			testhelper.RequireDirectoryState(t, stateDir, "", setupData.expectedDirectory)
		})
	}
}
