package gittest

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
)

// TreeEntry represents an entry of a git tree object.
type TreeEntry struct {
	// OID is the object ID the tree entry refers to.
	OID git.ObjectID
	// Mode is the file mode of the tree entry.
	Mode string
	// Path is the full path of the tree entry.
	Path string
	// Content is the content of the tree entry.
	Content string
}

// RequireTree looks up the given treeish and asserts that its entries match
// the given expected entries. Tree entries are checked recursively.
func RequireTree(tb testing.TB, cfg config.Cfg, repoPath, treeish string, expectedEntries []TreeEntry) {
	tb.Helper()

	for i, entry := range expectedEntries {
		if entry.OID != "" {
			continue
		}

		blob := fmt.Sprintf("blob %d\000%s", len(entry.Content), entry.Content)

		hasher := DefaultObjectHash.Hash()
		_, err := hasher.Write([]byte(blob))
		require.NoError(tb, err)

		expectedEntries[i].OID = git.ObjectID(hex.EncodeToString(hasher.Sum(nil)))
	}

	var actualEntries []TreeEntry

	output := bytes.TrimSpace(Exec(tb, cfg, "-C", repoPath, "ls-tree", "-r", treeish))

	if len(output) > 0 {
		for _, line := range bytes.Split(output, []byte("\n")) {
			// Format: <mode> SP <type> SP <object> TAB <file>
			tabSplit := bytes.Split(line, []byte("\t"))
			require.Len(tb, tabSplit, 2)

			spaceSplit := bytes.Split(tabSplit[0], []byte(" "))
			require.Len(tb, spaceSplit, 3)

			path := string(tabSplit[1])

			objectID, err := DefaultObjectHash.FromHex(string(spaceSplit[2]))
			require.NoError(tb, err)

			actualEntries = append(actualEntries, TreeEntry{
				OID:     objectID,
				Mode:    string(spaceSplit[0]),
				Path:    path,
				Content: string(Exec(tb, cfg, "-C", repoPath, "show", treeish+":"+path)),
			})
		}
	}

	require.Equal(tb, expectedEntries, actualEntries)
}

// WriteTree writes a new tree object to the given path. This function does not verify whether OIDs
// referred to by tree entries actually exist in the repository.
func WriteTree(tb testing.TB, cfg config.Cfg, repoPath string, entries []TreeEntry) git.ObjectID {
	tb.Helper()

	var tree bytes.Buffer
	for _, entry := range entries {
		var entryType string
		switch entry.Mode {
		case "100644":
			entryType = "blob"
		case "100755":
			entryType = "blob"
		case "040000":
			entryType = "tree"
		case "160000":
			entryType = "commit"
		default:
			tb.Fatalf("invalid entry type %q", entry.Mode)
		}

		require.False(tb, len(entry.OID) > 0 && len(entry.Content) > 0,
			"entry cannot have both OID and content")
		require.False(tb, len(entry.OID) == 0 && len(entry.Content) == 0,
			"entry must have either an OID or content")

		oid := entry.OID
		if len(entry.Content) > 0 {
			oid = WriteBlob(tb, cfg, repoPath, []byte(entry.Content))
		}

		formattedEntry := fmt.Sprintf("%s %s %s\t%s\000", entry.Mode, entryType, oid.String(), entry.Path)
		_, err := tree.WriteString(formattedEntry)
		require.NoError(tb, err)
	}

	stdout := ExecOpts(tb, cfg, ExecConfig{Stdin: &tree},
		"-C", repoPath, "mktree", "-z", "--missing",
	)
	treeOID, err := DefaultObjectHash.FromHex(text.ChompBytes(stdout))
	require.NoError(tb, err)

	return treeOID
}
