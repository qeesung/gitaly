//go:build !gitaly_test_sha256

package repository

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/stats"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func getNewestPackfileModtime(t *testing.T, repoPath string) time.Time {
	t.Helper()

	packFiles, err := filepath.Glob(filepath.Join(repoPath, "objects", "pack", "*.pack"))
	require.NoError(t, err)
	if len(packFiles) == 0 {
		t.Error("no packfiles exist")
	}

	var newestPackfileModtime time.Time

	for _, packFile := range packFiles {
		info, err := os.Stat(packFile)
		require.NoError(t, err)
		if info.ModTime().After(newestPackfileModtime) {
			newestPackfileModtime = info.ModTime()
		}
	}

	return newestPackfileModtime
}

func TestOptimizeRepository(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repoProto, repoPath, client := setupRepositoryService(ctx, t)

	gittest.Exec(t, cfg, "-C", repoPath, "repack", "-A", "-b")
	gittest.Exec(t, cfg, "-C", repoPath, "commit-graph", "write", "--size-multiple=4", "--split=replace", "--reachable", "--changed-paths")

	hasBitmap, err := stats.HasBitmap(repoPath)
	require.NoError(t, err)
	require.True(t, hasBitmap, "expect a bitmap since we just repacked with -b")

	missingBloomFilters, err := stats.IsMissingBloomFilters(repoPath)
	require.NoError(t, err)
	require.False(t, missingBloomFilters)

	// get timestamp of latest packfile
	newestsPackfileTime := getNewestPackfileModtime(t, repoPath)

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("master"))

	gittest.Exec(t, cfg, "-C", repoPath, "config", "http.http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c1.git.extraHeader", "Authorization: Basic secret-password")
	gittest.Exec(t, cfg, "-C", repoPath, "config", "http.http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c2.git.extraHeader", "Authorization: Basic secret-password")
	gittest.Exec(t, cfg, "-C", repoPath, "config", "randomStart-http.http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c3.git.extraHeader", "Authorization: Basic secret-password")
	gittest.Exec(t, cfg, "-C", repoPath, "config", "http.http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c4.git.extraHeader-randomEnd", "Authorization: Basic secret-password")
	gittest.Exec(t, cfg, "-C", repoPath, "config", "hTTp.http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c5.git.ExtrAheaDeR", "Authorization: Basic secret-password")
	gittest.Exec(t, cfg, "-C", repoPath, "config", "http.http://extraHeader/extraheader/EXTRAHEADER.git.extraHeader", "Authorization: Basic secret-password")
	gittest.Exec(t, cfg, "-C", repoPath, "config", "https.https://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c5.git.extraHeader", "Authorization: Basic secret-password")
	confFileData := testhelper.MustReadFile(t, filepath.Join(repoPath, "config"))
	require.True(t, bytes.Contains(confFileData, []byte("http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c1.git")))
	require.True(t, bytes.Contains(confFileData, []byte("http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c2.git")))
	require.True(t, bytes.Contains(confFileData, []byte("http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c3")))
	require.True(t, bytes.Contains(confFileData, []byte("http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c4.git")))
	require.True(t, bytes.Contains(confFileData, []byte("http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c5.git")))
	require.True(t, bytes.Contains(confFileData, []byte("http://extraHeader/extraheader/EXTRAHEADER.git")))
	require.True(t, bytes.Contains(confFileData, []byte("https://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c5.git")))

	_, err = client.OptimizeRepository(ctx, &gitalypb.OptimizeRepositoryRequest{Repository: repoProto})
	require.NoError(t, err)

	confFileData = testhelper.MustReadFile(t, filepath.Join(repoPath, "config"))
	require.False(t, bytes.Contains(confFileData, []byte("http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c1.git")))
	require.False(t, bytes.Contains(confFileData, []byte("http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c2.git")))
	require.True(t, bytes.Contains(confFileData, []byte("http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c3")))
	require.True(t, bytes.Contains(confFileData, []byte("http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c4.git")))
	require.False(t, bytes.Contains(confFileData, []byte("http://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c5.git")))
	require.False(t, bytes.Contains(confFileData, []byte("http://extraHeader/extraheader/EXTRAHEADER.git.extraHeader")))
	require.True(t, bytes.Contains(confFileData, []byte("https://localhost:51744/60631c8695bf041a808759a05de53e36a73316aacb502824fabbb0c6055637c5.git")))

	require.Equal(t, getNewestPackfileModtime(t, repoPath), newestsPackfileTime, "there should not have been a new packfile created")

	testRepoProto, testRepoPath := gittest.CreateRepository(ctx, t, cfg)

	blobs := 10
	blobIDs := gittest.WriteBlobs(t, cfg, testRepoPath, blobs)

	for _, blobID := range blobIDs {
		gittest.WriteCommit(t, cfg, testRepoPath,
			gittest.WithTreeEntries(gittest.TreeEntry{
				OID: git.ObjectID(blobID), Mode: "100644", Path: "blob",
			}),
			gittest.WithBranch(blobID),
			gittest.WithParents(),
		)
	}

	// Write a blob whose OID is known to have a "17" prefix, which is required such that
	// OptimizeRepository would try to repack at all.
	blobOIDWith17Prefix := gittest.WriteBlob(t, cfg, testRepoPath, []byte("32"))
	require.True(t, strings.HasPrefix(blobOIDWith17Prefix.String(), "17"))

	bitmaps, err := filepath.Glob(filepath.Join(testRepoPath, "objects", "pack", "*.bitmap"))
	require.NoError(t, err)
	require.Empty(t, bitmaps)

	mrRefs := filepath.Join(testRepoPath, "refs/merge-requests")
	emptyRef := filepath.Join(mrRefs, "1")
	require.NoError(t, os.MkdirAll(emptyRef, 0o755))
	require.DirExists(t, emptyRef, "sanity check for empty ref dir existence")

	// optimize repository on a repository without a bitmap should call repack full
	_, err = client.OptimizeRepository(ctx, &gitalypb.OptimizeRepositoryRequest{Repository: testRepoProto})
	require.NoError(t, err)

	bitmaps, err = filepath.Glob(filepath.Join(testRepoPath, "objects", "pack", "*.bitmap"))
	require.NoError(t, err)
	require.NotEmpty(t, bitmaps)

	missingBloomFilters, err = stats.IsMissingBloomFilters(testRepoPath)
	require.NoError(t, err)
	require.False(t, missingBloomFilters)

	// Empty directories should exist because they're too recent.
	require.DirExists(t, emptyRef)
	require.DirExists(t, mrRefs)
	require.FileExists(t,
		filepath.Join(testRepoPath, "refs/heads", blobIDs[0]),
		"unpacked refs should never be removed",
	)

	// Change the modification time to me older than a day and retry the call. Empty
	// directories must now be deleted.
	oneDayAgo := time.Now().Add(-24 * time.Hour)
	require.NoError(t, os.Chtimes(emptyRef, oneDayAgo, oneDayAgo))
	require.NoError(t, os.Chtimes(mrRefs, oneDayAgo, oneDayAgo))

	_, err = client.OptimizeRepository(ctx, &gitalypb.OptimizeRepositoryRequest{Repository: testRepoProto})
	require.NoError(t, err)

	require.NoFileExists(t, emptyRef)
	require.NoFileExists(t, mrRefs)
}

func TestOptimizeRepositoryValidation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, repo, _, client := setupRepositoryService(ctx, t)

	testCases := []struct {
		desc string
		repo *gitalypb.Repository
		exp  error
	}{
		{
			desc: "empty repository",
			repo: nil,
			exp:  status.Error(codes.InvalidArgument, gitalyOrPraefect("empty Repository", "repo scoped: empty Repository")),
		},
		{
			desc: "invalid repository storage",
			repo: &gitalypb.Repository{StorageName: "non-existent", RelativePath: repo.GetRelativePath()},
			exp:  status.Error(codes.InvalidArgument, gitalyOrPraefect(`GetStorageByName: no such storage: "non-existent"`, "repo scoped: invalid Repository")),
		},
		{
			desc: "invalid repository path",
			repo: &gitalypb.Repository{StorageName: repo.GetStorageName(), RelativePath: "path/not/exist"},
			exp: status.Error(
				codes.NotFound,
				gitalyOrPraefect(
					fmt.Sprintf(`GetRepoPath: not a git repository: "%s/path/not/exist"`, cfg.Storages[0].Path),
					`routing repository maintenance: getting repository metadata: repository not found`,
				),
			),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			_, err := client.OptimizeRepository(ctx, &gitalypb.OptimizeRepositoryRequest{Repository: tc.repo})
			require.Error(t, err)
			testhelper.RequireGrpcError(t, tc.exp, err)
		})
	}

	_, err := client.OptimizeRepository(ctx, &gitalypb.OptimizeRepositoryRequest{Repository: repo})
	require.NoError(t, err)
}
