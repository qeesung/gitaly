package objectpool

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestFetchFromOriginDangling(t *testing.T) {
	source, _, cleanup := testhelper.NewTestRepo(t)
	defer cleanup()

	pool, err := NewObjectPool(source.StorageName, testhelper.NewTestObjectPoolName(t))
	require.NoError(t, err)

	ctx, cancel := testhelper.Context()
	defer cancel()

	require.NoError(t, pool.FetchFromOrigin(ctx, source), "seed pool")

	const (
		existingTree   = "07f8147e8e73aab6c935c296e8cdc5194dee729b"
		existingCommit = "7975be0116940bf2ad4321f79d02a55c5f7779aa"
		existingBlob   = "c60514b6d3d6bf4bec1030f70026e34dfbd69ad5"
	)

	nonceBytes := make([]byte, 4)
	_, err = io.ReadFull(rand.Reader, nonceBytes)
	require.NoError(t, err)
	nonce := hex.EncodeToString(nonceBytes)

	baseArgs := []string{"-C", pool.FullPath()}
	newBlobArgs := append(baseArgs, "hash-object", "-t", "blob", "-w", "--stdin")
	newBlob := text.ChompBytes(testhelper.MustRunCommand(t, strings.NewReader(nonce), "git", newBlobArgs...))

	newTreeArgs := append(baseArgs, "mktree")
	newTreeStdin := strings.NewReader(fmt.Sprintf("100644 blob %s	%s\n", existingBlob, nonce))
	newTree := text.ChompBytes(testhelper.MustRunCommand(t, newTreeStdin, "git", newTreeArgs...))

	newCommitArgs := append(baseArgs, "commit-tree", existingTree)
	newCommit := text.ChompBytes(testhelper.MustRunCommand(t, strings.NewReader(nonce), "git", newCommitArgs...))

	newTagName := "tag-" + nonce
	newTagArgs := append(baseArgs, "tag", "-m", "msg", "-a", newTagName, existingCommit)
	testhelper.MustRunCommand(t, strings.NewReader(nonce), "git", newTagArgs...)
	newTag := text.ChompBytes(testhelper.MustRunCommand(t, nil, "git", append(baseArgs, "rev-parse", newTagName)...))
	testhelper.MustRunCommand(t, nil, "git", append(baseArgs, "update-ref", "-d", "refs/tags/"+newTagName)...)

	fsckBefore := testhelper.MustRunCommand(t, nil, "git", append(baseArgs, "fsck", "--connectivity-only", "--dangling")...)
	fsckBeforeLines := strings.Split(string(fsckBefore), "\n")

	for _, l := range []string{
		fmt.Sprintf("dangling blob %s", newBlob),
		fmt.Sprintf("dangling tree %s", newTree),
		fmt.Sprintf("dangling commit %s", newCommit),
		fmt.Sprintf("dangling tag %s", newTag),
	} {
		require.Contains(t, fsckBeforeLines, l)
	}

	require.NoError(t, pool.FetchFromOrigin(ctx, source), "second fetch")

	refsArgs := append(baseArgs, "for-each-ref", "--format=%(refname) %(objectname)")
	refsAfter := testhelper.MustRunCommand(t, nil, "git", refsArgs...)
	refsAfterLines := strings.Split(string(refsAfter), "\n")
	for _, id := range []string{newBlob, newTree, newCommit, newTag} {
		require.Contains(t, refsAfterLines, fmt.Sprintf("refs/dangling/%s %s", id, id))
	}
}
