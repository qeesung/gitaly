package housekeeping

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/housekeeping/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestRepackObjects(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	// repack is a custom helper function that repacks while explicitly disabling the update of
	// server info. This is done so that we assert that the actual repacking logic doesn't write
	// the server info.
	repack := func(t *testing.T, repoPath string, args ...string) {
		gittest.Exec(t, cfg, append([]string{
			"-c", "repack.updateServerInfo=false", "-C", repoPath, "repack",
		}, args...)...)
	}

	for _, tc := range []struct {
		desc              string
		setup             func(t *testing.T, repoPath string)
		repackCfg         config.RepackObjectsConfig
		stateBeforeRepack objectsState
		stateAfterRepack  objectsState
		expectedErr       error
	}{
		{
			desc:  "default strategy fails",
			setup: func(t *testing.T, repoPath string) {},
			repackCfg: config.RepackObjectsConfig{
				Strategy: "",
			},
			expectedErr: structerr.NewInvalidArgument(`invalid strategy: ""`),
		},
		{
			desc:  "garbage strategy fails",
			setup: func(t *testing.T, repoPath string) {},
			repackCfg: config.RepackObjectsConfig{
				Strategy: "garbage",
			},
			expectedErr: structerr.NewInvalidArgument(`invalid strategy: "garbage"`),
		},
		{
			desc: "incremental repack packs objects",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyIncrementalWithUnreachable,
			},
			stateBeforeRepack: objectsState{
				looseObjects: 2,
			},
			stateAfterRepack: objectsState{
				packfiles: 1,
			},
		},
		{
			desc: "incremental repack does not repack packfiles",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("first"), gittest.WithBranch("first"))
				repack(t, repoPath, "-d")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("second"), gittest.WithBranch("second"))
				repack(t, repoPath, "-d")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("third"), gittest.WithBranch("third"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyIncrementalWithUnreachable,
			},
			stateBeforeRepack: objectsState{
				packfiles:    2,
				looseObjects: 1,
			},
			stateAfterRepack: objectsState{
				packfiles: 3,
			},
		},
		{
			desc: "incremental repack with bitmap returns an error",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("first"), gittest.WithBranch("first"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy:    config.RepackObjectsStrategyIncrementalWithUnreachable,
				WriteBitmap: true,
			},
			stateBeforeRepack: objectsState{
				looseObjects: 2,
			},
			stateAfterRepack: objectsState{
				looseObjects: 2,
			},
			expectedErr: structerr.NewInvalidArgument("cannot write packfile bitmap for an incremental repack"),
		},
		{
			desc: "full repack packs packfiles and loose objects",
			setup: func(t *testing.T, repoPath string) {
				// We seed the repository so that it contains two packfiles and one loose object.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("first"), gittest.WithBranch("first"))
				repack(t, repoPath, "-d")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("second"), gittest.WithBranch("second"))
				repack(t, repoPath, "-d")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("third"), gittest.WithBranch("third"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyFullWithUnreachable,
			},
			stateBeforeRepack: objectsState{
				looseObjects: 1,
				packfiles:    2,
			},
			stateAfterRepack: objectsState{
				packfiles: 1,
			},
		},
		{
			desc: "full repack packs packfiles and loose objects and writes a bitmap",
			setup: func(t *testing.T, repoPath string) {
				// We seed the repository so that it contains two packfiles and one loose object.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("first"), gittest.WithBranch("first"))
				repack(t, repoPath, "-d")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("second"), gittest.WithBranch("second"))
				repack(t, repoPath, "-d")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("third"), gittest.WithBranch("third"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy:    config.RepackObjectsStrategyFullWithUnreachable,
				WriteBitmap: true,
			},
			stateBeforeRepack: objectsState{
				looseObjects: 1,
				packfiles:    2,
			},
			stateAfterRepack: objectsState{
				packfiles: 1,
				hasBitmap: true,
			},
		},
		{
			desc: "multi-pack-index with full repack packs packfiles and loose objects",
			setup: func(t *testing.T, repoPath string) {
				// We seed the repository so that it contains two packfiles and one loose object.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("first"), gittest.WithBranch("first"))
				repack(t, repoPath, "-d")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("second"), gittest.WithBranch("second"))
				repack(t, repoPath, "-d")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("third"), gittest.WithBranch("third"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyFullWithUnreachable,
				WriteMultiPackIndex: true,
			},
			stateBeforeRepack: objectsState{
				looseObjects: 1,
				packfiles:    2,
			},
			stateAfterRepack: objectsState{
				packfiles:         1,
				hasMultiPackIndex: true,
			},
		},
		{
			desc: "multi-pack-index with full repack and bitmap writes bitmap",
			setup: func(t *testing.T, repoPath string) {
				// We seed the repository so that it contains two packfiles and one loose object.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("first"), gittest.WithBranch("first"))
				repack(t, repoPath, "-d")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("second"), gittest.WithBranch("second"))
				repack(t, repoPath, "-d")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("third"), gittest.WithBranch("third"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyFullWithUnreachable,
				WriteBitmap:         true,
				WriteMultiPackIndex: true,
			},
			stateBeforeRepack: objectsState{
				looseObjects: 1,
				packfiles:    2,
			},
			stateAfterRepack: objectsState{
				packfiles:               1,
				hasMultiPackIndex:       true,
				hasMultiPackIndexBitmap: true,
			},
		},
		{
			desc: "multi-pack-index with full repack removes preexisting bitmaps",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("first"), gittest.WithBranch("first"))
				repack(t, repoPath, "-A", "-d", "-b")
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("second"), gittest.WithBranch("second"))
				repack(t, repoPath, "-d")
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy:            config.RepackObjectsStrategyFullWithUnreachable,
				WriteBitmap:         true,
				WriteMultiPackIndex: true,
			},
			stateBeforeRepack: objectsState{
				packfiles: 2,
				hasBitmap: true,
			},
			stateAfterRepack: objectsState{
				packfiles:               1,
				hasMultiPackIndex:       true,
				hasMultiPackIndexBitmap: true,
			},
		},
		{
			desc: "unreachable objects get moved into cruft pack",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("reachable"), gittest.WithBranch("reachable"))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("unreachable"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyFullWithCruft,
			},
			stateBeforeRepack: objectsState{
				looseObjects: 3,
			},
			stateAfterRepack: objectsState{
				packfiles:  2,
				cruftPacks: 1,
			},
		},
		{
			desc:  "expiring cruft objects requires writing cruft packs",
			setup: func(t *testing.T, repoPath string) {},
			repackCfg: config.RepackObjectsConfig{
				Strategy:          config.RepackObjectsStrategyIncrementalWithUnreachable,
				CruftExpireBefore: time.Now(),
			},
			expectedErr: structerr.NewInvalidArgument("cannot expire cruft objects when not writing cruft packs"),
		},
		{
			desc: "stale unreachable object is not added to cruft pack",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("reachable"), gittest.WithBranch("reachable"))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("unreachable"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy:          config.RepackObjectsStrategyFullWithCruft,
				CruftExpireBefore: time.Now().Add(time.Hour),
			},
			stateBeforeRepack: objectsState{
				looseObjects: 3,
			},
			// The loose object is older than the expiration date already, which is why
			// it is not added to any cruft pack in the first place. But neither is it
			// getting deleted, which means that we need to ensure that ourselves.
			stateAfterRepack: objectsState{
				looseObjects: 1,
				packfiles:    1,
			},
		},
		{
			desc: "recent unreachable objects are added to cruft pack",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("reachable"), gittest.WithBranch("reachable"))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("unreachable"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy:          config.RepackObjectsStrategyFullWithCruft,
				CruftExpireBefore: time.Now().Add(-1 * time.Hour),
			},
			stateBeforeRepack: objectsState{
				looseObjects: 3,
			},
			// The loose object is newer than the expiration date, so it should not get
			// pruned but instead it should be added to the cruft pack.
			stateAfterRepack: objectsState{
				packfiles:  2,
				cruftPacks: 1,
			},
		},
		{
			desc: "recent cruft objects don't get deleted",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("reachable"), gittest.WithBranch("reachable"))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("unreachable"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "--cruft", "-d", "-n", "--no-write-bitmap-index")
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy:          config.RepackObjectsStrategyFullWithCruft,
				CruftExpireBefore: time.Now().Add(-1 * time.Hour),
			},
			stateBeforeRepack: objectsState{
				packfiles:  2,
				cruftPacks: 1,
			},
			stateAfterRepack: objectsState{
				packfiles:  2,
				cruftPacks: 1,
			},
		},
		{
			desc: "stale cruft objects get deleted",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("reachable"), gittest.WithBranch("reachable"))
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("unreachable"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "--cruft", "-d", "-n", "--no-write-bitmap-index")
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy:          config.RepackObjectsStrategyFullWithCruft,
				CruftExpireBefore: time.Now().Add(1 * time.Hour),
			},
			stateBeforeRepack: objectsState{
				packfiles:  2,
				cruftPacks: 1,
			},
			stateAfterRepack: objectsState{
				packfiles: 1,
			},
		},
		{
			desc: "geometric repack with reachable object",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithMessage("unreachable"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyGeometric,
			},
			stateBeforeRepack: objectsState{
				looseObjects: 2,
			},
			stateAfterRepack: objectsState{
				packfiles: 1,
			},
		},
		{
			desc: "geometric repack soaks up unreachable objects",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteBlob(t, cfg, repoPath, []byte("unreachable blob"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyGeometric,
			},
			stateBeforeRepack: objectsState{
				looseObjects: 1,
			},
			stateAfterRepack: objectsState{
				packfiles: 1,
			},
		},
		{
			desc: "geometric repack leaves cruft pack alone",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteBlob(t, cfg, repoPath, []byte("unreachable blob"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "--cruft", "-d", "-n", "--no-write-bitmap-index")
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyGeometric,
			},
			stateBeforeRepack: objectsState{
				packfiles:  1,
				cruftPacks: 1,
			},
			stateAfterRepack: objectsState{
				packfiles:  1,
				cruftPacks: 1,
			},
		},
		{
			desc: "geometric repack leaves keep pack alone",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad", "-n", "--no-write-bitmap-index")

				packPath, err := filepath.Glob(filepath.Join(repoPath, "objects", "pack", "pack-*.pack"))
				require.NoError(t, err)
				require.Len(t, packPath, 1)

				keepPath := strings.TrimSuffix(packPath[0], ".pack") + ".keep"
				require.NoError(t, os.WriteFile(keepPath, nil, perm.PrivateFile))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyGeometric,
			},
			stateBeforeRepack: objectsState{
				packfiles: 1,
				keepPacks: 1,
			},
			stateAfterRepack: objectsState{
				packfiles: 1,
				keepPacks: 1,
			},
		},
		{
			desc: "geometric repack keeps valid geometric sequence",
			setup: func(t *testing.T, repoPath string) {
				// Write a commit that in total contains 3 objects: 1 commit, 1 tree
				// and 1 blob.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a", Mode: "100644", Content: "a"},
				), gittest.WithBranch("main"))

				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad", "-n", "--no-write-bitmap-index")

				// Now we write another blob. As the previous packfile contains 3x
				// the amount of objects we should just create a new packfile
				// instead of merging them.
				gittest.WriteBlob(t, cfg, repoPath, []byte("new blob"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyGeometric,
			},
			stateBeforeRepack: objectsState{
				packfiles:    1,
				looseObjects: 1,
			},
			stateAfterRepack: objectsState{
				packfiles: 2,
			},
		},
		{
			desc: "geometric repack keeps preexisting pack when new pack violates sequence",
			setup: func(t *testing.T, repoPath string) {
				// Write a commit that in total contains 3 objects: 1 commit, 1 tree
				// and 1 blob.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a", Mode: "100644", Content: "a"},
				), gittest.WithBranch("main"))

				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad", "-n", "--no-write-bitmap-index")

				// Now we write two additional blobs. Even though the newly written
				// packfile will cause us to invalidate the geometric sequence, we
				// still create it without merging them. This _seems_ to be on
				// purpose: packfiles will only be merged when preexisting packs
				// validate the sequence.
				gittest.WriteBlob(t, cfg, repoPath, []byte("one"))
				gittest.WriteBlob(t, cfg, repoPath, []byte("two"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyGeometric,
			},
			stateBeforeRepack: objectsState{
				packfiles:    1,
				looseObjects: 2,
			},
			stateAfterRepack: objectsState{
				packfiles: 2,
			},
		},
		{
			desc: "geometric repack repacks when geometric sequence is invalidated",
			setup: func(t *testing.T, repoPath string) {
				// Write a commit that in total contains 3 objects: 1 commit, 1 tree
				// and 1 blob.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a", Mode: "100644", Content: "a"},
				), gittest.WithBranch("main"))

				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-Ad", "-n", "--no-write-bitmap-index")

				// Write a second set of objects that is reachable so that we can
				// create a second packfile easily.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "1", Mode: "100644", Content: "1"},
				), gittest.WithBranch("main"))
				// Do an incremental repack now. We thus have two packfiles that
				// invalidate the geometric sequence.
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-d", "-n", "--no-write-bitmap-index")
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyGeometric,
			},
			stateBeforeRepack: objectsState{
				packfiles: 2,
			},
			stateAfterRepack: objectsState{
				packfiles: 1,
			},
		},
		{
			desc: "geometric repack consolidates multiple packs into one",
			setup: func(t *testing.T, repoPath string) {
				// Write a commit that in total contains 3 objects: 1 commit, 1 tree
				// and 1 blob.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a", Mode: "100644", Content: "a"},
				), gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-d", "-n", "--no-write-bitmap-index")

				// Write another commit with three new objects, so that we now have
				// two packfiles with three objects each.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "b", Mode: "100644", Content: "b"},
				), gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-d", "-n", "--no-write-bitmap-index")

				// Write a third commit with 12 new objects.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "01", Mode: "100644", Content: "01"},
					gittest.TreeEntry{Path: "02", Mode: "100644", Content: "02"},
					gittest.TreeEntry{Path: "03", Mode: "100644", Content: "03"},
					gittest.TreeEntry{Path: "04", Mode: "100644", Content: "04"},
					gittest.TreeEntry{Path: "05", Mode: "100644", Content: "05"},
					gittest.TreeEntry{Path: "06", Mode: "100644", Content: "06"},
					gittest.TreeEntry{Path: "07", Mode: "100644", Content: "07"},
					gittest.TreeEntry{Path: "08", Mode: "100644", Content: "08"},
					gittest.TreeEntry{Path: "09", Mode: "100644", Content: "09"},
					gittest.TreeEntry{Path: "10", Mode: "100644", Content: "10"},
				), gittest.WithBranch("main"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-d", "-n", "--no-write-bitmap-index")

				// We now have three packfiles: two with three objects respectively,
				// and one with 12 objects. The first two packfiles do not form a
				// geometric sequence, but if they are packed together they result
				// in a packfile with 6 objects, which would restore the sequence.
				// So what we want to see is that we start with three, but end with
				// 2 packfiles.
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyGeometric,
			},
			stateBeforeRepack: objectsState{
				packfiles: 3,
			},
			stateAfterRepack: objectsState{
				packfiles: 2,
			},
		},
		{
			desc: "full with unreachable objects",
			setup: func(t *testing.T, repoPath string) {
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))
				gittest.WriteBlob(t, cfg, repoPath, []byte("unreachable"))
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyFullWithUnreachable,
			},
			stateBeforeRepack: objectsState{
				looseObjects: 3,
			},
			stateAfterRepack: objectsState{
				packfiles: 1,
				// Ideally, the full repack would include this unreachable object
				// into the packfile. It doesn't though, and git-repack(1) does not
				// give us a way to make it do so. So we need to accept this
				// behaviour for now.
				looseObjects: 1,
			},
		},
		{
			desc: "full with unreachable objects includes packed unreachable objects",
			setup: func(t *testing.T, repoPath string) {
				// Write a first set of objects and pack them. We'll overwrite the
				// branch with the second commit, so these objects will become
				// unreachable.
				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a", Mode: "100644", Content: "a"},
				))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-dn", "--no-write-bitmap-index")

				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "b", Mode: "100644", Content: "b"},
				))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-dn", "--no-write-bitmap-index")
			},
			repackCfg: config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyFullWithUnreachable,
			},
			stateBeforeRepack: objectsState{
				packfiles: 2,
			},
			stateAfterRepack: objectsState{
				packfiles: 1,
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			tc.setup(t, repoPath)

			timestampBeforeRepack := time.Now()

			requireObjectsState(t, repo, tc.stateBeforeRepack)
			require.Equal(t, tc.expectedErr, RepackObjects(ctx, repo, tc.repackCfg))
			requireObjectsState(t, repo, tc.stateAfterRepack)

			timestampAfterRepack := time.Now()

			fullRepackTimestamp, err := stats.FullRepackTimestamp(repoPath)
			require.NoError(t, err)
			switch tc.repackCfg.Strategy {
			case config.RepackObjectsStrategyFullWithCruft, config.RepackObjectsStrategyFullWithUnreachable:
				require.GreaterOrEqual(t, fullRepackTimestamp, timestampBeforeRepack)
				require.LessOrEqual(t, fullRepackTimestamp, timestampAfterRepack)
			default:
				require.Zero(t, fullRepackTimestamp)
			}

			// There should not be any server info data in the repository.
			require.NoFileExists(t, filepath.Join(repoPath, "info", "refs"))
			require.NoFileExists(t, filepath.Join(repoPath, "objects", "info", "packs"))
		})
	}

	testRepoAndPool(t, "delta islands", func(t *testing.T, relativePath string) {
		t.Parallel()

		repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
			SkipCreationViaService: true,
			RelativePath:           relativePath,
		})
		repo := localrepo.NewTestRepo(t, cfg, repoProto)

		gittest.TestDeltaIslands(t, cfg, repoPath, repoPath, storage.IsPoolRepository(repoProto), func() error {
			return RepackObjects(ctx, repo, config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyFullWithUnreachable,
			})
		})
	})
}

func TestRepackObjects_incrementalWithUnreachable(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg := testcfg.Build(t)

	type setupData struct {
		expectedErr       error
		stateBeforeRepack objectsState
		stateAfterRepack  objectsState
		expectedPackfiles [][]git.ObjectID
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T, repoPath string) setupData
	}{
		{
			desc: "unreachable object is packed",
			setup: func(t *testing.T, repoPath string) setupData {
				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("data"))
				return setupData{
					stateBeforeRepack: objectsState{
						looseObjects: 1,
					},
					stateAfterRepack: objectsState{
						looseObjects: 0,
						packfiles:    1,
					},
					expectedPackfiles: [][]git.ObjectID{
						{
							blobID,
						},
					},
				}
			},
		},
		{
			desc: "reachable object is packed",
			setup: func(t *testing.T, repoPath string) setupData {
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))

				return setupData{
					stateBeforeRepack: objectsState{
						looseObjects: 2,
					},
					stateAfterRepack: objectsState{
						looseObjects: 0,
						packfiles:    1,
					},
					expectedPackfiles: [][]git.ObjectID{
						{
							commitID,
							gittest.DefaultObjectHash.EmptyTreeOID,
						},
					},
				}
			},
		},
		{
			desc: "no packfile is generated when all objects are packed",
			setup: func(t *testing.T, repoPath string) setupData {
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-a", "--no-write-bitmap-index")

				return setupData{
					stateBeforeRepack: objectsState{
						looseObjects: 2,
						packfiles:    1,
					},
					stateAfterRepack: objectsState{
						looseObjects: 0,
						packfiles:    1,
					},
					expectedPackfiles: [][]git.ObjectID{
						{
							commitID,
							gittest.DefaultObjectHash.EmptyTreeOID,
						},
					},
				}
			},
		},
		{
			desc: "some packed and non-packed objects",
			setup: func(t *testing.T, repoPath string) setupData {
				commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))
				gittest.Exec(t, cfg, "-C", repoPath, "repack", "-a", "--no-write-bitmap-index")
				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("content"))

				return setupData{
					stateBeforeRepack: objectsState{
						looseObjects: 3,
						packfiles:    1,
					},
					stateAfterRepack: objectsState{
						looseObjects: 0,
						packfiles:    2,
					},
					expectedPackfiles: [][]git.ObjectID{
						{
							commitID,
							gittest.DefaultObjectHash.EmptyTreeOID,
						},
						{
							blobID,
						},
					},
				}
			},
		},
		{
			desc: "loose object in alternate object directory is ignored",
			setup: func(t *testing.T, repoPath string) setupData {
				alternateRepoProto, alternateRepoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				alternateRepo := localrepo.NewTestRepo(t, cfg, alternateRepoProto)
				gittest.WriteBlob(t, cfg, alternateRepoPath, []byte("content"))
				requireObjectsState(t, alternateRepo, objectsState{
					looseObjects: 1,
				})

				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "objects", "info", "alternates"),
					[]byte(filepath.Join(alternateRepoPath, "objects")),
					perm.PrivateFile,
				))

				return setupData{
					stateBeforeRepack: objectsState{
						looseObjects: 0,
					},
					stateAfterRepack: objectsState{
						looseObjects: 0,
						packfiles:    0,
					},
					expectedPackfiles: [][]git.ObjectID{},
				}
			},
		},
		{
			desc: "local object that exists in alternate is skipped",
			setup: func(t *testing.T, repoPath string) setupData {
				alternateRepoProto, alternateRepoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
					SkipCreationViaService: true,
				})
				alternateRepo := localrepo.NewTestRepo(t, cfg, alternateRepoProto)
				// We're writing a referenced commit so that the resulting object
				// will be included in the packfile.
				gittest.WriteCommit(t, cfg, alternateRepoPath, gittest.WithBranch("branch"))
				gittest.Exec(t, cfg, "-C", alternateRepoPath, "repack", "-a", "-d", "--no-write-bitmap-index")

				requireObjectsState(t, alternateRepo, objectsState{
					packfiles: 1,
				})

				gittest.WriteCommit(t, cfg, repoPath)

				require.NoError(t, os.WriteFile(
					filepath.Join(repoPath, "objects", "info", "alternates"),
					[]byte(filepath.Join(alternateRepoPath, "objects")),
					perm.PrivateFile,
				))

				return setupData{
					stateBeforeRepack: objectsState{
						looseObjects: 2,
					},
					stateAfterRepack: objectsState{
						packfiles: 0,
					},
					expectedPackfiles: [][]git.ObjectID{},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg, gittest.CreateRepositoryConfig{
				SkipCreationViaService: true,
			})
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			setup := tc.setup(t, repoPath)

			requireObjectsState(t, repo, setup.stateBeforeRepack)
			require.Equal(t, setup.expectedErr, RepackObjects(ctx, repo, config.RepackObjectsConfig{
				Strategy: config.RepackObjectsStrategyIncrementalWithUnreachable,
			}))
			requireObjectsState(t, repo, setup.stateAfterRepack)

			packfileIndices, err := filepath.Glob(filepath.Join(repoPath, "objects", "pack", "pack-*.idx"))
			require.NoError(t, err)

			var packfiles [][]git.ObjectID
			for _, packfileIndex := range packfileIndices {
				data := testhelper.MustReadFile(t, packfileIndex)

				var stdout bytes.Buffer
				gittest.ExecOpts(t, cfg, gittest.ExecConfig{
					Stdin:  bytes.NewReader(data),
					Stdout: &stdout,
				}, "-C", repoPath, "show-index")

				var packfile []git.ObjectID
				for _, objectLine := range strings.Split(text.ChompBytes(stdout.Bytes()), "\n") {
					// Each line printed by git-show-index(1) contains three
					// tuples of information: the object size, the object ID and
					// the CRC32 of the object data. We only care for the second
					// column, the object ID.
					objectInfo := strings.Split(objectLine, " ")
					require.Len(t, objectInfo, 3)

					packfile = append(packfile, git.ObjectID(objectInfo[1]))
				}
				sort.Slice(packfile, func(i, j int) bool {
					return packfile[i].String() < packfile[j].String()
				})

				packfiles = append(packfiles, packfile)
			}

			for _, expectedPackfile := range setup.expectedPackfiles {
				sort.Slice(expectedPackfile, func(i, j int) bool {
					return expectedPackfile[i].String() < expectedPackfile[j].String()
				})
			}

			require.ElementsMatch(t, setup.expectedPackfiles, packfiles)
		})
	}
}
