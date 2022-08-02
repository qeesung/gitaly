//go:build static && system_libgit2 && !gitaly_test_sha256

package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	glgit "gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestConflicts(t *testing.T) {
	testcases := []struct {
		desc      string
		base      []gittest.TreeEntry
		ours      []gittest.TreeEntry
		theirs    []gittest.TreeEntry
		conflicts []git2go.Conflict
	}{
		{
			desc: "no conflicts",
			base: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "file", Content: "b", Mode: "100644"},
			},
			conflicts: nil,
		},
		{
			desc: "single file",
			base: []gittest.TreeEntry{
				{Path: "file", Content: "a", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "file", Content: "b", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "file", Content: "c", Mode: "100644"},
			},
			conflicts: []git2go.Conflict{
				{
					Ancestor: git2go.ConflictEntry{Path: "file", Mode: 0o100644},
					Our:      git2go.ConflictEntry{Path: "file", Mode: 0o100644},
					Their:    git2go.ConflictEntry{Path: "file", Mode: 0o100644},
					Content:  []byte("<<<<<<< file\nb\n=======\nc\n>>>>>>> file\n"),
				},
			},
		},
		{
			desc: "multiple files with single conflict",
			base: []gittest.TreeEntry{
				{Path: "file-1", Content: "a", Mode: "100644"},
				{Path: "file-2", Content: "a", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "file-1", Content: "b", Mode: "100644"},
				{Path: "file-2", Content: "b", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "file-1", Content: "a", Mode: "100644"},
				{Path: "file-2", Content: "c", Mode: "100644"},
			},
			conflicts: []git2go.Conflict{
				{
					Ancestor: git2go.ConflictEntry{Path: "file-2", Mode: 0o100644},
					Our:      git2go.ConflictEntry{Path: "file-2", Mode: 0o100644},
					Their:    git2go.ConflictEntry{Path: "file-2", Mode: 0o100644},
					Content:  []byte("<<<<<<< file-2\nb\n=======\nc\n>>>>>>> file-2\n"),
				},
			},
		},
		{
			desc: "multiple conflicts",
			base: []gittest.TreeEntry{
				{Path: "file-1", Content: "a", Mode: "100644"},
				{Path: "file-2", Content: "a", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "file-1", Content: "b", Mode: "100644"},
				{Path: "file-2", Content: "b", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "file-1", Content: "c", Mode: "100644"},
				{Path: "file-2", Content: "c", Mode: "100644"},
			},
			conflicts: []git2go.Conflict{
				{
					Ancestor: git2go.ConflictEntry{Path: "file-1", Mode: 0o100644},
					Our:      git2go.ConflictEntry{Path: "file-1", Mode: 0o100644},
					Their:    git2go.ConflictEntry{Path: "file-1", Mode: 0o100644},
					Content:  []byte("<<<<<<< file-1\nb\n=======\nc\n>>>>>>> file-1\n"),
				},
				{
					Ancestor: git2go.ConflictEntry{Path: "file-2", Mode: 0o100644},
					Our:      git2go.ConflictEntry{Path: "file-2", Mode: 0o100644},
					Their:    git2go.ConflictEntry{Path: "file-2", Mode: 0o100644},
					Content:  []byte("<<<<<<< file-2\nb\n=======\nc\n>>>>>>> file-2\n"),
				},
			},
		},
		{
			desc: "modified-delete-conflict",
			base: []gittest.TreeEntry{
				{Path: "file", Content: "content", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "file", Content: "changed", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "different-file", Content: "unrelated", Mode: "100644"},
			},
			conflicts: []git2go.Conflict{
				{
					Ancestor: git2go.ConflictEntry{Path: "file", Mode: 0o100644},
					Our:      git2go.ConflictEntry{Path: "file", Mode: 0o100644},
					Their:    git2go.ConflictEntry{},
					Content:  []byte("<<<<<<< file\nchanged\n=======\n>>>>>>> \n"),
				},
			},
		},
		{
			// Ruby code doesn't call `merge_commits` with rename
			// detection and so don't we. The rename conflict is
			// thus split up into three conflicts.
			desc: "rename-rename-conflict",
			base: []gittest.TreeEntry{
				{Path: "file", Content: "a\nb\nc\nd\ne\nf\ng\n", Mode: "100644"},
			},
			ours: []gittest.TreeEntry{
				{Path: "renamed-1", Content: "a\nb\nc\nd\ne\nf\ng\n", Mode: "100644"},
			},
			theirs: []gittest.TreeEntry{
				{Path: "renamed-2", Content: "a\nb\nc\nd\ne\nf\ng\n", Mode: "100644"},
			},
			conflicts: []git2go.Conflict{
				{
					Ancestor: git2go.ConflictEntry{Path: "file", Mode: 0o100644},
					Our:      git2go.ConflictEntry{},
					Their:    git2go.ConflictEntry{},
					Content:  nil,
				},
				{
					Ancestor: git2go.ConflictEntry{},
					Our:      git2go.ConflictEntry{Path: "renamed-1", Mode: 0o100644},
					Their:    git2go.ConflictEntry{},
					Content:  []byte("a\nb\nc\nd\ne\nf\ng\n"),
				},
				{
					Ancestor: git2go.ConflictEntry{},
					Our:      git2go.ConflictEntry{},
					Their:    git2go.ConflictEntry{Path: "renamed-2", Mode: 0o100644},
					Content:  []byte("a\nb\nc\nd\ne\nf\ng\n"),
				},
			},
		},
	}

	for _, tc := range testcases {
		cfg, repo, repoPath := testcfg.BuildWithRepo(t)
		executor := buildExecutor(t, cfg)

		testcfg.BuildGitalyGit2Go(t, cfg)

		base := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(tc.base...))
		ours := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(base), gittest.WithTreeEntries(tc.ours...))
		theirs := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(base), gittest.WithTreeEntries(tc.theirs...))

		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			response, err := executor.Conflicts(ctx, repo, git2go.ConflictsCommand{
				Repository: repoPath,
				Ours:       ours.String(),
				Theirs:     theirs.String(),
			})

			require.NoError(t, err)
			require.Equal(t, tc.conflicts, response.Conflicts)
		})
	}
}

func TestConflicts_checkError(t *testing.T) {
	cfg, repo, repoPath := testcfg.BuildWithRepo(t)
	base := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries())
	validOID := glgit.ObjectID(base.String())
	executor := buildExecutor(t, cfg)

	testcfg.BuildGitalyGit2Go(t, cfg)

	testcases := []struct {
		desc             string
		overrideRepoPath string
		ours             glgit.ObjectID
		theirs           glgit.ObjectID
		expErr           error
	}{
		{
			desc:   "ours is not set",
			ours:   "",
			theirs: validOID,
			expErr: fmt.Errorf("conflicts: %w: missing ours", git2go.ErrInvalidArgument),
		},
		{
			desc:   "theirs is not set",
			ours:   validOID,
			theirs: "",
			expErr: fmt.Errorf("conflicts: %w: missing theirs", git2go.ErrInvalidArgument),
		},
		{
			desc:             "invalid repository",
			overrideRepoPath: "not/existing/path.git",
			ours:             validOID,
			theirs:           validOID,
			expErr:           status.Error(codes.Internal, "could not open repository: failed to resolve path 'not/existing/path.git': No such file or directory"),
		},
		{
			desc:   "ours is invalid",
			ours:   "1",
			theirs: validOID,
			expErr: status.Error(codes.InvalidArgument, "encoding/hex: odd length hex string"),
		},
		{
			desc:   "theirs is invalid",
			ours:   validOID,
			theirs: "1",
			expErr: status.Error(codes.InvalidArgument, "encoding/hex: odd length hex string"),
		},
		{
			desc:   "ours OID doesn't exist",
			ours:   glgit.ObjectHashSHA1.ZeroOID,
			theirs: validOID,
			expErr: status.Error(codes.InvalidArgument, "odb: cannot read object: null OID cannot exist"),
		},
		{
			desc:   "invalid object type",
			ours:   glgit.ObjectHashSHA1.EmptyTreeOID,
			theirs: validOID,
			expErr: status.Error(codes.InvalidArgument, "the requested type does not match the type in the ODB"),
		},
		{
			desc:   "theirs OID doesn't exist",
			ours:   validOID,
			theirs: glgit.ObjectHashSHA1.ZeroOID,
			expErr: status.Error(codes.InvalidArgument, "odb: cannot read object: null OID cannot exist"),
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			repoPath := repoPath
			if tc.overrideRepoPath != "" {
				repoPath = tc.overrideRepoPath
			}
			ctx := testhelper.Context(t)

			_, err := executor.Conflicts(ctx, repo, git2go.ConflictsCommand{
				Repository: repoPath,
				Ours:       tc.ours.String(),
				Theirs:     tc.theirs.String(),
			})

			require.Error(t, err)
			require.Equal(t, tc.expErr, err)
		})
	}
}
