package tempdir

import (
	"io/ioutil"
	"os"
	"path"
	"testing"

	pb "gitlab.com/gitlab-org/gitaly-proto/go"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"

	"github.com/stretchr/testify/require"
)

func TestNewSuccess(t *testing.T) {
	repo := testhelper.TestRepository()

	tempDir, err := New(repo)
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	err = ioutil.WriteFile(path.Join(tempDir, "test"), []byte("hello"), 0644)
	require.NoError(t, err, "write file in tempdir")

	require.NoError(t, os.RemoveAll(tempDir), "remove tempdir")
}

func TestNewFailStorageUnknown(t *testing.T) {
	_, err := New(&pb.Repository{StorageName: "does-not-exist", RelativePath: "foobar.git"})
	require.Error(t, err)
}
