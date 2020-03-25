package stats

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestRepositoryProfile(t *testing.T) {
	testRepo, testRepoPath, cleanup := testhelper.InitBareRepo(t)
	defer cleanup()

	ctx, cancel := testhelper.Context()
	defer cancel()

	profile, err := GetProfile(ctx, testRepo)
	require.NoError(t, err)

	require.False(t, profile.HasBitmap(), "repository should not have a bitmap initially")
	require.Zero(t, profile.UnpackedObjects())
	require.Zero(t, profile.Packfiles())

	blobs := 10
	blobIDs := writeBlobs(t, testRepoPath, blobs)

	profile, err = GetProfile(ctx, testRepo)
	require.NoError(t, err)
	require.Equal(t, int64(blobs), profile.UnpackedObjects())
	require.Equal(t, int64(blobs), profile.LooseObjects())

	for _, blobID := range blobIDs {
		commitID := testhelper.CommitBlobWithName(t, testRepoPath, blobID, blobID, "adding another blob....")
		testhelper.MustRunCommand(t, nil, "git", "-C", testRepoPath, "update-ref", "refs/heads/"+blobID, commitID)
	}

	// write a loose object
	writeBlobs(t, testRepoPath, 1)

	testhelper.MustRunCommand(t, nil, "git", "-C", testRepoPath, "repack", "-A", "-b", "-d")

	profile, err = GetProfile(ctx, testRepo)
	require.NoError(t, err)
	require.Zero(t, profile.UnpackedObjects())
	require.Equal(t, int64(1), profile.LooseObjects())

	// let a ms elapse for the OS to recognize the blobs have been written after the packfile
	time.Sleep(1 * time.Millisecond)

	// write another loose object
	blobID := writeBlobs(t, testRepoPath, 1)[0]

	// due to OS semantics, ensure that the blob has a timestamp that is after the packfile
	theFuture := time.Now().Add(10 * time.Minute)
	require.NoError(t, os.Chtimes(filepath.Join(testRepoPath, "objects", blobID[0:2], blobID[2:]), theFuture, theFuture))

	profile, err = GetProfile(ctx, testRepo)
	require.NoError(t, err)
	require.Equal(t, int64(1), profile.UnpackedObjects())
	require.Equal(t, int64(2), profile.LooseObjects())
}

func writeBlobs(t *testing.T, testRepoPath string, n int) []string {
	var blobIDs []string
	for i := 0; i < n; i++ {
		var stdin bytes.Buffer
		stdin.Write([]byte(strconv.Itoa(time.Now().Nanosecond())))
		blobIDs = append(blobIDs, text.ChompBytes(testhelper.MustRunCommand(t, &stdin, "git", "-C", testRepoPath, "hash-object", "-w", "--stdin")))
	}

	return blobIDs
}
