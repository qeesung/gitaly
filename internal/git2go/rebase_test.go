//go:build static && system_libgit2 && !gitaly_test_sha256

package git2go

import (
	"fmt"
	"testing"
	"time"

	git "github.com/libgit2/git2go/v33"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/cmd/gitaly-git2go/git2goutil"
	gitalygit "gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
)

var masterRevision = "1e292f8fedd741b75372e19097c76d327140c312"

func TestRebase_validation(t *testing.T) {
	cfg, repo, repoPath := testcfg.BuildWithRepo(t)
	testcfg.BuildGitalyGit2Go(t, cfg)
	committer := NewSignature("Foo", "foo@example.com", time.Now())
	executor := NewExecutor(cfg, gittest.NewCommandFactory(t, cfg), config.NewLocator(cfg))

	testcases := []struct {
		desc        string
		request     RebaseCommand
		expectedErr string
	}{
		{
			desc:        "no arguments",
			expectedErr: "rebase: missing repository",
		},
		{
			desc:        "missing repository",
			request:     RebaseCommand{Committer: committer, BranchName: "feature", UpstreamRevision: masterRevision},
			expectedErr: "rebase: missing repository",
		},
		{
			desc:        "missing committer name",
			request:     RebaseCommand{Repository: repoPath, Committer: Signature{Email: "foo@example.com"}, BranchName: "feature", UpstreamRevision: masterRevision},
			expectedErr: "rebase: missing committer name",
		},
		{
			desc:        "missing committer email",
			request:     RebaseCommand{Repository: repoPath, Committer: Signature{Name: "Foo"}, BranchName: "feature", UpstreamRevision: masterRevision},
			expectedErr: "rebase: missing committer email",
		},
		{
			desc:        "missing branch name",
			request:     RebaseCommand{Repository: repoPath, Committer: committer, UpstreamRevision: masterRevision},
			expectedErr: "rebase: missing branch name",
		},
		{
			desc:        "missing upstream branch",
			request:     RebaseCommand{Repository: repoPath, Committer: committer, BranchName: "feature"},
			expectedErr: "rebase: missing upstream revision",
		},
		{
			desc:        "both branch name and commit ID",
			request:     RebaseCommand{Repository: repoPath, Committer: committer, BranchName: "feature", CommitID: "a"},
			expectedErr: "rebase: both branch name and commit ID",
		},
		{
			desc:        "both upstream revision and upstream commit ID",
			request:     RebaseCommand{Repository: repoPath, Committer: committer, BranchName: "feature", UpstreamRevision: "a", UpstreamCommitID: "a"},
			expectedErr: "rebase: both upstream revision and upstream commit ID",
		},
	}
	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			_, err := executor.Rebase(ctx, repo, tc.request)
			require.EqualError(t, err, tc.expectedErr)
		})
	}
}

func TestRebase_rebase(t *testing.T) {
	testcases := []struct {
		desc         string
		branch       string
		commitsAhead int
		setupRepo    func(testing.TB, *git.Repository)
		expected     string
		expectedErr  string
	}{
		{
			desc:         "Single commit rebase",
			branch:       "gitaly-rename-test",
			commitsAhead: 1,
			expected:     "a08ed4bc45f9e686db93c5d0519f63d7b537270c",
		},
		{
			desc:         "Multiple commits",
			branch:       "csv",
			commitsAhead: 5,
			expected:     "2f8365edc69d3683e22c4209ae9641642d84dd4a",
		},
		{
			desc:         "Branch zero commits behind",
			branch:       "sha-starting-with-large-number",
			commitsAhead: 1,
			expected:     "842616594688d2351480dfebd67b3d8d15571e6d",
		},
		{
			desc:     "Merged branch",
			branch:   "branch-merged",
			expected: masterRevision,
		},
		{
			desc:   "Partially merged branch",
			branch: "branch-merged-plus-one",
			setupRepo: func(tb testing.TB, repo *git.Repository) {
				head, err := git2goutil.LookupCommit(repo, "branch-merged")
				require.NoError(tb, err)

				other, err := git2goutil.LookupCommit(repo, "gitaly-rename-test")
				require.NoError(tb, err)
				tree, err := other.Tree()
				require.NoError(tb, err)
				newOid, err := repo.CreateCommitFromIds("refs/heads/branch-merged-plus-one", &defaultAuthor, &defaultAuthor, "Message", tree.Object.Id(), head.Object.Id())
				require.NoError(tb, err)
				require.Equal(tb, "8665d9b4b56f6b8ab8c4128a5549d1820bf68bf5", newOid.String())
			},
			commitsAhead: 1,
			expected:     "56bafb70922008232d171b78930be6cdb722bb39",
		},
		{
			desc:   "With upstream merged into",
			branch: "csv-plus-merge",
			setupRepo: func(tb testing.TB, repo *git.Repository) {
				ours, err := git2goutil.LookupCommit(repo, "csv")
				require.NoError(tb, err)
				theirs, err := git2goutil.LookupCommit(repo, "b83d6e391c22777fca1ed3012fce84f633d7fed0")
				require.NoError(tb, err)

				index, err := repo.MergeCommits(ours, theirs, nil)
				require.NoError(tb, err)
				tree, err := index.WriteTreeTo(repo)
				require.NoError(tb, err)

				newOid, err := repo.CreateCommitFromIds("refs/heads/csv-plus-merge", &defaultAuthor, &defaultAuthor, "Message", tree, ours.Object.Id(), theirs.Object.Id())
				require.NoError(tb, err)
				require.Equal(tb, "5b2d6bd7be0b1b9f7e46b64d02fe9882c133a128", newOid.String())
			},
			commitsAhead: 5, // Same as "Multiple commits"
			expected:     "2f8365edc69d3683e22c4209ae9641642d84dd4a",
		},
		{
			desc:        "Rebase with conflict",
			branch:      "rebase-encoding-failure-trigger",
			expectedErr: "rebase: commit \"eb8f5fb9523b868cef583e09d4bf70b99d2dd404\": there are conflicting files",
		},
		{
			desc:        "Orphaned branch",
			branch:      "orphaned-branch",
			expectedErr: "rebase: find merge base: no merge base found",
		},
	}

	for _, tc := range testcases {
		t.Run(tc.desc, func(t *testing.T) {
			ctx := testhelper.Context(t)

			committer := NewSignature(string(gittest.TestUser.Name),
				string(gittest.TestUser.Email),
				time.Date(2021, 3, 1, 13, 45, 50, 0, time.FixedZone("", +2*60*60)))

			cfg, repoProto, repoPath := testcfg.BuildWithRepo(t)
			testcfg.BuildGitalyGit2Go(t, cfg)
			executor := NewExecutor(cfg, gittest.NewCommandFactory(t, cfg), config.NewLocator(cfg))

			repo, err := git2goutil.OpenRepository(repoPath)
			require.NoError(t, err)

			if tc.setupRepo != nil {
				tc.setupRepo(t, repo)
			}

			branchCommit, err := git2goutil.LookupCommit(repo, tc.branch)
			require.NoError(t, err)

			for desc, request := range map[string]RebaseCommand{
				"with branch and upstream": {
					Repository:       repoPath,
					Committer:        committer,
					BranchName:       tc.branch,
					UpstreamRevision: masterRevision,
				},
				"with branch and upstream commit ID": {
					Repository:       repoPath,
					Committer:        committer,
					BranchName:       tc.branch,
					UpstreamCommitID: gitalygit.ObjectID(masterRevision),
				},
				"with commit ID and upstream": {
					Repository:       repoPath,
					Committer:        committer,
					BranchName:       tc.branch,
					UpstreamRevision: masterRevision,
				},
				"with commit ID and upstream commit ID": {
					Repository:       repoPath,
					Committer:        committer,
					CommitID:         gitalygit.ObjectID(branchCommit.Id().String()),
					UpstreamCommitID: gitalygit.ObjectID(masterRevision),
				},
			} {
				t.Run(desc, func(t *testing.T) {
					response, err := executor.Rebase(ctx, repoProto, request)
					if tc.expectedErr != "" {
						require.EqualError(t, err, tc.expectedErr)
					} else {
						require.NoError(t, err)

						result := response.String()
						require.Equal(t, tc.expected, result)

						commit, err := git2goutil.LookupCommit(repo, result)
						require.NoError(t, err)

						for i := tc.commitsAhead; i > 0; i-- {
							commit = commit.Parent(0)
						}
						masterCommit, err := git2goutil.LookupCommit(repo, masterRevision)
						require.NoError(t, err)
						require.Equal(t, masterCommit, commit)
					}
				})
			}
		})
	}
}

func TestRebase_skipEmptyCommit(t *testing.T) {
	ctx := testhelper.Context(t)

	cfg, repoProto, repoPath := testcfg.BuildWithRepo(t)
	testcfg.BuildGitalyGit2Go(t, cfg)

	// Set up history with two diverging lines of branches, where both sides have implemented
	// the same changes. During rebase, the diff will thus become empty.
	base := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithTreeEntries(gittest.TreeEntry{
			Path: "a", Content: "base", Mode: "100644",
		}),
	)
	theirs := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("theirs"),
		gittest.WithParents(base), gittest.WithTreeEntries(gittest.TreeEntry{
			Path: "a", Content: "changed", Mode: "100644",
		}),
	)
	ours := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("ours"),
		gittest.WithParents(base), gittest.WithTreeEntries(gittest.TreeEntry{
			Path: "a", Content: "changed", Mode: "100644",
		}),
	)

	for _, tc := range []struct {
		desc             string
		skipEmptyCommits bool
		expectedErr      string
		expectedResponse gitalygit.ObjectID
	}{
		{
			desc:             "do not skip empty commit",
			skipEmptyCommits: false,
			expectedErr:      fmt.Sprintf("rebase: commit %q: this patch has already been applied", ours),
		},
		{
			desc:             "skip empty commit",
			skipEmptyCommits: true,
			expectedResponse: theirs,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			response, err := NewExecutor(cfg, gittest.NewCommandFactory(t, cfg), config.NewLocator(cfg)).Rebase(ctx, repoProto, RebaseCommand{
				Repository:       repoPath,
				Committer:        NewSignature("Foo", "foo@example.com", time.Now()),
				CommitID:         ours,
				UpstreamCommitID: theirs,
				SkipEmptyCommits: tc.skipEmptyCommits,
			})
			if tc.expectedErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tc.expectedErr)
			}
			require.Equal(t, tc.expectedResponse, response)
		})
	}
}
