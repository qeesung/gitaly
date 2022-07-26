package gittest

import (
	"strings"
	"testing"

	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
)

func TestWriteTree(t *testing.T) {
	cfg, _, repoPath := setup(t)

	differentContentBlobID := WriteBlob(t, cfg, repoPath, []byte("different content"))
	blobID := WriteBlob(t, cfg, repoPath, []byte("foobar\n"))
	treeID := WriteTree(t, cfg, repoPath, []TreeEntry{
		{
			OID:  blobID,
			Mode: "100644",
			Path: "file",
		},
	})

	for _, tc := range []struct {
		desc            string
		entries         []TreeEntry
		expectedEntries []TreeEntry
	}{
		{
			desc: "entry with blob OID",
			entries: []TreeEntry{
				{
					OID:  blobID,
					Mode: "100644",
					Path: "file",
				},
			},
			expectedEntries: []TreeEntry{
				{
					OID:     blobID,
					Content: "foobar\n",
					Mode:    "100644",
					Path:    "file",
				},
			},
		},
		{
			desc: "entry with blob content",
			entries: []TreeEntry{
				{
					Content: "foobar\n",
					Mode:    "100644",
					Path:    "file",
				},
			},
			expectedEntries: []TreeEntry{
				{
					OID:     blobID,
					Content: "foobar\n",
					Mode:    "100644",
					Path:    "file",
				},
			},
		},
		{
			desc: "entry with tree OID",
			entries: []TreeEntry{
				{
					OID:  treeID,
					Mode: "040000",
					Path: "dir",
				},
			},
			expectedEntries: []TreeEntry{
				{
					OID:     blobID,
					Content: "foobar\n",
					Mode:    "100644",
					Path:    "dir/file",
				},
			},
		},
		{
			desc: "mixed tree and blob entries",
			entries: []TreeEntry{
				{
					OID:  treeID,
					Mode: "040000",
					Path: "dir",
				},
				{
					OID:  blobID,
					Mode: "100644",
					Path: "file1",
				},
				{
					Content: "different content",
					Mode:    "100644",
					Path:    "file2",
				},
			},
			expectedEntries: []TreeEntry{
				{
					OID:     blobID,
					Content: "foobar\n",
					Mode:    "100644",
					Path:    "dir/file",
				},
				{
					OID:     blobID,
					Content: "foobar\n",
					Mode:    "100644",
					Path:    "file1",
				},
				{
					OID:     differentContentBlobID,
					Content: "different content",
					Mode:    "100644",
					Path:    "file2",
				},
			},
		},
		{
			desc: "two entries with nonexistant objects",
			entries: []TreeEntry{
				{
					OID:  git.ObjectID(strings.Repeat("1", DefaultObjectHash.Hash().Size()*2)),
					Mode: "100644",
					Path: "file",
				},
				{
					OID:  DefaultObjectHash.ZeroOID,
					Mode: "100644",
					Path: "file",
				},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oid := WriteTree(t, cfg, repoPath, tc.entries)

			if tc.expectedEntries != nil {
				RequireTree(t, cfg, repoPath, oid.String(), tc.expectedEntries)
			}
		})
	}
}
