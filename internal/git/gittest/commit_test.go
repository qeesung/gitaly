package gittest_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/catfile"
	"gitlab.com/gitlab-org/gitaly/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
)

func TestWriteCommit(t *testing.T) {
	cfg, repoProto, repoPath := testcfg.BuildWithRepo(t)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)
	ctx := testhelper.Context(t)

	catfileCache := catfile.NewCache(cfg)
	defer catfileCache.Stop()

	objectReader, cancel, err := catfileCache.ObjectReader(ctx, repo)
	require.NoError(t, err)
	defer cancel()

	defaultCommitter := &gitalypb.CommitAuthor{
		Name:  []byte("Scrooge McDuck"),
		Email: []byte("scrooge@mcduck.com"),
	}
	defaultParentID := "1a0b36b3cdad1d2ee32457c102a8c0b7056fa863"

	revisions := map[git.Revision]git.ObjectID{
		"refs/heads/master":  "",
		"refs/heads/master~": "",
	}
	for revision := range revisions {
		oid, err := repo.ResolveRevision(ctx, revision)
		require.NoError(t, err)
		revisions[revision] = oid
	}

	for _, tc := range []struct {
		desc                string
		opts                []gittest.WriteCommitOption
		expectedCommit      *gitalypb.GitCommit
		expectedTreeEntries []gittest.TreeEntry
		expectedRevUpdate   git.Revision
	}{
		{
			desc: "no options",
			expectedCommit: &gitalypb.GitCommit{
				Author:    defaultCommitter,
				Committer: defaultCommitter,
				Subject:   []byte("message"),
				Body:      []byte("message"),
				Id:        "cab056fb7bfc5a4d024c2c5b9b445b80f212fdcd",
				ParentIds: []string{
					defaultParentID,
				},
			},
		},
		{
			desc: "with commit message",
			opts: []gittest.WriteCommitOption{
				gittest.WithMessage("my custom message\n\nfoobar\n"),
			},
			expectedCommit: &gitalypb.GitCommit{
				Author:    defaultCommitter,
				Committer: defaultCommitter,
				Subject:   []byte("my custom message"),
				Body:      []byte("my custom message\n\nfoobar\n"),
				Id:        "7b7e8876f7df27ab99e46678acbf9ae3d29264ba",
				ParentIds: []string{
					defaultParentID,
				},
			},
		},
		{
			desc: "with no parents",
			opts: []gittest.WriteCommitOption{
				gittest.WithParents(),
			},
			expectedCommit: &gitalypb.GitCommit{
				Author:    defaultCommitter,
				Committer: defaultCommitter,
				Subject:   []byte("message"),
				Body:      []byte("message"),
				Id:        "549090fbeacc6607bc70648d3ba554c355e670c5",
				ParentIds: nil,
			},
		},
		{
			desc: "with multiple parents",
			opts: []gittest.WriteCommitOption{
				gittest.WithParents(revisions["refs/heads/master"], revisions["refs/heads/master~"]),
			},
			expectedCommit: &gitalypb.GitCommit{
				Author:    defaultCommitter,
				Committer: defaultCommitter,
				Subject:   []byte("message"),
				Body:      []byte("message"),
				Id:        "650084693e5ca9c0b05a21fc5ac21ad1805c758b",
				ParentIds: []string{
					revisions["refs/heads/master"].String(),
					revisions["refs/heads/master~"].String(),
				},
			},
		},
		{
			desc: "with branch",
			opts: []gittest.WriteCommitOption{
				gittest.WithBranch("foo"),
			},
			expectedCommit: &gitalypb.GitCommit{
				Author:    defaultCommitter,
				Committer: defaultCommitter,
				Subject:   []byte("message"),
				Body:      []byte("message"),
				Id:        "cab056fb7bfc5a4d024c2c5b9b445b80f212fdcd",
				ParentIds: []string{
					defaultParentID,
				},
			},
			expectedRevUpdate: "refs/heads/foo",
		},
		{
			desc: "with tree entry",
			opts: []gittest.WriteCommitOption{
				gittest.WithTreeEntries(gittest.TreeEntry{
					Content: "foobar",
					Mode:    "100644",
					Path:    "file",
				}),
			},
			expectedCommit: &gitalypb.GitCommit{
				Author:    defaultCommitter,
				Committer: defaultCommitter,
				Subject:   []byte("message"),
				Body:      []byte("message"),
				Id:        "12da4907ed3331f4991ba6817317a3a90801288e",
				ParentIds: []string{
					defaultParentID,
				},
			},
			expectedTreeEntries: []gittest.TreeEntry{
				{
					Content: "foobar",
					Mode:    "100644",
					Path:    "file",
				},
			},
		},
		{
			desc: "with tree",
			opts: []gittest.WriteCommitOption{
				gittest.WithTree(gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{
						Content: "something",
						Mode:    "100644",
						Path:    "file",
					},
				})),
			},
			expectedCommit: &gitalypb.GitCommit{
				Author:    defaultCommitter,
				Committer: defaultCommitter,
				Subject:   []byte("message"),
				Body:      []byte("message"),
				Id:        "fc157fcabd57d95752ade820a791899f9891b984",
				ParentIds: []string{
					defaultParentID,
				},
			},
			expectedTreeEntries: []gittest.TreeEntry{
				{
					Content: "something",
					Mode:    "100644",
					Path:    "file",
				},
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			oid := gittest.WriteCommit(t, cfg, repoPath, tc.opts...)

			commit, err := catfile.GetCommit(ctx, objectReader, oid.Revision())
			require.NoError(t, err)

			gittest.CommitEqual(t, tc.expectedCommit, commit)

			if tc.expectedTreeEntries != nil {
				gittest.RequireTree(t, cfg, repoPath, oid.String(), tc.expectedTreeEntries)
			}

			if tc.expectedRevUpdate != "" {
				updatedOID, err := repo.ResolveRevision(ctx, tc.expectedRevUpdate)
				require.NoError(t, err)
				require.Equal(t, oid, updatedOID)
			}
		})
	}
}
