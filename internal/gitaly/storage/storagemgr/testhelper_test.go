package storagemgr

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/dgraph-io/badger/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/packfile"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/repoutil"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/counter"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/proto"
)

func TestMain(m *testing.M) {
	testhelper.Run(m)
}

// RepositoryState describes the full asserted state of a repository.
type RepositoryState struct {
	// DefaultBranch is the expected refname that HEAD points to.
	DefaultBranch git.ReferenceName
	// CustomHooks is the expected state of the custom hooks.
	CustomHooks testhelper.DirectoryState
	// Objects are the objects that are expected to exist.
	Objects []git.ObjectID
	// Alternate is the content of 'objects/info/alternates'.
	Alternate string
	// References is the references that should exist, including ones in the packed-refs file and loose references.
	References *ReferencesState
	// Packfiles provides a more detailed object assertion in a repository. It allows us to examine the list of
	// packfiles, loose objects, invisible objects, index files, etc. Most test cases don't need to be concerned
	// with Packfile structure, though. So, we keep both, but Packfiles takes higher precedence.
	Packfiles *PackfilesState
}

// ReferencesState describes the asserted state of packed-refs and loose references. It's mostly used for verifying
// pack-refs housekeeping task.
type ReferencesState struct {
	// PackedReferences is the content of pack-refs file, line by line
	PackedReferences map[git.ReferenceName]git.ObjectID
	// LooseReferences is the exact list of loose references outside packed-refs.
	LooseReferences map[git.ReferenceName]git.ObjectID
}

// PackfilesState describes the asserted state of all packfiles and common multi-pack-index.
type PackfilesState struct {
	// LooseObjects contain the list of objects outside packfiles. Although the transaction manager cleans them up
	// eventually, loose objects should be handled during migration.
	LooseObjects []git.ObjectID
	// Packfiles is the hash map of name and corresponding packfile state assertion.
	Packfiles []*PackfileState
	// PooledObjects contain the list of pooled objects member repositories can see from the pool.
	PooledObjects []git.ObjectID
	// HasMultiPackIndex asserts whether there is a multi-pack-index inside the objects/pack directory.
	HasMultiPackIndex bool
	// HasCommitGraphs assert whether the repository has valid commit graphs.
	HasCommitGraphs bool
}

// PackfileState describes the asserted state of an individual packfile, including its contained objects, index, bitmap.
type PackfileState struct {
	// Objects asserts the list of objects this packfile contains.
	Objects []git.ObjectID
	// HasBitmap asserts whether the packfile includes a corresponding bitmap index (*.bitmap).
	HasBitmap bool
	// HasReverseIndex asserts whether the packfile includes a corresponding reverse index (*.rev).
	HasReverseIndex bool
}

// sortObjects sorts objects lexically by their oid.
func sortObjects(objects []git.ObjectID) {
	sort.Slice(objects, func(i, j int) bool {
		return objects[i] < objects[j]
	})
}

// sortPackfiles sorts the list of packfiles by their contained objects. Each packfile has a static hash. This hash is
// different between object formats and subject to change in the future. So, to make the test deterministic, we sort
// packfile states by their contained objects. This function is called before asserting packfile structures. It should
// be fine as soon as the set of objects the object distribution in packfiles are the same. Of course, we'll need to
// verify midx, which depends on packfile names, in another assertion.
func sortPackfilesState(packfilesState *PackfilesState) {
	if packfilesState == nil || len(packfilesState.Packfiles) <= 1 {
		return
	}
	// The sort key is re-compute in each sort loop. For testing purpose, which involves a handufl of objects and
	// packfiles, it's still efficient enough.
	concatObjects := func(oids []git.ObjectID) string {
		var result strings.Builder
		for _, oid := range oids {
			result.Write([]byte(oid))
		}
		return result.String()
	}
	sort.Slice(packfilesState.Packfiles, func(i, j int) bool {
		return concatObjects(packfilesState.Packfiles[i].Objects) < concatObjects(packfilesState.Packfiles[j].Objects)
	})
}

// RequireRepositoryState asserts the given repository matches the expected state.
func RequireRepositoryState(tb testing.TB, ctx context.Context, cfg config.Cfg, repo *localrepo.Repo, expected RepositoryState) {
	tb.Helper()

	repoPath, err := repo.Path()
	require.NoError(tb, err)

	headReference, err := repo.HeadReference(ctx)
	require.NoError(tb, err)

	actualReferencesState, err := collectReferencesState(tb, expected, repoPath)
	require.NoError(tb, err)

	// Verify if the combination of packed-refs and loose refs match the point of view of Git.
	referencesFromFile := map[git.ReferenceName]git.ObjectID{}
	if actualReferencesState != nil {
		for name, oid := range actualReferencesState.PackedReferences {
			referencesFromFile[name] = oid
		}
		for name, oid := range actualReferencesState.LooseReferences {
			referencesFromFile[name] = oid
		}
	}

	actualGitReferences := map[git.ReferenceName]git.ObjectID{}
	gitReferences, err := repo.GetReferences(ctx)
	require.NoError(tb, err)
	for _, ref := range gitReferences {
		actualGitReferences[ref.Name] = git.ObjectID(ref.Target)
	}
	require.Equalf(tb, referencesFromFile, actualGitReferences, "references perceived by Git don't match the ones in loose reference and packed-refs file")

	// Assert if there is any empty directory in the refs hierarchy excepts for heads and tags
	rootRefsDir := filepath.Join(repoPath, "refs")
	ignoredDirs := map[string]struct{}{
		rootRefsDir:                         {},
		filepath.Join(rootRefsDir, "heads"): {},
		filepath.Join(rootRefsDir, "tags"):  {},
	}
	require.NoError(tb, filepath.WalkDir(rootRefsDir, func(path string, entry fs.DirEntry, err error) error {
		if entry.IsDir() {
			if _, exist := ignoredDirs[path]; !exist {
				isEmpty, err := isDirEmpty(path)
				require.NoError(tb, err)
				require.Falsef(tb, isEmpty, "there shouldn't be any empty directory in the refs hierarchy %s", path)
			}
		}
		return nil
	}))

	var actualPackfiles, expectedPackfiles *PackfilesState

	expectedObjects := []git.ObjectID{}
	// If Packfiles assertion exists, we collect all objects from packfiles and loose objects. This list will be
	// compared with the actual list of objects gathered by `git-cat-file(1)`. This is to ensure on-disk structure
	// matches Git's perspective.
	if expected.Packfiles != nil {
		expectedPackfiles = expected.Packfiles

		for _, packfile := range expected.Packfiles.Packfiles {
			sortObjects(packfile.Objects)
			expectedObjects = append(expectedObjects, packfile.Objects...)
		}
		sortObjects(expectedPackfiles.LooseObjects)
		expectedObjects = append(expectedObjects, expectedPackfiles.LooseObjects...)

		sortObjects(expectedPackfiles.PooledObjects)
		expectedObjects = append(expectedObjects, expectedPackfiles.PooledObjects...)
		expectedPackfiles.PooledObjects = nil

		objectHash, err := repo.ObjectHash(ctx)
		require.NoError(tb, err)

		actualPackfiles = collectPackfilesState(tb, repoPath, cfg, objectHash)
		sortObjects(actualPackfiles.LooseObjects)
		for _, packfile := range actualPackfiles.Packfiles {
			sortObjects(packfile.Objects)
		}
	} else if expected.Objects != nil {
		expectedObjects = expected.Objects
	}
	sortObjects(expectedObjects)

	actualObjects := gittest.ListObjects(tb, cfg, repoPath)
	sortObjects(actualObjects)

	alternate, err := os.ReadFile(stats.AlternatesFilePath(repoPath))
	if err != nil {
		require.ErrorIs(tb, err, fs.ErrNotExist)
	}

	sortPackfilesState(expectedPackfiles)
	sortPackfilesState(actualPackfiles)

	require.Equal(tb,
		RepositoryState{
			DefaultBranch: expected.DefaultBranch,
			Objects:       expectedObjects,
			Alternate:     expected.Alternate,
			References:    expected.References,
			Packfiles:     expectedPackfiles,
		},
		RepositoryState{
			DefaultBranch: headReference,
			Objects:       actualObjects,
			Alternate:     string(alternate),
			References:    actualReferencesState,
			Packfiles:     actualPackfiles,
		},
	)
	testhelper.RequireDirectoryState(tb, filepath.Join(repoPath, repoutil.CustomHooksDir), "", expected.CustomHooks)
}

func collectReferencesState(tb testing.TB, expected RepositoryState, repoPath string) (*ReferencesState, error) {
	if expected.References == nil {
		return nil, nil
	}

	packRefsFile, err := os.ReadFile(filepath.Join(repoPath, "packed-refs"))
	if errors.Is(err, os.ErrNotExist) {
		// Treat missing packed-refs file as empty.
		packRefsFile = nil
	} else {
		require.NoError(tb, err)
	}

	// Parse packed-refs file
	var packedReferences map[git.ReferenceName]git.ObjectID
	if len(packRefsFile) != 0 {
		packedReferences = make(map[git.ReferenceName]git.ObjectID)
		lines := strings.Split(string(packRefsFile), "\n")
		require.Equalf(tb, strings.TrimSpace(lines[0]), "# pack-refs with: peeled fully-peeled sorted", "invalid packed-refs header")
		lines = lines[1:]
		for _, line := range lines {
			line = strings.TrimSpace(line)

			if len(line) == 0 {
				continue
			}

			// We don't care about the peeled object ID.
			if strings.HasPrefix(line, "^") {
				continue
			}

			parts := strings.Split(line, " ")
			require.Equalf(tb, 2, len(parts), "invalid packed-refs format: %q", line)
			packedReferences[git.ReferenceName(parts[1])] = git.ObjectID(parts[0])
		}
	}

	// Walk and collect loose refs.
	looseReferences := map[git.ReferenceName]git.ObjectID{}
	refsPath := filepath.Join(repoPath, "refs")
	require.NoError(tb, filepath.WalkDir(refsPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			ref, err := filepath.Rel(repoPath, path)
			if err != nil {
				return fmt.Errorf("extracting ref name: %w", err)
			}
			oid, err := os.ReadFile(path)
			require.NoError(tb, err)

			looseReferences[git.ReferenceName(ref)] = git.ObjectID(strings.TrimSpace(string(oid)))
		}
		return nil
	}))

	return &ReferencesState{
		PackedReferences: packedReferences,
		LooseReferences:  looseReferences,
	}, nil
}

func collectPackfilesState(tb testing.TB, repoPath string, cfg config.Cfg, objectHash git.ObjectHash) *PackfilesState {
	state := &PackfilesState{}

	objectsDir := filepath.Join(repoPath, "objects")

	looseObjects, err := filepath.Glob(filepath.Join(objectsDir, "??/*"))
	require.NoError(tb, err)
	for _, obj := range looseObjects {
		relPath, err := filepath.Rel(objectsDir, obj)
		require.NoError(tb, err)

		parts := strings.Split(relPath, "/")
		require.Equalf(tb, 2, len(parts), "invalid loose object path %q", relPath)

		state.LooseObjects = append(state.LooseObjects, git.ObjectID(fmt.Sprintf("%s%s", parts[0], parts[1])))
	}

	packDir := filepath.Join(objectsDir, "pack")

	entries, err := os.ReadDir(filepath.Join(packDir))
	require.NoError(tb, err)

	state.Packfiles = []*PackfileState{}
	for _, file := range entries {
		indexPath := filepath.Join(objectsDir, "pack", file.Name())
		ext := filepath.Ext(file.Name())
		if ext != ".idx" {
			continue
		}
		packName := strings.TrimSuffix(file.Name(), ext)
		packfileState := &PackfileState{}

		// Run git-verify-pack(1) to ensure the packfil is well functioning.
		gittest.Exec(tb, cfg, "-C", repoPath, "verify-pack", indexPath)

		// Parse index to collect list of objects.
		idxContent, err := os.ReadFile(indexPath)
		require.NoError(tb, err)

		var stdout bytes.Buffer
		gittest.ExecOpts(tb, cfg, gittest.ExecConfig{
			Stdin:  strings.NewReader(string(idxContent)),
			Stdout: &stdout,
		}, "show-index", "--object-format="+objectHash.Format)
		require.NoError(tb, packfile.ParseIndex(&stdout, func(obj *packfile.Object) {
			packfileState.Objects = append(packfileState.Objects, git.ObjectID(obj.OID))
		}))

		// Check other index files exist.
		if _, err := os.Stat(filepath.Join(packDir, packName+".bitmap")); err == nil {
			packfileState.HasBitmap = true
		} else if !errors.Is(err, os.ErrNotExist) {
			require.NoError(tb, err)
		}

		if _, err := os.Stat(filepath.Join(packDir, packName+".rev")); err == nil {
			packfileState.HasReverseIndex = true
		} else if !errors.Is(err, os.ErrNotExist) {
			require.NoError(tb, err)
		}
		state.Packfiles = append(state.Packfiles, packfileState)
	}
	midxPath := filepath.Join(packDir, "multi-pack-index")
	if _, err := os.Stat(midxPath); err == nil {
		state.HasMultiPackIndex = true
		_, err := stats.MultiPackIndexInfoForPath(midxPath)
		require.NoError(tb, err)

		gittest.Exec(tb, cfg, "-C", repoPath, "multi-pack-index", "verify")
	} else if !errors.Is(err, os.ErrNotExist) {
		require.NoError(tb, err)
	}

	info, err := stats.CommitGraphInfoForRepository(repoPath)
	require.NoError(tb, err)
	state.HasCommitGraphs = info.Exists

	return state
}

type repositoryBuilder func(relativePath string) *localrepo.Repo

// RepositoryStates describes the state of repositories in a storage. The key is the relative path of a repository that
// is expected to exist and the value the expected state contained in the repository.
type RepositoryStates map[string]RepositoryState

// RequireRepositories finds all Git repositories in the given storage path. It asserts the set of existing repositories match the expected set
// and that all of the repositories contain the expected state.
func RequireRepositories(tb testing.TB, ctx context.Context, cfg config.Cfg, storagePath string, buildRepository repositoryBuilder, expected RepositoryStates) {
	tb.Helper()

	var actualRelativePaths []string
	require.NoError(tb, filepath.WalkDir(storagePath, func(path string, d fs.DirEntry, err error) error {
		require.NoError(tb, err)

		if !d.IsDir() {
			return nil
		}

		if err := storage.ValidateGitDirectory(path); err != nil {
			require.ErrorAs(tb, err, &storage.InvalidGitDirectoryError{})
			return nil
		}

		relativePath, err := filepath.Rel(storagePath, path)
		require.NoError(tb, err)

		actualRelativePaths = append(actualRelativePaths, relativePath)
		return nil
	}))

	var expectedRelativePaths []string
	for relativePath := range expected {
		expectedRelativePaths = append(expectedRelativePaths, relativePath)
	}

	// Sort the slices instead of using ElementsMatch so the state assertions are always done in the
	// same order as well.
	sort.Strings(actualRelativePaths)
	sort.Strings(expectedRelativePaths)

	require.Equal(tb, expectedRelativePaths, actualRelativePaths,
		"expected and actual set of repositories in the storage don't match")

	for _, relativePath := range expectedRelativePaths {
		func() {
			defer func() {
				// RequireRepositoryState works within a repository and doesn't thus print out the
				// relative path of the repository that failed. If the call failed the test,
				// print out the relative path to ease troubleshooting.
				if tb.Failed() {
					require.Failf(tb, "unexpected repository state", "relative path: %q", relativePath)
				}
			}()

			RequireRepositoryState(tb, ctx, cfg, buildRepository(relativePath), expected[relativePath])
		}()
	}
}

// DatabaseState describes the expected state of the key-value store. The keys in the map are the expected keys
// in the database and the values are the expected unmarshaled values.
type DatabaseState map[string]proto.Message

// RequireDatabase asserts the actual database state matches the expected database state. The actual values in the
// database are unmarshaled to the same type the values have in the expected database state.
func RequireDatabase(tb testing.TB, ctx context.Context, database Database, expectedState DatabaseState) {
	tb.Helper()

	if expectedState == nil {
		expectedState = DatabaseState{}
	}

	actualState := DatabaseState{}
	unexpectedKeys := []string{}
	require.NoError(tb, database.View(func(txn DatabaseTransaction) error {
		iterator := txn.NewIterator(badger.DefaultIteratorOptions)
		defer iterator.Close()

		for iterator.Rewind(); iterator.Valid(); iterator.Next() {
			key := iterator.Item().Key()
			expectedValue, ok := expectedState[string(key)]
			if !ok {
				// Print the keys out escaped as otherwise the non-printing characters are not visible in the assertion failure.
				unexpectedKeys = append(unexpectedKeys, fmt.Sprintf("%q", key))
				continue
			}

			require.NoError(tb, iterator.Item().Value(func(value []byte) error {
				// Unmarshal the actual value to the same type as the expected value.
				actualValue := reflect.New(reflect.TypeOf(expectedValue).Elem()).Interface().(proto.Message)
				require.NoError(tb, proto.Unmarshal(value, actualValue))
				actualState[string(key)] = actualValue
				return nil
			}))
		}

		return nil
	}))

	require.Empty(tb, unexpectedKeys, "database contains unexpected keys")
	testhelper.ProtoEqual(tb, expectedState, actualState)
}

type testTransactionCommit struct {
	OID  git.ObjectID
	Pack []byte
}

type testTransactionTag struct {
	Name string
	OID  git.ObjectID
}

type testTransactionCommits struct {
	First       testTransactionCommit
	Second      testTransactionCommit
	Third       testTransactionCommit
	Diverging   testTransactionCommit
	Orphan      testTransactionCommit
	Unreachable testTransactionCommit
}

type testTransactionSetup struct {
	PartitionID       partitionID
	RelativePath      string
	RepositoryPath    string
	Repo              *localrepo.Repo
	Config            config.Cfg
	CommandFactory    git.CommandFactory
	RepositoryFactory localrepo.Factory
	ObjectHash        git.ObjectHash
	NonExistentOID    git.ObjectID
	Commits           testTransactionCommits
	AnnotatedTags     []testTransactionTag
}

type testTransactionHooks struct {
	// BeforeApplyLogEntry is called before a log entry is applied to the repository.
	BeforeApplyLogEntry hookFunc
	// BeforeAppendLogEntry is called before a log entry is appended to the log.
	BeforeAppendLogEntry hookFunc
	// AfterDeleteLogEntry is called after a log entry is deleted.
	AfterDeleteLogEntry hookFunc
	// BeforeReadAppliedLSN is invoked before the applied LSN is read.
	BeforeReadAppliedLSN hookFunc
	// BeforeStoreAppliedLSN is invoked before the applied LSN is stored.
	BeforeStoreAppliedLSN hookFunc
	// WaitForTransactionsWhenClosing waits for a in-flight to finish before returning
	// from Run.
	WaitForTransactionsWhenClosing bool
}

// StartManager starts a TransactionManager.
type StartManager struct {
	// Hooks contains the hook functions that are configured on the TransactionManager. These allow
	// for better synchronization.
	Hooks testTransactionHooks
	// ExpectedError is the expected error to be raised from the manager's Run. Panics are converted
	// to errors and asserted to match this as well.
	ExpectedError error
	// ModifyStorage allows modifying the storage prior to the manager starting. This
	// may be necessary to test some states that can be reached from hard crashes
	// but not during the tests.
	ModifyStorage func(tb testing.TB, cfg config.Cfg, storagePath string)
}

// CloseManager closes a TransactionManager.
type CloseManager struct{}

// AssertManager asserts whether the manager has closed and Run returned. If it has, it asserts the
// error matched the expected. If the manager has exited with an error, AssertManager must be called
// or the test case fails.
type AssertManager struct {
	// ExpectedError is the error TransactionManager's Run method is expected to return.
	ExpectedError error
}

// Begin calls Begin on the TransactionManager to start a new transaction.
type Begin struct {
	// TransactionID is the identifier given to the transaction created. This is used to identify
	// the transaction in later steps.
	TransactionID int
	// RelativePath is the relative path of the repository this transaction is operating on.
	RelativePath string
	// SnapshottedRelativePaths are the relative paths of the repositories to include in the snapshot
	// in addition to the target repository.
	SnapshottedRelativePaths []string
	// ReadOnly indicates whether this is a read-only transaction.
	ReadOnly bool
	// Context is the context to use for the Begin call.
	Context context.Context
	// ExpectedSnapshot is the expected LSN this transaction should read the repsoitory's state at.
	ExpectedSnapshotLSN LSN
	// ExpectedError is the error expected to be returned from the Begin call.
	ExpectedError error
}

// CreateRepository creates the transaction's repository..
type CreateRepository struct {
	// TransactionID is the transaction for which to create the repository.
	TransactionID int
	// DefaultBranch is the default branch to set in the repository.
	DefaultBranch git.ReferenceName
	// References are the references to create in the repository.
	References map[git.ReferenceName]git.ObjectID
	// Packs are the objects that are written into the repository.
	Packs [][]byte
	// CustomHooks are the custom hooks to write into the repository.
	CustomHooks []byte
	// Alternate links the given relative path as the repository's alternate.
	Alternate string
}

// RunPackRefs calls pack-refs housekeeping task on a transaction.
type RunPackRefs struct {
	// TransactionID is the transaction for which the pack-refs task runs.
	TransactionID int
}

// RunRepack calls repack housekeeping task on a transaction.
type RunRepack struct {
	// TransactionID is the transaction for which the repack task runs.
	TransactionID int
	// Config is the desired repacking config for the task.
	Config housekeeping.RepackObjectsConfig
}

// CustomHooksUpdate models an update to the custom hooks.
type CustomHooksUpdate struct {
	// CustomHooksTAR contains the custom hooks as a TAR. The TAR contains a `custom_hooks`
	// directory which contains the hooks. Setting the update with nil `custom_hooks_tar` clears
	// the hooks from the repository.
	CustomHooksTAR []byte
}

// DefaultBranchUpdate provides the information to update the default branch of the repo.
type DefaultBranchUpdate struct {
	// Reference is the reference to update the default branch to.
	Reference git.ReferenceName
}

// alternateUpdate models an update to the repository's alternates file.
type alternateUpdate struct {
	// content is the content to write in the 'objects/info/alternates' file. If empty, the file
	// is removed instead.
	content string
}

// Commit calls Commit on a transaction.
type Commit struct {
	// TransactionID identifies the transaction to commit.
	TransactionID int
	// Context is the context to use for the Commit call.
	Context context.Context
	// ExpectedError is the error that is expected to be returned when committing the transaction.
	// If ExpectedError is a function with signature func(tb testing.TB, actualErr error), it is
	// run instead of asserting the error.
	ExpectedError any

	// SkipVerificationFailures sets the verification failure handling for this commit.
	SkipVerificationFailures bool
	// ReferenceUpdates are the reference updates to commit.
	ReferenceUpdates ReferenceUpdates
	// QuarantinedPacks are the packs to include in the quarantine directory of the transaction.
	QuarantinedPacks [][]byte
	// DefaultBranchUpdate is the default branch update to commit.
	DefaultBranchUpdate *DefaultBranchUpdate
	// CustomHooksUpdate is the custom hooks update to commit.
	CustomHooksUpdate *CustomHooksUpdate
	// CreateRepository creates the repository on commit.
	CreateRepository bool
	// DeleteRepository deletes the repository on commit.
	DeleteRepository bool
	// IncludeObjects includes objects in the transaction's logged pack.
	IncludeObjects []git.ObjectID
	// UpdateAlternate updates the repository's alternate when set.
	UpdateAlternate *alternateUpdate
}

// RecordInitialReferenceValues calls RecordInitialReferenceValues on a transaction.
type RecordInitialReferenceValues struct {
	// TransactionID identifies the transaction to prepare the reference updates on.
	TransactionID int
	// InitialValues are the initial values to record.
	InitialValues map[git.ReferenceName]git.ObjectID
}

// UpdateReferences calls UpdateReferences on a transaction.
type UpdateReferences struct {
	// TransactionID identifies the transaction to update references on.
	TransactionID int
	// ReferenceUpdates are the reference updates to make.
	ReferenceUpdates ReferenceUpdates
}

// Rollback calls Rollback on a transaction.
type Rollback struct {
	// TransactionID identifies the transaction to rollback.
	TransactionID int
	// ExpectedError is the error that is expected to be returned when rolling back the transaction.
	ExpectedError error
}

// Prune prunes all unreferenced objects from the repository.
type Prune struct {
	// ExpectedObjects are the object expected to exist in the repository after pruning.
	ExpectedObjects []git.ObjectID
}

// RemoveRepository removes the repository from the disk. It must be run with the TransactionManager
// closed.
type RemoveRepository struct{}

// RepositoryAssertion asserts a given transaction's view of repositories matches the expected.
type RepositoryAssertion struct {
	// TransactionID identifies the transaction whose snapshot to assert.
	TransactionID int
	// Repositories is the expected state of the repositories the transaction sees. The
	// key is the repository's relative path and the value describes its expected state.
	Repositories RepositoryStates
}

// StateAssertions models an assertion of the entire state managed by the TransactionManager.
type StateAssertion struct {
	// Database is the expected state of the database.
	Database DatabaseState
	// Directory is the expected state of the manager's state directory in the repository.
	Directory testhelper.DirectoryState
	// Repositories is the expected state of the repositories in the storage. The key is
	// the repository's relative path and the value describes its expected state.
	Repositories RepositoryStates
}

// AdhocAssertion allows a test to add some custom assertions apart from the built-in assertions above.
type AdhocAssertion func(*testing.T, context.Context, *TransactionManager)

// steps defines execution steps in a test. Each test case can define multiple steps to exercise
// more complex behavior.
type steps []any

type transactionTestCase struct {
	desc          string
	steps         steps
	customSetup   func(*testing.T, context.Context, partitionID, string) testTransactionSetup
	expectedState StateAssertion
}

func runTransactionTest(t *testing.T, ctx context.Context, tc transactionTestCase, setup testTransactionSetup) {
	logger := testhelper.NewLogger(t)
	umask := testhelper.Umask()

	storageScopedFactory, err := setup.RepositoryFactory.ScopeByStorage(setup.Config.Storages[0].Name)
	require.NoError(t, err)
	repo := storageScopedFactory.Build(setup.RelativePath)

	repoPath, err := repo.Path()
	require.NoError(t, err)

	database, err := OpenDatabase(testhelper.SharedLogger(t), t.TempDir())
	require.NoError(t, err)
	defer testhelper.MustClose(t, database)

	storagePath := setup.Config.Storages[0].Path
	stateDir := filepath.Join(storagePath, "state")

	stagingDir := filepath.Join(storagePath, "staging")
	require.NoError(t, os.Mkdir(stagingDir, perm.PrivateDir))

	var (
		// managerRunning tracks whether the manager is running or closed.
		managerRunning bool
		// transactionManager is the current TransactionManager instance.
		transactionManager = NewTransactionManager(setup.PartitionID, logger, database, storagePath, stateDir, stagingDir, setup.CommandFactory, storageScopedFactory)
		// managerErr is used for synchronizing manager closing and returning
		// the error from Run.
		managerErr chan error
		// inflightTransactions tracks the number of on going transactions calls. It is used to synchronize
		// the database hooks with transactions.
		inflightTransactions sync.WaitGroup
	)

	// closeManager closes the manager. It waits until the manager's Run method has exited.
	closeManager := func() {
		t.Helper()

		transactionManager.Close()
		managerRunning, err = checkManagerError(t, ctx, managerErr, transactionManager)
		require.NoError(t, err)
		require.False(t, managerRunning)
	}

	// openTransactions holds references to all of the transactions that have been
	// began in a test case.
	openTransactions := map[int]*Transaction{}

	// Close the manager if it is running at the end of the test.
	defer func() {
		if managerRunning {
			closeManager()
		}
	}()
	for _, step := range tc.steps {
		switch step := step.(type) {
		case StartManager:
			require.False(t, managerRunning, "test error: manager started while it was already running")

			if step.ModifyStorage != nil {
				step.ModifyStorage(t, setup.Config, storagePath)
			}

			managerRunning = true
			managerErr = make(chan error)

			// The PartitionManager deletes and recreates the staging directory prior to starting a TransactionManager
			// to clean up any stale state leftover by crashes. Do that here as well so the tests don't fail if we don't
			// finish transactions after crash simulations.
			require.NoError(t, os.RemoveAll(stagingDir))
			require.NoError(t, os.Mkdir(stagingDir, perm.PrivateDir))

			transactionManager = NewTransactionManager(setup.PartitionID, logger, database, storagePath, stateDir, stagingDir, setup.CommandFactory, storageScopedFactory)
			installHooks(transactionManager, &inflightTransactions, step.Hooks)

			go func() {
				defer func() {
					if r := recover(); r != nil {
						err, ok := r.(error)
						if !ok {
							panic(r)
						}
						assert.ErrorIs(t, err, step.ExpectedError)
						managerErr <- err
					}
				}()

				managerErr <- transactionManager.Run()
			}()
		case CloseManager:
			require.True(t, managerRunning, "test error: manager closed while it was already closed")
			closeManager()
		case AssertManager:
			require.True(t, managerRunning, "test error: manager must be running for syncing")
			managerRunning, err = checkManagerError(t, ctx, managerErr, transactionManager)
			require.ErrorIs(t, err, step.ExpectedError)
		case Begin:
			require.NotContains(t, openTransactions, step.TransactionID, "test error: transaction id reused in begin")

			beginCtx := ctx
			if step.Context != nil {
				beginCtx = step.Context
			}

			transaction, err := transactionManager.Begin(beginCtx, step.RelativePath, step.SnapshottedRelativePaths, step.ReadOnly)
			require.ErrorIs(t, err, step.ExpectedError)
			if err == nil {
				require.Equalf(t, step.ExpectedSnapshotLSN, transaction.SnapshotLSN(), "mismatched ExpectedSnapshotLSN")
			}

			if step.ReadOnly {
				require.Empty(t,
					transaction.quarantineDirectory,
					"read-only transaction should not have a quarantine directory",
				)
			}

			openTransactions[step.TransactionID] = transaction
		case Commit:
			require.Contains(t, openTransactions, step.TransactionID, "test error: transaction committed before beginning it")

			transaction := openTransactions[step.TransactionID]
			if step.SkipVerificationFailures {
				transaction.SkipVerificationFailures()
			}

			rewrittenRepo := setup.RepositoryFactory.Build(
				transaction.RewriteRepository(&gitalypb.Repository{
					StorageName:  setup.Config.Storages[0].Name,
					RelativePath: transaction.relativePath,
				}),
			)

			if step.UpdateAlternate != nil {
				transaction.MarkAlternateUpdated()

				repoPath, err := rewrittenRepo.Path()
				require.NoError(t, err)

				if step.UpdateAlternate.content != "" {
					require.NoError(t, os.WriteFile(stats.AlternatesFilePath(repoPath), []byte(step.UpdateAlternate.content), fs.ModePerm))
				} else {
					// Ignore not exists errors as there are test cases testing removing alternate
					// when one is not set.
					if err := os.Remove(stats.AlternatesFilePath(repoPath)); !errors.Is(err, fs.ErrNotExist) {
						require.NoError(t, err)
					}
				}
			}

			if step.ReferenceUpdates != nil {
				transaction.UpdateReferences(step.ReferenceUpdates)
			}

			if step.DefaultBranchUpdate != nil {
				transaction.MarkDefaultBranchUpdated()
				require.NoError(t, rewrittenRepo.SetDefaultBranch(ctx, nil, step.DefaultBranchUpdate.Reference))
			}

			if step.CustomHooksUpdate != nil {
				transaction.MarkCustomHooksUpdated()
				if step.CustomHooksUpdate.CustomHooksTAR != nil {
					require.NoError(t, repoutil.SetCustomHooks(
						ctx,
						logger,
						config.NewLocator(setup.Config),
						nil,
						bytes.NewReader(step.CustomHooksUpdate.CustomHooksTAR),
						rewrittenRepo,
					))
				} else {
					rewrittenPath, err := rewrittenRepo.Path()
					require.NoError(t, err)
					require.NoError(t, os.RemoveAll(filepath.Join(rewrittenPath, repoutil.CustomHooksDir)))
				}
			}

			if step.QuarantinedPacks != nil {
				for _, dir := range []string{
					transaction.stagingDirectory,
					transaction.quarantineDirectory,
				} {
					const expectedPerm = perm.PrivateDir
					stat, err := os.Stat(dir)
					require.NoError(t, err)
					require.Equal(t, stat.Mode().Perm(), umask.Mask(expectedPerm),
						"%q had %q permission but expected %q", dir, stat.Mode().Perm().String(), expectedPerm,
					)
				}

				for _, pack := range step.QuarantinedPacks {
					require.NoError(t, rewrittenRepo.UnpackObjects(ctx, bytes.NewReader(pack)))
				}
			}

			if step.DeleteRepository {
				transaction.DeleteRepository()
			}

			for _, objectID := range step.IncludeObjects {
				transaction.IncludeObject(objectID)
			}

			commitCtx := ctx
			if step.Context != nil {
				commitCtx = step.Context
			}

			commitErr := transaction.Commit(commitCtx)
			switch expectedErr := step.ExpectedError.(type) {
			case func(testing.TB, error):
				expectedErr(t, commitErr)
			case error:
				require.ErrorIs(t, commitErr, expectedErr)
			case nil:
				require.NoError(t, commitErr)
			default:
				t.Fatalf("unexpected error type: %T", expectedErr)
			}
		case RecordInitialReferenceValues:
			require.Contains(t, openTransactions, step.TransactionID, "test error: record initial reference value on transaction before beginning it")

			transaction := openTransactions[step.TransactionID]
			require.NoError(t, transaction.RecordInitialReferenceValues(ctx, step.InitialValues))
		case UpdateReferences:
			require.Contains(t, openTransactions, step.TransactionID, "test error: reference updates aborted on committed before beginning it")

			transaction := openTransactions[step.TransactionID]
			transaction.UpdateReferences(step.ReferenceUpdates)
		case Rollback:
			require.Contains(t, openTransactions, step.TransactionID, "test error: transaction rollbacked before beginning it")
			require.Equal(t, step.ExpectedError, openTransactions[step.TransactionID].Rollback())
		case Prune:
			// Repack all objects into a single pack and remove all other packs to remove all
			// unreachable objects from the packs.
			gittest.Exec(t, setup.Config, "-C", repoPath, "repack", "-ad")
			// Prune all unreachable loose objects in the repository.
			gittest.Exec(t, setup.Config, "-C", repoPath, "prune")

			require.ElementsMatch(t, step.ExpectedObjects, gittest.ListObjects(t, setup.Config, repoPath))
		case RemoveRepository:
			require.NoError(t, os.RemoveAll(repoPath))
		case CreateRepository:
			require.Contains(t, openTransactions, step.TransactionID, "test error: repository created in transaction before beginning it")

			transaction := openTransactions[step.TransactionID]
			require.NoError(t, repoutil.Create(
				ctx,
				logger,
				config.NewLocator(setup.Config),
				setup.CommandFactory,
				nil,
				counter.NewRepositoryCounter(setup.Config.Storages),
				transaction.RewriteRepository(&gitalypb.Repository{
					StorageName:  setup.Config.Storages[0].Name,
					RelativePath: transaction.relativePath,
				}),
				func(repoProto *gitalypb.Repository) error {
					repo := setup.RepositoryFactory.Build(repoProto)

					if step.DefaultBranch != "" {
						require.NoError(t, repo.SetDefaultBranch(ctx, nil, step.DefaultBranch))
					}

					for _, pack := range step.Packs {
						require.NoError(t, repo.UnpackObjects(ctx, bytes.NewReader(pack)))
					}

					for name, oid := range step.References {
						require.NoError(t, repo.UpdateRef(ctx, name, oid, setup.ObjectHash.ZeroOID))
					}

					if step.CustomHooks != nil {
						require.NoError(t,
							repoutil.SetCustomHooks(ctx, logger, config.NewLocator(setup.Config), nil, bytes.NewReader(step.CustomHooks), repo),
						)
					}

					if step.Alternate != "" {
						repoPath, err := repo.Path()
						require.NoError(t, err)

						require.NoError(t, os.WriteFile(stats.AlternatesFilePath(repoPath), []byte(step.Alternate), fs.ModePerm))
					}

					return nil
				},
				repoutil.WithObjectHash(setup.ObjectHash),
			))
		case RunPackRefs:
			require.Contains(t, openTransactions, step.TransactionID, "test error: pack-refs housekeeping task aborted on committed before beginning it")

			transaction := openTransactions[step.TransactionID]
			transaction.PackRefs()
		case RunRepack:
			require.Contains(t, openTransactions, step.TransactionID, "test error: repack housekeeping task aborted on committed before beginning it")

			transaction := openTransactions[step.TransactionID]
			transaction.Repack(step.Config)
		case RepositoryAssertion:
			require.Contains(t, openTransactions, step.TransactionID, "test error: transaction's snapshot asserted before beginning it")
			transaction := openTransactions[step.TransactionID]

			RequireRepositories(t, ctx, setup.Config,
				// Assert the contents of the transaction's snapshot.
				filepath.Join(setup.Config.Storages[0].Path, transaction.snapshot.prefix),
				// Rewrite all of the repositories to point to their snapshots.
				func(relativePath string) *localrepo.Repo {
					return setup.RepositoryFactory.Build(
						transaction.RewriteRepository(&gitalypb.Repository{
							StorageName:  setup.Config.Storages[0].Name,
							RelativePath: relativePath,
						}),
					)
				}, step.Repositories)
		case AdhocAssertion:
			step(t, ctx, transactionManager)
		default:
			t.Fatalf("unhandled step type: %T", step)
		}
	}

	if managerRunning {
		managerRunning, err = checkManagerError(t, ctx, managerErr, transactionManager)
		require.NoError(t, err)
	}

	RequireDatabase(t, ctx, database, tc.expectedState.Database)

	expectedRepositories := tc.expectedState.Repositories
	if expectedRepositories == nil {
		expectedRepositories = RepositoryStates{
			setup.RelativePath: {},
		}
	}

	for relativePath, state := range expectedRepositories {
		if state.Objects == nil {
			state.Objects = []git.ObjectID{
				setup.ObjectHash.EmptyTreeOID,
				setup.Commits.First.OID,
				setup.Commits.Second.OID,
				setup.Commits.Third.OID,
				setup.Commits.Diverging.OID,
			}
			for _, tag := range setup.AnnotatedTags {
				state.Objects = append(state.Objects, tag.OID)
			}
		}

		if state.DefaultBranch == "" {
			state.DefaultBranch = git.DefaultRef
		}

		expectedRepositories[relativePath] = state
	}

	RequireRepositories(t, ctx, setup.Config, setup.Config.Storages[0].Path, storageScopedFactory.Build, expectedRepositories)

	expectedDirectory := tc.expectedState.Directory
	if expectedDirectory == nil {
		// Set the base state as the default so we don't have to repeat it in every test case but it
		// gets asserted.
		expectedDirectory = testhelper.DirectoryState{
			"/":    {Mode: fs.ModeDir | perm.PrivateDir},
			"/wal": {Mode: fs.ModeDir | perm.PrivateDir},
		}
	}

	testhelper.RequireDirectoryState(t, stateDir, "", expectedDirectory)

	entries, err := os.ReadDir(stagingDir)
	require.NoError(t, err)
	require.Empty(t, entries, "staging directory was not cleaned up")
}

func checkManagerError(t *testing.T, ctx context.Context, managerErrChannel chan error, mgr *TransactionManager) (bool, error) {
	t.Helper()

	testTransaction := &Transaction{
		referenceUpdates: []ReferenceUpdates{{"sentinel": {}}},
		result:           make(chan error, 1),
		finish:           func() error { return nil },
	}

	var (
		// managerErr is the error returned from the TransactionManager's Run method.
		managerErr error
		// closeChannel determines whether the channel was still open. If so, we need to close it
		// so further calls of checkManagerError do not block as they won't manage to receive an err
		// as it was already received and won't be able to send as the manager is no longer running.
		closeChannel bool
	)

	select {
	case managerErr, closeChannel = <-managerErrChannel:
	case mgr.admissionQueue <- testTransaction:
		// If the error channel doesn't receive, we don't know whether it is because the manager is still running
		// or we are still waiting for it to return. We test whether the manager is running or not here by queueing a
		// a transaction that will error. If the manager processes it, we know it is still running.
		//
		// If the manager was closed, it might manage to admit the testTransaction but not process it. To determine
		// whether that was the case, we also keep waiting on the managerErr channel.
		select {
		case err := <-testTransaction.result:
			require.Error(t, err, "test transaction is expected to error out")

			// Begin a transaction to wait until the manager has applied all log entries currently
			// committed. This ensures the disk state assertions run with all log entries fully applied
			// to the repository.
			tx, err := mgr.Begin(ctx, "non-existent", nil, false)
			require.NoError(t, err)
			require.NoError(t, tx.Rollback())

			return true, nil
		case managerErr, closeChannel = <-managerErrChannel:
		}
	}

	if closeChannel {
		close(managerErrChannel)
	}

	return false, managerErr
}
