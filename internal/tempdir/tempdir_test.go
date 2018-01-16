package tempdir

import (
	"io/ioutil"
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
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
