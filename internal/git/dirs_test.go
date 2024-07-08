package git

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

func TestObjectDirs(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	logger := testhelper.NewLogger(t)

	altObjDirs := []string{
		"testdata/objdirs/repo1/objects",
		"testdata/objdirs/repo2/objects",
		"testdata/objdirs/repo3/objects",
		"testdata/objdirs/repo4/objects",
		"testdata/objdirs/repo5/objects",
		"testdata/objdirs/repoB/objects",
	}

	repo := "testdata/objdirs/repo0"
	objDirs := append([]string{filepath.Join(repo, "objects")}, altObjDirs...)

	out, err := ObjectDirectories(ctx, logger, "testdata/objdirs", repo)
	require.NoError(t, err)
	require.Equal(t, objDirs, out)

	out, err = AlternateObjectDirectories(ctx, logger, "testdata/objdirs", repo)
	require.NoError(t, err)
	require.Equal(t, altObjDirs, out)
}

func TestObjectDirsNoAlternates(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	logger := testhelper.NewLogger(t)

	repo := "testdata/objdirs/no-alternates"
	out, err := ObjectDirectories(ctx, logger, "testdata/objdirs", repo)
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(repo, "objects")}, out)

	out, err = AlternateObjectDirectories(ctx, logger, "testdata/objdirs", repo)
	require.NoError(t, err)
	require.Equal(t, []string{}, out)
}

func TestObjectDirsOutsideStorage(t *testing.T) {
	t.Parallel()

	tmp := testhelper.TempDir(t)
	logger := testhelper.NewLogger(t)

	storageRoot := filepath.Join(tmp, "storage-root")
	repoPath := filepath.Join(storageRoot, "repo")
	alternatesFile := filepath.Join(repoPath, "objects", "info", "alternates")
	altObjDir := filepath.Join(tmp, "outside-storage-sibling", "objects")
	require.NoError(t, os.MkdirAll(filepath.Dir(alternatesFile), perm.PrivateDir))
	expectedErr := alternateOutsideStorageError(altObjDir)

	for _, tc := range []struct {
		desc       string
		alternates string
	}{
		{
			desc:       "relative path",
			alternates: "../../../outside-storage-sibling/objects",
		},
		{
			desc:       "absolute path",
			alternates: altObjDir,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			require.NoError(t, os.WriteFile(alternatesFile, []byte(tc.alternates), perm.PrivateWriteOnceFile))
			out, err := ObjectDirectories(ctx, logger, storageRoot, repoPath)
			require.Equal(t, expectedErr, err)
			require.Nil(t, out)
		})
	}
}
