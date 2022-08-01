package gittest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
)

// TestDeltaIslands checks whether functions that repack objects in a repository correctly set up
// delta islands. Based on https://github.com/git/git/blob/master/t/t5320-delta-islands.sh. Note
// that this function accepts two different repository paths: one repo to modify that shall grow the
// new references and objects, and one repository that we ultimately end up repacking. In the
// general case these should refer to the same repository, but for object pools these may be the
// pool member and the pool, respectively.
func TestDeltaIslands(
	t *testing.T,
	cfg config.Cfg,
	repoPathToModify string,
	repoPathToRepack string,
	isPoolRepo bool,
	repack func() error,
) {
	t.Helper()

	// Create blobs that we expect Git to use delta compression on.
	blob1 := strings.Repeat("X", 100000)
	blob2 := blob1 + "\nblob 2"
	// Create another, third blob that is longer than the second blob. Git prefers the largest
	// blob as delta base, which means that it should in theory pick this blob. But we will make
	// it reachable via a reference that is not part of the delta island, and thus it should not
	// be used as delta base.
	badBlob := blob2 + "\nbad blob"

	refsPrefix := "refs"
	if isPoolRepo {
		// Pool repositories use different references for their delta islands, so we need to
		// adapt accordingly.
		refsPrefix = git.ObjectPoolRefNamespace
	}

	// Make the first two blobs reachable via references that are part of the delta island.
	blob1ID := commitBlob(t, cfg, repoPathToModify, refsPrefix+"/heads/branch1", blob1)
	blob2ID := commitBlob(t, cfg, repoPathToModify, refsPrefix+"/tags/tag2", blob2)

	// The bad blob will only be reachable via a reference that is not covered by a delta
	// island. Because of that it should be excluded from delta chains in the main island.
	badBlobID := commitBlob(t, cfg, repoPathToModify, refsPrefix+"/bad/ref3", badBlob)

	// Repack all objects into a single pack so that we can verify that delta chains are built
	// by Git as expected. Most notably, we don't use the delta islands here yet and thus the
	// delta base for both blob1 and blob2 should be the bad blob.
	Exec(t, cfg, "-C", repoPathToModify, "repack", "-ad")
	require.Equal(t, badBlobID, deltaBase(t, cfg, repoPathToModify, blob1ID), "expect blob 1 delta base to be bad blob after test setup")
	require.Equal(t, badBlobID, deltaBase(t, cfg, repoPathToModify, blob2ID), "expect blob 2 delta base to be bad blob after test setup")

	// Now we run the repacking function provided to us by the caller. We expect it to use delta
	// chains, and thus neither of the two blobs should use the bad blob as delta base.
	require.NoError(t, repack(), "repack after delta island setup")
	require.Equal(t, blob2ID, deltaBase(t, cfg, repoPathToRepack, blob1ID), "blob 1 delta base should be blob 2 after repack")
	require.Equal(t, DefaultObjectHash.ZeroOID.String(), deltaBase(t, cfg, repoPathToRepack, blob2ID), "blob 2 should not be delta compressed after repack")
}

func commitBlob(t *testing.T, cfg config.Cfg, repoPath, ref string, content string) string {
	blobID := WriteBlob(t, cfg, repoPath, []byte(content))

	// No parent, that means this will be an initial commit. Not very
	// realistic but it doesn't matter for delta compression.
	commitID := WriteCommit(t, cfg, repoPath,
		WithTreeEntries(TreeEntry{
			Mode: "100644", OID: blobID, Path: "file",
		}),
	)

	Exec(t, cfg, "-C", repoPath, "update-ref", ref, commitID.String())

	return blobID.String()
}

func deltaBase(t *testing.T, cfg config.Cfg, repoPath string, blobID string) string {
	catfileOut := ExecOpts(t, cfg, ExecConfig{Stdin: strings.NewReader(blobID)},
		"-C", repoPath, "cat-file", "--batch-check=%(deltabase)",
	)

	return text.ChompBytes(catfileOut)
}
