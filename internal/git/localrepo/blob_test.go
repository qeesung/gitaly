package localrepo

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/perm"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
)

type ReaderFunc func([]byte) (int, error)

func (fn ReaderFunc) Read(b []byte) (int, error) { return fn(b) }

func TestRepo_WriteBlob(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	_, repo, repoPath := setupRepo(t)

	for _, tc := range []struct {
		desc       string
		attributes string
		input      io.Reader
		sha        string
		error      error
		content    string
	}{
		{
			desc:  "error reading",
			input: ReaderFunc(func([]byte) (int, error) { return 0, assert.AnError }),
			error: structerr.New("writing blob: %w", assert.AnError),
		},
		{
			desc:    "successful empty blob",
			input:   strings.NewReader(""),
			content: "",
		},
		{
			desc:    "successful blob",
			input:   strings.NewReader("some content"),
			content: "some content",
		},
		{
			desc:    "LF line endings left unmodified",
			input:   strings.NewReader("\n"),
			content: "\n",
		},
		{
			desc:    "CRLF not converted to LF due to global git config",
			input:   strings.NewReader("\r\n"),
			content: "\r\n",
		},
		{
			desc:       "line endings preserved in binary files",
			input:      strings.NewReader("\r\n"),
			attributes: "file-path binary",
			content:    "\r\n",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			// Apply the gitattributes
			// We should get rid of this with https://gitlab.com/groups/gitlab-org/-/epics/9006
			attributesPath := filepath.Join(repoPath, "info", "attributes")
			require.NoError(t, os.MkdirAll(filepath.Dir(attributesPath), perm.PrivateDir))
			require.NoError(t, os.WriteFile(attributesPath, []byte(tc.attributes), perm.PrivateWriteOnceFile))

			sha, err := repo.WriteBlob(ctx, tc.input, WriteBlobConfig{
				Path: "file-path",
			})
			require.Equal(t, tc.error, err)
			if tc.error != nil {
				return
			}

			content, err := repo.ReadObject(ctx, sha)
			require.NoError(t, err)
			assert.Equal(t, tc.content, string(content))
		})
	}
}
