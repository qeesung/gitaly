package lstree

import (
	"bytes"
	"io"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

func TestParser(t *testing.T) {
	t.Parallel()

	cfg := testcfg.Build(t)
	_, repoPath := gittest.InitRepo(t, cfg, cfg.Storages[0])

	gitignoreBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("gitignore"))
	gitmodulesBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("gitmodules"))
	submoduleCommitID := gittest.WriteCommit(t, cfg, repoPath)

	regularEntriesTreeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: ".gitignore", Mode: "100644", OID: gitignoreBlobID},
		{Path: ".gitmodules", Mode: "100644", OID: gitmodulesBlobID},
		{Path: "entry with space", Mode: "040000", OID: gittest.DefaultObjectHash.EmptyTreeOID},
		{Path: "gitlab-shell", Mode: "160000", OID: submoduleCommitID},
	})

	for _, tc := range []struct {
		desc            string
		treeID          git.ObjectID
		expectedEntries Entries
	}{
		{
			desc:   "regular entries",
			treeID: regularEntriesTreeID,
			expectedEntries: Entries{
				{
					Mode:     []byte("100644"),
					Type:     Blob,
					ObjectID: gitignoreBlobID,
					Path:     ".gitignore",
				},
				{
					Mode:     []byte("100644"),
					Type:     Blob,
					ObjectID: gitmodulesBlobID,
					Path:     ".gitmodules",
				},
				{
					Mode:     []byte("040000"),
					Type:     Tree,
					ObjectID: gittest.DefaultObjectHash.EmptyTreeOID,
					Path:     "entry with space",
				},
				{
					Mode:     []byte("160000"),
					Type:     Submodule,
					ObjectID: submoduleCommitID,
					Path:     "gitlab-shell",
				},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			treeData := gittest.Exec(t, cfg, "-C", repoPath, "ls-tree", "-z", tc.treeID.String())

			parser := NewParser(bytes.NewReader(treeData), gittest.DefaultObjectHash)
			parsedEntries := Entries{}
			for {
				entry, err := parser.NextEntry()
				if err == io.EOF {
					break
				}

				require.NoError(t, err)
				parsedEntries = append(parsedEntries, *entry)
			}

			require.Equal(t, tc.expectedEntries, parsedEntries)
		})
	}
}
