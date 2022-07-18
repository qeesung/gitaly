//go:build !gitaly_test_sha256

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

	defaultCommitter := "Scrooge McDuck <scrooge@mcduck.com> 1572776879 +0100"
	defaultParentID := "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863"

	revisions := map[git.Revision]git.ObjectID{
		"refs/heads/master":  "",
		"refs/heads/master~": "",
	}
	for revision := range revisions {
		oid := Exec(t, cfg, "-C", repoPath, "rev-parse", revision.String())
		revisions[revision] = git.ObjectID(text.ChompBytes(oid))
	}

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
				"tree 91639b9835ff541f312fd2735f639a50bf35d472",
				"parent " + defaultParentID,
				"author " + defaultCommitter,
				"committer " + defaultCommitter,
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
				"tree 91639b9835ff541f312fd2735f639a50bf35d472",
				"parent " + defaultParentID,
				"author " + defaultCommitter,
				"committer " + defaultCommitter,
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
				"tree 91639b9835ff541f312fd2735f639a50bf35d472",
				"parent " + defaultParentID,
				"author John Doe <scrooge@mcduck.com> 1112911993 -0700",
				"committer " + defaultCommitter,
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
				"tree 91639b9835ff541f312fd2735f639a50bf35d472",
				"parent " + defaultParentID,
				"author Scrooge McDuck <scrooge@mcduck.com> 1572776879 +0100",
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
				"tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904",
				"author " + defaultCommitter,
				"committer " + defaultCommitter,
				"",
				"message",
			}, "\n"),
		},
		{
			desc: "with multiple parents",
			opts: []WriteCommitOption{
				WithParents(revisions["refs/heads/master"], revisions["refs/heads/master~"]),
			},
			expectedCommit: strings.Join([]string{
				"tree 07f8147e8e73aab6c935c296e8cdc5194dee729b",
				"parent 1e292f8fedd741b75372e19097c76d327140c312",
				"parent 7975be0116940bf2ad4321f79d02a55c5f7779aa",
				"author " + defaultCommitter,
				"committer " + defaultCommitter,
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
				"tree 91639b9835ff541f312fd2735f639a50bf35d472",
				"parent " + defaultParentID,
				"author " + defaultCommitter,
				"committer " + defaultCommitter,
				"",
				"message",
			}, "\n"),
			expectedRevUpdate: "refs/heads/foo",
		},
		{
			desc: "with tree entry",
			opts: []WriteCommitOption{
				WithTreeEntries(TreeEntry{
					Content: "foobar",
					Mode:    "100644",
					Path:    "file",
				}),
			},
			expectedCommit: strings.Join([]string{
				"tree 0a2fde9f84d2642adbfdf7c37560005e2532fd31",
				"parent " + defaultParentID,
				"author " + defaultCommitter,
				"committer " + defaultCommitter,
				"",
				"message",
			}, "\n"),
			expectedTreeEntries: []TreeEntry{
				{
					Content: "foobar",
					Mode:    "100644",
					Path:    "file",
				},
			},
		},
		{
			desc: "with tree",
			opts: []WriteCommitOption{
				WithTree(WriteTree(t, cfg, repoPath, []TreeEntry{
					{
						Content: "something",
						Mode:    "100644",
						Path:    "file",
					},
				})),
			},
			expectedCommit: strings.Join([]string{
				"tree 52193934b12dbe23bf1d663802d77a04792a79ac",
				"parent " + defaultParentID,
				"author " + defaultCommitter,
				"committer " + defaultCommitter,
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
