package repository

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
)

func TestSuccessfullBackupCustomHooksRequest(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)
	_, repo, repoPath, client := setupRepositoryService(ctx, t)

	expectedTarResponse := []string{
		"custom_hooks/",
		"custom_hooks/pre-commit.sample",
		"custom_hooks/prepare-commit-msg.sample",
		"custom_hooks/pre-push.sample",
	}
	require.NoError(t, os.Mkdir(filepath.Join(repoPath, "custom_hooks"), 0o700), "Could not create custom_hooks dir")
	for _, fileName := range expectedTarResponse[1:] {
		require.NoError(t, os.WriteFile(filepath.Join(repoPath, fileName), []byte("Some hooks"), 0o700), fmt.Sprintf("Could not create %s", fileName))
	}

	backupRequest := &gitalypb.BackupCustomHooksRequest{Repository: repo}
	backupStream, err := client.BackupCustomHooks(ctx, backupRequest)
	require.NoError(t, err)

	reader := tar.NewReader(streamio.NewReader(func() ([]byte, error) {
		response, err := backupStream.Recv()
		return response.GetData(), err
	}))

	fileLength := 0
	for {
		file, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		fileLength++
		require.Contains(t, expectedTarResponse, file.Name)
	}
	require.Equal(t, fileLength, len(expectedTarResponse))
}

func TestSuccessfullBackupCustomHooksSymlink(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)
	_, repo, repoPath, client := setupRepositoryService(ctx, t)

	linkTarget := "/var/empty"
	require.NoError(t, os.Symlink(linkTarget, filepath.Join(repoPath, "custom_hooks")), "Could not create custom_hooks symlink")

	backupRequest := &gitalypb.BackupCustomHooksRequest{Repository: repo}
	backupStream, err := client.BackupCustomHooks(ctx, backupRequest)
	require.NoError(t, err)

	reader := tar.NewReader(streamio.NewReader(func() ([]byte, error) {
		response, err := backupStream.Recv()
		return response.GetData(), err
	}))

	file, err := reader.Next()
	require.NoError(t, err)

	require.Equal(t, "custom_hooks", file.Name, "tar entry name")
	require.Equal(t, byte(tar.TypeSymlink), file.Typeflag, "tar entry type")
	require.Equal(t, linkTarget, file.Linkname, "link target")

	_, err = reader.Next()
	require.Equal(t, io.EOF, err, "custom_hooks should have been the only entry")
}

func TestSuccessfullBackupCustomHooksRequestWithNoHooks(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)
	_, repo, _, client := setupRepositoryService(ctx, t)

	backupRequest := &gitalypb.BackupCustomHooksRequest{Repository: repo}
	backupStream, err := client.BackupCustomHooks(ctx, backupRequest)
	require.NoError(t, err)

	reader := streamio.NewReader(func() ([]byte, error) {
		response, err := backupStream.Recv()
		return response.GetData(), err
	})

	buf := bytes.NewBuffer(nil)
	_, err = io.Copy(buf, reader)
	require.NoError(t, err)

	require.Empty(t, buf.String(), "Returned stream should be empty")
}
