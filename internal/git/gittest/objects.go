package gittest

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
)

// RequireObjectExists asserts that the given repository does contain an object with the specified
// object ID.
func RequireObjectExists(t testing.TB, cfg config.Cfg, repoPath string, objectID git.ObjectID) {
	requireObjectExists(t, cfg, repoPath, objectID, true)
}

// RequireObjectNotExists asserts that the given repository does not contain an object with the
// specified object ID.
func RequireObjectNotExists(t testing.TB, cfg config.Cfg, repoPath string, objectID git.ObjectID) {
	requireObjectExists(t, cfg, repoPath, objectID, false)
}

func requireObjectExists(t testing.TB, cfg config.Cfg, repoPath string, objectID git.ObjectID, exists bool) {
	cmd := NewCommand(t, cfg, "-C", repoPath, "cat-file", "-e", objectID.String())
	cmd.Env = []string{
		"GIT_ALLOW_PROTOCOL=", // To prevent partial clone reaching remote repo over SSH
	}

	if exists {
		require.NoError(t, cmd.Run(), "checking for object should succeed")
		return
	}
	require.Error(t, cmd.Run(), "checking for object should fail")
}

// GetGitPackfileDirSize gets the number of 1k blocks of a git object directory
func GetGitPackfileDirSize(t testing.TB, repoPath string) int64 {
	return getGitDirSize(t, repoPath, "objects", "pack")
}

func getGitDirSize(t testing.TB, repoPath string, subdirs ...string) int64 {
	cmd := exec.Command("du", "-s", "-k", filepath.Join(append([]string{repoPath}, subdirs...)...))
	output, err := cmd.Output()
	require.NoError(t, err)
	if len(output) < 2 {
		t.Error("invalid output of du -s -k")
	}

	outputSplit := strings.SplitN(string(output), "\t", 2)
	blocks, err := strconv.ParseInt(outputSplit[0], 10, 64)
	require.NoError(t, err)

	return blocks
}

// WriteBlobs writes n distinct blobs into the git repository's object
// database. Each object has the current time in nanoseconds as contents.
func WriteBlobs(t testing.TB, cfg config.Cfg, testRepoPath string, n int) []string {
	var blobIDs []string
	for i := 0; i < n; i++ {
		contents := []byte(strconv.Itoa(time.Now().Nanosecond()))
		blobIDs = append(blobIDs, WriteBlob(t, cfg, testRepoPath, contents).String())
	}

	return blobIDs
}

// WriteBlob writes the given contents as a blob into the repository and returns its OID.
func WriteBlob(t testing.TB, cfg config.Cfg, testRepoPath string, contents []byte) git.ObjectID {
	hex := text.ChompBytes(ExecOpts(t, cfg, ExecConfig{Stdin: bytes.NewReader(contents)},
		"-C", testRepoPath, "hash-object", "-w", "--stdin",
	))
	oid, err := DefaultObjectHash.FromHex(hex)
	require.NoError(t, err)
	return oid
}
