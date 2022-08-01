package gittest

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
)

func TestWriteCommit(t *testing.T) {
	cfg, _, repoPath := setup(t)

	treeEntryA := TreeEntry{Path: "file", Mode: "100644", Content: "something"}

	treeA := WriteTree(t, cfg, repoPath, []TreeEntry{treeEntryA})
	treeB := WriteTree(t, cfg, repoPath, []TreeEntry{
		{Path: "file", Mode: "100644", Content: "changed"},
	})
	commitA := WriteCommit(t, cfg, repoPath, WithTree(treeA))
	commitB := WriteCommit(t, cfg, repoPath, WithTree(treeB))

	for _, tc := range []struct {
		desc                string
		opts                []WriteCommitOption
		expectedCommit      string
		expectedTreeEntries []TreeEntry
		expectedRevUpdate   git.Revision
	}{
		{
			desc: "no options",
			expectedCommit: strings.Join([]string{
				"tree " + DefaultObjectHash.EmptyTreeOID.String(),
				"author " + DefaultCommitterSignature,
				"committer " + DefaultCommitterSignature,
				"",
				"message",
			}, "\n"),
		},
		{
			desc: "with commit message",
			opts: []WriteCommitOption{
				WithMessage("my custom message\n\nfoobar\n"),
			},
			expectedCommit: strings.Join([]string{
				"tree " + DefaultObjectHash.EmptyTreeOID.String(),
				"author " + DefaultCommitterSignature,
				"committer " + DefaultCommitterSignature,
				"",
				"my custom message",
				"",
				"foobar",
			}, "\n"),
		},
		{
			desc: "with author",
			opts: []WriteCommitOption{
				WithAuthorName("John Doe"),
				WithAuthorDate(time.Date(2005, 4, 7, 15, 13, 13, 0, time.FixedZone("UTC-7", -7*60*60))),
			},
			expectedCommit: strings.Join([]string{
				"tree " + DefaultObjectHash.EmptyTreeOID.String(),
				"author John Doe <scrooge@mcduck.com> 1112911993 -0700",
				"committer " + DefaultCommitterSignature,
				"",
				"message",
			}, "\n"),
		},
		{
			desc: "with commiter",
			opts: []WriteCommitOption{
				WithCommitterName("John Doe"),
				WithCommitterDate(time.Date(2005, 4, 7, 15, 13, 13, 0, time.FixedZone("UTC-7", -7*60*60))),
			},
			expectedCommit: strings.Join([]string{
				"tree " + DefaultObjectHash.EmptyTreeOID.String(),
				"author " + DefaultCommitterSignature,
				"committer John Doe <scrooge@mcduck.com> 1112911993 -0700",
				"",
				"message",
			}, "\n"),
		},
		{
			desc: "with no parents",
			opts: []WriteCommitOption{
				WithParents(),
			},
			expectedCommit: strings.Join([]string{
				"tree " + DefaultObjectHash.EmptyTreeOID.String(),
				"author " + DefaultCommitterSignature,
				"committer " + DefaultCommitterSignature,
				"",
				"message",
			}, "\n"),
		},
		{
			desc: "with multiple parents",
			opts: []WriteCommitOption{
				WithParents(commitA, commitB),
			},
			expectedCommit: strings.Join([]string{
				"tree " + treeA.String(),
				"parent " + commitA.String(),
				"parent " + commitB.String(),
				"author " + DefaultCommitterSignature,
				"committer " + DefaultCommitterSignature,
				"",
				"message",
			}, "\n"),
		},
		{
			desc: "with branch",
			opts: []WriteCommitOption{
				WithBranch("foo"),
			},
			expectedCommit: strings.Join([]string{
				"tree " + DefaultObjectHash.EmptyTreeOID.String(),
				"author " + DefaultCommitterSignature,
				"committer " + DefaultCommitterSignature,
				"",
				"message",
			}, "\n"),
			expectedRevUpdate: "refs/heads/foo",
		},
		{
			desc: "with reference",
			opts: []WriteCommitOption{
				WithReference("refs/custom/namespace"),
			},
			expectedCommit: strings.Join([]string{
				"tree " + DefaultObjectHash.EmptyTreeOID.String(),
				"author " + DefaultCommitterSignature,
				"committer " + DefaultCommitterSignature,
				"",
				"message",
			}, "\n"),
			expectedRevUpdate: "refs/custom/namespace",
		},
		{
			desc: "with tree entry",
			opts: []WriteCommitOption{
				WithTreeEntries(treeEntryA),
			},
			expectedCommit: strings.Join([]string{
				"tree " + treeA.String(),
				"author " + DefaultCommitterSignature,
				"committer " + DefaultCommitterSignature,
				"",
				"message",
			}, "\n"),
			expectedTreeEntries: []TreeEntry{treeEntryA},
		},
		{
			desc: "with tree",
			opts: []WriteCommitOption{
				WithTree(treeA),
			},
			expectedCommit: strings.Join([]string{
				"tree " + treeA.String(),
				"author " + DefaultCommitterSignature,
				"committer " + DefaultCommitterSignature,
				"",
				"message",
			}, "\n"),
			expectedTreeEntries: []TreeEntry{
				{
					Content: "something",
					Mode:    "100644",
					Path:    "file",
				},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oid := WriteCommit(t, cfg, repoPath, tc.opts...)

			commit := Exec(t, cfg, "-C", repoPath, "cat-file", "-p", oid.String())

			require.Equal(t, tc.expectedCommit, text.ChompBytes(commit))

			if tc.expectedTreeEntries != nil {
				RequireTree(t, cfg, repoPath, oid.String(), tc.expectedTreeEntries)
			}

			if tc.expectedRevUpdate != "" {
				updatedOID := Exec(t, cfg, "-C", repoPath, "rev-parse", tc.expectedRevUpdate.String())
				require.Equal(t, oid, git.ObjectID(text.ChompBytes(updatedOID)))
			}
		})
	}
}
