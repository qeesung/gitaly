package storage

import (
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/config"
	"gitlab.com/gitlab-org/gitaly/internal/tempdir"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestDeleteAllSuccess(t *testing.T) {
	gitalyDataFile := path.Join(testStorage.Path, tempdir.GitalyDataPrefix+"/foobar")
	require.NoError(t, os.MkdirAll(path.Dir(gitalyDataFile), 0755))
	require.NoError(t, ioutil.WriteFile(gitalyDataFile, nil, 0644))

	repoPaths := []string{
		"foo/bar1.git",
		"foo/bar2.git",
		"baz/foo/qux3.git",
		"baz/foo/bar1.git",
	}

	for _, p := range repoPaths {
		fullPath := path.Join(testStorage.Path, p)
		require.NoError(t, os.MkdirAll(fullPath, 0755))
		require.NoError(t, exec.Command("git", "init", "--bare", fullPath).Run())
	}

	require.Len(t, storageDirents(t, testStorage), 3, "there should be directory entries in test storage")

	server, socketPath := runStorageServer(t)
	defer server.Stop()

	client, conn := newStorageClient(t, socketPath)
	defer conn.Close()

	ctx, cancel := testhelper.Context()
	defer cancel()
	_, err := client.DeleteAllRepositories(ctx, &pb.DeleteAllRepositoriesRequest{StorageName: testStorage.Name})
	require.NoError(t, err)

	require.Len(t, storageDirents(t, testStorage), 1, "there should be no more git directory entries in test storage")
	_, err = os.Stat(gitalyDataFile)
	require.NoError(t, err)
}

func storageDirents(t *testing.T, st config.Storage) []os.FileInfo {
	dirents, err := ioutil.ReadDir(testStorage.Path)
	require.NoError(t, err)
	return dirents
}
