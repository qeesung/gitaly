package gitaly

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnpackAuxiliaryBinaries_success(t *testing.T) {
	destinationDir := t.TempDir()
	require.NoError(t, UnpackAuxiliaryBinaries(destinationDir))

	entries, err := os.ReadDir(destinationDir)
	require.NoError(t, err)

	require.Greater(t, len(entries), 1, "expected multiple packed binaries present")

	for _, entry := range entries {
		fileInfo, err := entry.Info()
		require.NoError(t, err)
		require.Equal(t, fileInfo.Mode(), os.FileMode(0o700), "expected the owner to have rwx permissions on the unpacked binary")

		sourceBinary, err := os.ReadFile(filepath.Join(buildDir, fileInfo.Name()))
		require.NoError(t, err)

		unpackedBinary, err := os.ReadFile(filepath.Join(destinationDir, fileInfo.Name()))
		require.NoError(t, err)

		require.Equal(t, sourceBinary, unpackedBinary, "unpacked binary does not match the source binary")
	}
}

func TestUnpackAuxiliaryBinaries_alreadyExists(t *testing.T) {
	destinationDir := t.TempDir()

	existingFile := filepath.Join(destinationDir, "gitaly-hooks")
	require.NoError(t, os.WriteFile(existingFile, []byte("existing file"), os.ModePerm))

	err := UnpackAuxiliaryBinaries(destinationDir)
	require.EqualError(t, err, fmt.Sprintf(`open %s: file exists`, existingFile), "expected unpacking to fail if destination binary already existed")
}
