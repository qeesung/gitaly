package archive

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
)

func TestWriteTarball(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	tempDir := testhelper.TempDir(t)
	srcDir := filepath.Join(tempDir, "src")

	// regular file
	writeFile(t, filepath.Join(srcDir, "a.txt"), []byte("a"))
	// empty dir
	require.NoError(t, os.Mkdir(filepath.Join(srcDir, "empty_dir"), perm.PrivateDir))
	// file with long name
	writeFile(t, filepath.Join(srcDir, strings.Repeat("b", 150)+".txt"), []byte("b"))
	// regular file that is not expected to be part of the archive (not in the members list)
	writeFile(t, filepath.Join(srcDir, "excluded.txt"), []byte("excluded"))
	// folder with multiple files all expected to be archived
	nestedPath := filepath.Join(srcDir, "nested1")
	for i := 0; i < readDirEntriesPageSize+1; i++ {
		writeFile(t, filepath.Join(nestedPath, fmt.Sprintf("%d.txt", i)), []byte{byte(i)})
	}
	// nested file that is not expected to be part of the archive
	writeFile(t, filepath.Join(srcDir, "nested2/nested/nested/c.txt"), []byte("c"))
	// deeply nested file
	writeFile(t, filepath.Join(srcDir, "nested2/nested/nested/nested/nested/d.txt"), []byte("d"))
	// file that is used to create a symbolic link, is not expected to be part of the archive
	writeFile(t, filepath.Join(srcDir, "nested3/target.txt"), []byte("target"))
	// link to the file above
	require.NoError(t, os.Symlink(filepath.Join(srcDir, "nested3/target.txt"), filepath.Join(srcDir, "link.to.target.txt")))
	// directory that is a target of the symlink should not be archived
	writeFile(t, filepath.Join(srcDir, "nested4/stub.txt"), []byte("symlinked"))
	// link to the folder above
	require.NoError(t, os.Symlink(filepath.Join(srcDir, "nested4"), filepath.Join(srcDir, "link.to.nested4")))

	tarPath := filepath.Join(tempDir, "out.tar")
	archFile, err := os.Create(tarPath)
	require.NoError(t, err)
	defer func() { _ = archFile.Close() }()

	err = writeTarball(
		ctx,
		archFile,
		srcDir,
		"a.txt",
		strings.Repeat("b", 150)+".txt",
		"nested1",
		"nested2/nested/nested/nested/nested/d.txt",
		"link.to.target.txt",
		"link.to.nested4",
	)
	require.NoError(t, err)
	require.NoError(t, archFile.Close())

	cfg := testcfg.Build(t)

	dstDir := filepath.Join(tempDir, "dst")
	require.NoError(t, os.Mkdir(dstDir, perm.PrivateDir))
	output, err := exec.Command("tar", "-xf", tarPath, "-C", dstDir).CombinedOutput()
	require.NoErrorf(t, err, "%s", output)
	diff := gittest.ExecOpts(t, cfg, gittest.ExecConfig{ExpectedExitCode: 1}, "diff", "--no-index", "--name-only", "--exit-code", dstDir, srcDir)
	expected := strings.Join(
		[]string{
			filepath.Join(srcDir, "excluded.txt"),
			filepath.Join(srcDir, "nested2/nested/nested/c.txt"),
			filepath.Join(srcDir, "nested3/target.txt"),
			filepath.Join(srcDir, "nested4/stub.txt\n"),
		},
		"\n",
	)
	require.Equal(t, expected, string(diff))
}

func writeFile(tb testing.TB, path string, data []byte) {
	tb.Helper()
	require.NoError(tb, os.MkdirAll(filepath.Dir(path), perm.PrivateDir))
	require.NoError(tb, os.WriteFile(path, data, perm.PrivateWriteOnceFile))
}
