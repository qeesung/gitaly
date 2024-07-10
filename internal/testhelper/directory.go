package testhelper

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
)

// DirectoryEntry models an entry in a directory.
type DirectoryEntry struct {
	// Mode is the file mode of the entry.
	Mode fs.FileMode
	// Content contains the file content if this is a regular file.
	Content any
	// ParseContent is a function that receives the file's absolute path, actual content
	// and returns it parsed into the expected form. The returned value is ultimately
	// asserted for equality with the Content.
	ParseContent func(tb testing.TB, path string, content []byte) any
}

// DirectoryState models the contents of a directory. The key is relative of the entry in
// the rootDirectory as described on RequireDirectoryState.
type DirectoryState map[string]DirectoryEntry

// RequireDirectoryState asserts that given directory matches the expected state. The rootDirectory and
// relativeDirectory are joined together to decide the directory to walk. rootDirectory is trimmed out of the
// paths in the DirectoryState to make assertions easier by using the relative paths only. For example, given
// `/root-path` and `relative/path`, the directory walked is `/root-path/relative/path`. The paths in DirectoryState
// trim the root prefix, thus they should be like `/relative/path/...`. The beginning point of the walk has path "/".
func RequireDirectoryState(tb testing.TB, rootDirectory, relativeDirectory string, expected DirectoryState) {
	tb.Helper()

	actual := DirectoryState{}
	require.NoError(tb, filepath.WalkDir(filepath.Join(rootDirectory, relativeDirectory), func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		require.NoError(tb, err)

		trimmedPath := strings.TrimPrefix(path, rootDirectory)
		if trimmedPath == "" {
			// Store the walked directory itself as "/". Less confusing than having it be
			// an empty string.
			trimmedPath = string(os.PathSeparator)
		}

		info, err := entry.Info()
		require.NoError(tb, err)

		actualEntry := DirectoryEntry{
			Mode: info.Mode(),
		}

		if entry.Type().IsRegular() {
			content, err := os.ReadFile(path)
			require.NoError(tb, err)

			actualEntry.Content = content
			// Find prefect match or glob matching (/wal/1/pack*.pack, for example).
			for pattern, expectedEntry := range expected {
				matched, err := filepath.Match(pattern, trimmedPath)
				if err == nil && matched {
					if expectedEntry.ParseContent != nil {
						actualEntry.Content = expectedEntry.ParseContent(tb, path, content)
					}
					actual[pattern] = actualEntry
					return nil
				}
			}
		}

		actual[trimmedPath] = actualEntry
		return nil
	}))

	// Functions are never equal unless they are nil, see https://pkg.go.dev/reflect#DeepEqual.
	// So to check of equality we set the ParseContent functions to nil.
	// We use a copy so we don't unexpectedly modify the original.
	expectedCopy := make(DirectoryState, len(expected))
	for key, value := range expected {
		value.ParseContent = nil
		expectedCopy[key] = value
	}

	ProtoEqual(tb, expectedCopy, actual)
}

// RequireTarState asserts that the provided tarball contents matches the expected state.
func RequireTarState(tb testing.TB, tarball io.Reader, expected DirectoryState) {
	tb.Helper()

	actual := DirectoryState{}
	tr := tar.NewReader(tarball)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(tb, err)

		actualEntry := DirectoryEntry{
			Mode: fs.FileMode(header.Mode),
		}

		if header.Typeflag == tar.TypeReg {
			b, err := io.ReadAll(tr)
			require.NoError(tb, err)

			actualEntry.Content = b
		}

		actual[header.Name] = actualEntry
	}

	if expected == nil {
		expected = DirectoryState{}
	}

	ProtoEqual(tb, expected, actual)
}

// MustCreateCustomHooksTar creates a temporary custom hooks tar archive on disk
// for testing and returns its file path.
func MustCreateCustomHooksTar(tb testing.TB) io.Reader {
	tb.Helper()

	writeFile := func(writer *tar.Writer, path string, mode fs.FileMode, content string) {
		require.NoError(tb, writer.WriteHeader(&tar.Header{
			Name: path,
			Mode: int64(mode),
			Size: int64(len(content)),
		}))
		_, err := writer.Write([]byte(content))
		require.NoError(tb, err)
	}

	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	defer MustClose(tb, writer)

	require.NoError(tb, writer.WriteHeader(&tar.Header{Name: "custom_hooks/", Mode: int64(perm.PrivateDir)}))
	writeFile(writer, "custom_hooks/pre-commit", perm.PrivateExecutable, "pre-commit content")
	writeFile(writer, "custom_hooks/pre-push", perm.PrivateExecutable, "pre-push content")
	writeFile(writer, "custom_hooks/pre-receive", perm.PrivateExecutable, "pre-receive content")

	return &buffer
}

// CreateFS takes in an FS and creates its state on the actual filesystem at rootPath.
// fstest.MapFS is convenient type to use for building state.
func CreateFS(tb testing.TB, rootPath string, state fs.FS) {
	tb.Helper()

	require.NoError(tb, fs.WalkDir(state, ".", func(relativePath string, d fs.DirEntry, err error) error {
		require.NoError(tb, err)

		info, err := d.Info()
		require.NoError(tb, err)

		absolutePath := filepath.Join(rootPath, relativePath)
		if d.IsDir() {
			require.NoError(tb, os.Mkdir(absolutePath, info.Mode().Perm()))
			return nil
		}

		source, err := state.Open(relativePath)
		require.NoError(tb, err)
		defer MustClose(tb, source)

		destination, err := os.OpenFile(absolutePath, os.O_WRONLY|os.O_EXCL|os.O_CREATE, info.Mode().Perm())
		require.NoError(tb, err)
		defer MustClose(tb, destination)

		_, err = io.Copy(destination, source)
		require.NoError(tb, err)

		return nil
	}))
}
