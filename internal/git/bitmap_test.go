package git

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
)

func TestNumBitmaps(t *testing.T) {
	_, repoPath, cleanup := testhelper.NewTestRepo(t)
	defer cleanup()

	packDir := filepath.Join(repoPath, "objects/pack")
	entries, err := ioutil.ReadDir(packDir)
	require.NoError(t, err)

	for _, ent := range entries {
		name := ent.Name()
		if strings.HasPrefix(name, "pack-") && strings.HasSuffix(name, ".bitmap") {
			require.NoError(t, os.Remove(filepath.Join(packDir, name)))
		}
	}

	result, err := numBitmaps(repoPath)
	require.NoError(t, err)
	require.Equal(t, 0, result)

	inRepo := []string{"-C", repoPath}

	testhelper.MustRunCommand(t, nil, "git", append(inRepo, "repack", "-adb")...)
	result, err = numBitmaps(repoPath)
	require.NoError(t, err)
	require.Equal(t, 1, result)

	fakeBitmap := filepath.Join(repoPath, "objects/pack/pack-000000.bitmap")
	require.NoError(t, ioutil.WriteFile(fakeBitmap, nil, 0644))

	result, err = numBitmaps(repoPath)
	require.NoError(t, err)
	require.Equal(t, 2, result)
}
