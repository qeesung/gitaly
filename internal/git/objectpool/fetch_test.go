package objectpool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git"
	"gitlab.com/gitlab-org/gitaly/v14/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v14/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v14/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v14/internal/testhelper"
)

func TestFetchFromOriginDangling(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, pool, testRepo := setupObjectPool(t, ctx)

	require.NoError(t, pool.FetchFromOrigin(ctx, testRepo), "seed pool")

	const (
		existingTree   = "07f8147e8e73aab6c935c296e8cdc5194dee729b"
		existingCommit = "7975be0116940bf2ad4321f79d02a55c5f7779aa"
		existingBlob   = "c60514b6d3d6bf4bec1030f70026e34dfbd69ad5"
	)

	// We want to have some objects that are guaranteed to be dangling. Use
	// random data to make each object unique.
	nonce, err := text.RandomHex(4)
	require.NoError(t, err)

	// A blob with random contents should be unique.
	newBlob := gittest.WriteBlob(t, cfg, pool.FullPath(), []byte(nonce))

	// A tree with a randomly named blob entry should be unique.
	newTree := gittest.WriteTree(t, cfg, pool.FullPath(), []gittest.TreeEntry{
		{Mode: "100644", OID: git.ObjectID(existingBlob), Path: nonce},
	})

	// A commit with a random message should be unique.
	newCommit := gittest.WriteCommit(t, cfg, pool.FullPath(),
		gittest.WithTreeEntries(gittest.TreeEntry{
			OID: git.ObjectID(existingTree), Path: nonce, Mode: "040000",
		}),
	)

	// A tag with random hex characters in its name should be unique.
	newTagName := "tag-" + nonce
	newTag := gittest.WriteTag(t, cfg, pool.FullPath(), newTagName, existingCommit, gittest.WriteTagConfig{
		Message: "msg",
	})

	// `git tag` automatically creates a ref, so our new tag is not dangling.
	// Deleting the ref should fix that.
	gittest.Exec(t, cfg, "-C", pool.FullPath(), "update-ref", "-d", "refs/tags/"+newTagName)

	fsckBefore := gittest.Exec(t, cfg, "-C", pool.FullPath(), "fsck", "--connectivity-only", "--dangling")
	fsckBeforeLines := strings.Split(string(fsckBefore), "\n")

	for _, l := range []string{
		fmt.Sprintf("dangling blob %s", newBlob),
		fmt.Sprintf("dangling tree %s", newTree),
		fmt.Sprintf("dangling commit %s", newCommit),
		fmt.Sprintf("dangling tag %s", newTag),
	} {
		require.Contains(t, fsckBeforeLines, l, "test setup sanity check")
	}

	// We expect this second run to convert the dangling objects into
	// non-dangling objects.
	require.NoError(t, pool.FetchFromOrigin(ctx, testRepo), "second fetch")

	refsAfter := gittest.Exec(t, cfg, "-C", pool.FullPath(), "for-each-ref", "--format=%(refname) %(objectname)")
	refsAfterLines := strings.Split(string(refsAfter), "\n")
	for _, id := range []git.ObjectID{newBlob, newTree, newCommit, newTag} {
		require.Contains(t, refsAfterLines, fmt.Sprintf("refs/dangling/%s %s", id, id))
	}
}

func TestFetchFromOriginFsck(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, pool, repo := setupObjectPool(t, ctx)
	repoPath := filepath.Join(cfg.Storages[0].Path, repo.RelativePath)

	require.NoError(t, pool.FetchFromOrigin(ctx, repo), "seed pool")

	// We're creating a new commit which has a root tree with duplicate entries. git-mktree(1)
	// allows us to create these trees just fine, but git-fsck(1) complains.
	gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithTreeEntries(
			gittest.TreeEntry{OID: "4b825dc642cb6eb9a060e54bf8d69288fbee4904", Path: "dup", Mode: "040000"},
			gittest.TreeEntry{OID: "4b825dc642cb6eb9a060e54bf8d69288fbee4904", Path: "dup", Mode: "040000"},
		),
		gittest.WithBranch("branch"),
	)

	err := pool.FetchFromOrigin(ctx, repo)
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicateEntries: contains duplicate file entries")
}

func TestFetchFromOriginDeltaIslands(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, pool, testRepo := setupObjectPool(t, ctx)
	testRepoPath := filepath.Join(cfg.Storages[0].Path, testRepo.RelativePath)

	require.NoError(t, pool.FetchFromOrigin(ctx, testRepo), "seed pool")
	require.NoError(t, pool.Link(ctx, testRepo))

	gittest.TestDeltaIslands(t, cfg, testRepoPath, func() error {
		// This should create a new packfile with good delta chains in the pool
		if err := pool.FetchFromOrigin(ctx, testRepo); err != nil {
			return err
		}

		// Make sure the old packfile, with bad delta chains, is deleted from the source repo
		gittest.Exec(t, cfg, "-C", testRepoPath, "repack", "-ald")

		return nil
	})
}

func TestFetchFromOriginBitmapHashCache(t *testing.T) {
	ctx := testhelper.Context(t)

	_, pool, testRepo := setupObjectPool(t, ctx)

	require.NoError(t, pool.FetchFromOrigin(ctx, testRepo), "seed pool")

	packDir := filepath.Join(pool.FullPath(), "objects/pack")
	packEntries, err := os.ReadDir(packDir)
	require.NoError(t, err)

	var bitmap string
	for _, ent := range packEntries {
		if name := ent.Name(); strings.HasSuffix(name, ".bitmap") {
			bitmap = filepath.Join(packDir, name)
			break
		}
	}

	require.NotEmpty(t, bitmap, "path to bitmap file")

	gittest.TestBitmapHasHashcache(t, bitmap)
}

func TestFetchFromOriginRefUpdates(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, pool, testRepo := setupObjectPool(t, ctx)
	testRepoPath := filepath.Join(cfg.Storages[0].Path, testRepo.RelativePath)

	poolPath := pool.FullPath()

	require.NoError(t, pool.FetchFromOrigin(ctx, testRepo), "seed pool")

	oldRefs := map[string]string{
		"heads/csv":   "3dd08961455abf80ef9115f4afdc1c6f968b503c",
		"tags/v1.1.0": "8a2a6eb295bb170b34c24c76c49ed0e9b2eaf34b",
	}

	for ref, oid := range oldRefs {
		require.Equal(t, oid, resolveRef(t, cfg, testRepoPath, "refs/"+ref), "look up %q in source", ref)
		require.Equal(t, oid, resolveRef(t, cfg, poolPath, "refs/remotes/origin/"+ref), "look up %q in pool", ref)
	}

	newRefs := map[string]string{
		"heads/csv":   "46abbb087fcc0fd02c340f0f2f052bd2c7708da3",
		"tags/v1.1.0": "646ece5cfed840eca0a4feb21bcd6a81bb19bda3",
	}

	for ref, newOid := range newRefs {
		require.NotEqual(t, newOid, oldRefs[ref], "sanity check of new refs")
	}

	for ref, oid := range newRefs {
		gittest.Exec(t, cfg, "-C", testRepoPath, "update-ref", "refs/"+ref, oid)
		require.Equal(t, oid, resolveRef(t, cfg, testRepoPath, "refs/"+ref), "look up %q in source after update", ref)
	}

	require.NoError(t, pool.FetchFromOrigin(ctx, testRepo), "update pool")

	for ref, oid := range newRefs {
		require.Equal(t, oid, resolveRef(t, cfg, poolPath, "refs/remotes/origin/"+ref), "look up %q in pool after update", ref)
	}

	looseRefs := testhelper.MustRunCommand(t, nil, "find", filepath.Join(poolPath, "refs"), "-type", "f")
	require.Equal(t, "", string(looseRefs), "there should be no loose refs after the fetch")
}

func TestFetchFromOrigin_refs(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, pool, _ := setupObjectPool(t, ctx)
	poolPath := pool.FullPath()

	// Init the source repo with a bunch of refs.
	repo, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(), gittest.WithTreeEntries())
	for _, ref := range []string{"refs/heads/master", "refs/environments/1", "refs/tags/lightweight-tag"} {
		gittest.Exec(t, cfg, "-C", repoPath, "update-ref", ref, commitID.String())
	}
	gittest.WriteTag(t, cfg, repoPath, "annotated-tag", commitID.Revision(), gittest.WriteTagConfig{
		Message: "tag message",
	})

	require.NoError(t, pool.Init(ctx))

	// The pool shouldn't have any refs yet.
	require.Empty(t, gittest.Exec(t, cfg, "-C", poolPath, "for-each-ref", "--format=%(refname)"))

	require.NoError(t, pool.FetchFromOrigin(ctx, repo))

	require.Equal(t,
		[]string{
			"refs/remotes/origin/environments/1",
			"refs/remotes/origin/heads/master",
			"refs/remotes/origin/tags/annotated-tag",
			"refs/remotes/origin/tags/lightweight-tag",
		},
		strings.Split(text.ChompBytes(gittest.Exec(t, cfg, "-C", poolPath, "for-each-ref", "--format=%(refname)")), "\n"),
	)
}

func resolveRef(t *testing.T, cfg config.Cfg, repo string, ref string) string {
	out := gittest.Exec(t, cfg, "-C", repo, "rev-parse", ref)
	return text.ChompBytes(out)
}
