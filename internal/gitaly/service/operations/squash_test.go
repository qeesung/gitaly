//go:build !gitaly_test_sha256

package operations

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v15/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	author = &gitalypb.User{
		Name:     []byte("John Doe"),
		Email:    []byte("johndoe@gitlab.com"),
		Timezone: gittest.Timezone,
	}
	branchName    = "not-merged-branch"
	startSha      = "b83d6e391c22777fca1ed3012fce84f633d7fed0"
	endSha        = "54cec5282aa9f21856362fe321c800c236a61615"
	commitMessage = []byte("Squash message")
)

func TestUserSquash_successful(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	for _, tc := range []struct {
		desc             string
		startOID, endOID string
	}{
		{
			desc:     "with sparse checkout",
			startOID: startSha,
			endOID:   endSha,
		},
		{
			desc:     "without sparse checkout",
			startOID: "60ecb67744cb56576c30214ff52294f8ce2def98",
			endOID:   "c84ff944ff4529a70788a5e9003c2b7feae29047",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)

			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			request := &gitalypb.UserSquashRequest{
				Repository:    repoProto,
				User:          gittest.TestUser,
				Author:        author,
				CommitMessage: commitMessage,
				StartSha:      tc.startOID,
				EndSha:        tc.endOID,
			}

			response, err := client.UserSquash(ctx, request)
			require.NoError(t, err)

			commit, err := repo.ReadCommit(ctx, git.Revision(response.SquashSha))
			require.NoError(t, err)
			require.Equal(t, []string{tc.startOID}, commit.ParentIds)
			require.Equal(t, author.Name, commit.Author.Name)
			require.Equal(t, author.Email, commit.Author.Email)
			require.Equal(t, gittest.TestUser.Name, commit.Committer.Name)
			require.Equal(t, gittest.TestUser.Email, commit.Committer.Email)
			require.Equal(t, gittest.TimezoneOffset, string(commit.Committer.Timezone))
			require.Equal(t, gittest.TimezoneOffset, string(commit.Author.Timezone))
			require.Equal(t, commitMessage, commit.Subject)

			treeData := gittest.Exec(t, cfg, "-C", repoPath, "ls-tree", "--name-only", response.SquashSha)
			files := strings.Fields(text.ChompBytes(treeData))
			require.Subset(t, files, []string{"VERSION", "README", "files", ".gitattributes"}, "ensure the files remain on their places")
		})
	}
}

func TestUserSquash_transactional(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	txManager := transaction.MockManager{}

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx,
		testserver.WithTransactionManager(&txManager),
	)

	squashedCommitID := "c653dc8f98dba7f7a42c2e3c4b8d850d195e60b6"
	squashedCommitVote := voting.VoteFromData([]byte(squashedCommitID))

	for _, tc := range []struct {
		desc           string
		voteFn         func(context.Context, txinfo.Transaction, voting.Vote, voting.Phase) error
		expectedErr    error
		expectedVotes  []voting.Vote
		expectedExists bool
	}{
		{
			desc: "successful",
			voteFn: func(context.Context, txinfo.Transaction, voting.Vote, voting.Phase) error {
				return nil
			},
			expectedVotes: []voting.Vote{
				// Only a single vote because we abort on the first one.
				squashedCommitVote,
				squashedCommitVote,
			},
			expectedExists: true,
		},
		{
			desc: "preparatory vote failure",
			voteFn: func(ctx context.Context, tx txinfo.Transaction, vote voting.Vote, phase voting.Phase) error {
				return fmt.Errorf("vote failed")
			},
			expectedErr: helper.ErrAbortedf("preparatory vote on squashed commit: vote failed"),
			expectedVotes: []voting.Vote{
				squashedCommitVote,
			},
			expectedExists: false,
		},
		{
			desc: "committing vote failure",
			voteFn: func(ctx context.Context, tx txinfo.Transaction, vote voting.Vote, phase voting.Phase) error {
				if phase == voting.Committed {
					return fmt.Errorf("vote failed")
				}
				return nil
			},
			expectedErr: helper.ErrAbortedf("committing vote on squashed commit: vote failed"),
			expectedVotes: []voting.Vote{
				squashedCommitVote,
				squashedCommitVote,
			},
			// Even though the committing vote has failed, we expect objects to have
			// been migrated after the preparatory vote. The commit should thus exist in
			// the repository.
			expectedExists: true,
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			ctx, err := txinfo.InjectTransaction(ctx, 1, "node", true)
			require.NoError(t, err)
			ctx = metadata.IncomingToOutgoing(ctx)

			// We need to use a voting function which simply does nothing at first so
			// that `CreateRepository()` isn't impacted.
			txManager.VoteFn = func(_ context.Context, _ txinfo.Transaction, _ voting.Vote, _ voting.Phase) error {
				return nil
			}

			repoProto, _ := gittest.CreateRepository(ctx, t, cfg, gittest.CreateRepositoryConfig{
				Seed: gittest.SeedGitLabTest,
			})
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			var votes []voting.Vote
			txManager.VoteFn = func(ctx context.Context, tx txinfo.Transaction, vote voting.Vote, phase voting.Phase) error {
				votes = append(votes, vote)
				return tc.voteFn(ctx, tx, vote, phase)
			}

			response, err := client.UserSquash(ctx, &gitalypb.UserSquashRequest{
				Repository:    repoProto,
				User:          gittest.TestUser,
				Author:        author,
				CommitMessage: []byte("Squashed commit"),
				StartSha:      startSha,
				EndSha:        endSha,
				Timestamp:     &timestamppb.Timestamp{Seconds: 1234512345},
			})

			if tc.expectedErr == nil {
				require.NoError(t, err)
				require.Equal(t, squashedCommitID, response.SquashSha)
			} else {
				testhelper.RequireGrpcError(t, tc.expectedErr, err)
			}
			require.Equal(t, tc.expectedVotes, votes)

			exists, err := repo.HasRevision(ctx, git.Revision(squashedCommitID+"^{commit}"))
			require.NoError(t, err)

			// We use a quarantine directory to stage the new objects. So if we fail to
			// reach quorum in the preparatory vote we expect the object to not have
			// been migrated to the repository.
			require.Equal(t, tc.expectedExists, exists)
		})
	}
}

func TestUserSquash_stableID(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, repoProto, _, client := setupOperationsService(t, ctx)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	response, err := client.UserSquash(ctx, &gitalypb.UserSquashRequest{
		Repository:    repoProto,
		User:          gittest.TestUser,
		Author:        author,
		CommitMessage: []byte("Squashed commit"),
		StartSha:      startSha,
		EndSha:        endSha,
		Timestamp:     &timestamppb.Timestamp{Seconds: 1234512345},
	})
	require.NoError(t, err)

	commit, err := repo.ReadCommit(ctx, git.Revision(response.SquashSha))
	require.NoError(t, err)
	require.Equal(t, &gitalypb.GitCommit{
		Id:     "c653dc8f98dba7f7a42c2e3c4b8d850d195e60b6",
		TreeId: "324242f415a3cdbfc088103b496379fd91965854",
		ParentIds: []string{
			"b83d6e391c22777fca1ed3012fce84f633d7fed0",
		},
		Subject:   []byte("Squashed commit"),
		Body:      []byte("Squashed commit\n"),
		BodySize:  16,
		Author:    authorFromUser(author, 1234512345),
		Committer: authorFromUser(gittest.TestUser, 1234512345),
	}, commit)
}

func authorFromUser(user *gitalypb.User, seconds int64) *gitalypb.CommitAuthor {
	return &gitalypb.CommitAuthor{
		Name:     user.Name,
		Email:    user.Email,
		Date:     &timestamppb.Timestamp{Seconds: seconds},
		Timezone: []byte(gittest.TimezoneOffset),
	}
}

func ensureSplitIndexExists(t *testing.T, cfg config.Cfg, repoDir string) bool {
	gittest.Exec(t, cfg, "-C", repoDir, "update-index", "--add")

	fis, err := os.ReadDir(repoDir)
	require.NoError(t, err)
	for _, fi := range fis {
		if strings.HasPrefix(fi.Name(), "sharedindex") {
			return true
		}
	}
	return false
}

func TestUserSquash_threeWayMerge(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, repoProto, _, client := setupOperationsService(t, ctx)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	request := &gitalypb.UserSquashRequest{
		Repository:    repoProto,
		User:          gittest.TestUser,
		Author:        author,
		CommitMessage: commitMessage,
		// The diff between two of these commits results in some changes to files/ruby/popen.rb
		StartSha: "6f6d7e7ed97bb5f0054f2b1df789b39ca89b6ff9",
		EndSha:   "570e7b2abdd848b95f2f578043fc23bd6f6fd24d",
	}

	response, err := client.UserSquash(ctx, request)
	require.NoError(t, err)

	commit, err := repo.ReadCommit(ctx, git.Revision(response.SquashSha))
	require.NoError(t, err)
	require.Equal(t, []string{"6f6d7e7ed97bb5f0054f2b1df789b39ca89b6ff9"}, commit.ParentIds)
	require.Equal(t, author.Name, commit.Author.Name)
	require.Equal(t, author.Email, commit.Author.Email)
	require.Equal(t, gittest.TestUser.Name, commit.Committer.Name)
	require.Equal(t, gittest.TimezoneOffset, string(commit.Committer.Timezone))
	require.Equal(t, gittest.TimezoneOffset, string(commit.Author.Timezone))
	require.Equal(t, gittest.TestUser.Email, commit.Committer.Email)
	require.Equal(t, commitMessage, commit.Subject)
}

func TestUserSquash_splitIndex(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, repo, repoPath, client := setupOperationsService(t, ctx)

	require.False(t, ensureSplitIndexExists(t, cfg, repoPath))

	request := &gitalypb.UserSquashRequest{
		Repository:    repo,
		User:          gittest.TestUser,
		Author:        author,
		CommitMessage: commitMessage,
		StartSha:      startSha,
		EndSha:        endSha,
	}

	_, err := client.UserSquash(ctx, request)
	require.NoError(t, err)
	require.False(t, ensureSplitIndexExists(t, cfg, repoPath))
}

func TestUserSquash_renames(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg)

	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"))

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	originalFilename := "original-file.txt"
	renamedFilename := "renamed-file.txt"

	rootCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: originalFilename, Mode: "100644", Content: "This is a test"},
		),
	)

	startCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(rootCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: renamedFilename, Mode: "100644", Content: "This is a test"},
		),
	)

	changedCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(rootCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: originalFilename, Mode: "100644", Content: "This is a change"},
		),
	)

	endCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(changedCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: originalFilename, Mode: "100644", Content: "This is another change"},
		),
	)

	request := &gitalypb.UserSquashRequest{
		Repository:    repoProto,
		User:          gittest.TestUser,
		Author:        author,
		CommitMessage: commitMessage,
		StartSha:      startCommitID.String(),
		EndSha:        endCommitID.String(),
	}

	response, err := client.UserSquash(ctx, request)
	require.NoError(t, err)

	commit, err := repo.ReadCommit(ctx, git.Revision(response.SquashSha))
	require.NoError(t, err)
	require.Equal(t, []string{startCommitID.String()}, commit.ParentIds)
	require.Equal(t, author.Name, commit.Author.Name)
	require.Equal(t, author.Email, commit.Author.Email)
	require.Equal(t, gittest.TestUser.Name, commit.Committer.Name)
	require.Equal(t, gittest.TestUser.Email, commit.Committer.Email)
	require.Equal(t, gittest.TimezoneOffset, string(commit.Committer.Timezone))
	require.Equal(t, gittest.TimezoneOffset, string(commit.Author.Timezone))
	require.Equal(t, commitMessage, commit.Subject)

	gittest.RequireTree(t, cfg, repoPath, response.SquashSha, []gittest.TreeEntry{
		{Path: renamedFilename, Mode: "100644", Content: "This is another change", OID: "1b2ae89cca65f0d514f677981f012d708df651fc"},
	})
}

func TestUserSquash_missingFileOnTargetBranch(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, _, repo, _, client := setupOperationsService(t, ctx)

	conflictingStartSha := "bbd36ad238d14e1c03ece0f3358f545092dc9ca3"

	request := &gitalypb.UserSquashRequest{
		Repository:    repo,
		User:          gittest.TestUser,
		Author:        author,
		CommitMessage: commitMessage,
		StartSha:      conflictingStartSha,
		EndSha:        endSha,
	}

	_, err := client.UserSquash(ctx, request)
	require.NoError(t, err)
}

func TestUserSquash_emptyCommit(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	// Set up history with two diverging lines of branches, where both sides have implemented
	// the same changes. During merge, the diff will thus become empty.
	base := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "a", Content: "base", Mode: "100644"},
		),
	)
	theirs := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("theirs"),
		gittest.WithParents(base), gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "a", Content: "changed", Mode: "100644"},
		),
	)
	ours := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("ours"),
		gittest.WithParents(base), gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "a", Content: "changed", Mode: "100644"},
		),
	)
	oursWithAdditionalChanges := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("ours"),
		gittest.WithParents(ours), gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "a", Content: "changed", Mode: "100644"},
			gittest.TreeEntry{Path: "ours", Content: "ours", Mode: "100644"},
		),
	)

	for _, tc := range []struct {
		desc                      string
		ours, theirs, expectedOID git.ObjectID
		expectedTreeEntries       []gittest.TreeEntry
		expectedCommit            *gitalypb.GitCommit
	}{
		{
			desc:        "ours becomes completely empty",
			ours:        ours,
			theirs:      theirs,
			expectedOID: "0c097018ea50a9c036ba7e98db2b12495e912884",
			expectedTreeEntries: []gittest.TreeEntry{
				{Path: "a", Content: "changed", Mode: "100644"},
			},
			expectedCommit: &gitalypb.GitCommit{
				Id:     "0c097018ea50a9c036ba7e98db2b12495e912884",
				TreeId: "dcec1f671540174251d228f3b1292cc4f84cd964",
				ParentIds: []string{
					theirs.String(),
				},
				Subject:   []byte("squashed"),
				Body:      []byte("squashed\n"),
				BodySize:  9,
				Author:    authorFromUser(author, 1234512345),
				Committer: authorFromUser(gittest.TestUser, 1234512345),
			},
		},
		{
			desc:        "parts of ours become empty",
			ours:        oursWithAdditionalChanges,
			theirs:      theirs,
			expectedOID: "1589b6ee8b29e193b6648f75b7289d95e90dbce1",
			expectedTreeEntries: []gittest.TreeEntry{
				{Path: "a", Content: "changed", Mode: "100644"},
				{Path: "ours", Content: "ours", Mode: "100644"},
			},
			expectedCommit: &gitalypb.GitCommit{
				Id:     "1589b6ee8b29e193b6648f75b7289d95e90dbce1",
				TreeId: "b39ebc91ea635e7469c406329bcf00be4ebe0e50",
				ParentIds: []string{
					theirs.String(),
				},
				Subject:   []byte("squashed"),
				Body:      []byte("squashed\n"),
				BodySize:  9,
				Author:    authorFromUser(author, 1234512345),
				Committer: authorFromUser(gittest.TestUser, 1234512345),
			},
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			response, err := client.UserSquash(ctx, &gitalypb.UserSquashRequest{
				Repository:    repoProto,
				User:          gittest.TestUser,
				Author:        author,
				CommitMessage: []byte("squashed"),
				StartSha:      tc.theirs.String(),
				EndSha:        tc.ours.String(),
				Timestamp:     &timestamppb.Timestamp{Seconds: 1234512345},
			})
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.UserSquashResponse{
				SquashSha: tc.expectedOID.String(),
			}, response)

			gittest.RequireTree(t, cfg, repoPath, tc.expectedOID.String(), tc.expectedTreeEntries)

			commit, err := repo.ReadCommit(ctx, tc.expectedOID.Revision())
			require.NoError(t, err)
			require.Equal(t, tc.expectedCommit, commit)
		})
	}
}

func TestUserSquash_validation(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, _, repo, _, client := setupOperationsService(t, ctx)

	testCases := []struct {
		desc    string
		request *gitalypb.UserSquashRequest
		code    codes.Code
	}{
		{
			desc: "empty Repository",
			request: &gitalypb.UserSquashRequest{
				Repository:    nil,
				User:          gittest.TestUser,
				Author:        gittest.TestUser,
				CommitMessage: commitMessage,
				StartSha:      startSha,
				EndSha:        endSha,
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty User",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          nil,
				Author:        gittest.TestUser,
				CommitMessage: commitMessage,
				StartSha:      startSha,
				EndSha:        endSha,
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty StartSha",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        gittest.TestUser,
				CommitMessage: commitMessage,
				StartSha:      "",
				EndSha:        endSha,
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty EndSha",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        gittest.TestUser,
				CommitMessage: commitMessage,
				StartSha:      startSha,
				EndSha:        "",
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty Author",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        nil,
				CommitMessage: commitMessage,
				StartSha:      startSha,
				EndSha:        endSha,
			},
			code: codes.InvalidArgument,
		},
		{
			desc: "empty CommitMessage",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        gittest.TestUser,
				CommitMessage: nil,
				StartSha:      startSha,
				EndSha:        endSha,
			},
			code: codes.InvalidArgument,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			_, err := client.UserSquash(ctx, testCase.request)
			testhelper.RequireGrpcCode(t, err, testCase.code)
			require.Contains(t, err.Error(), testCase.desc)
		})
	}
}

func TestUserSquash_conflicts(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, repo, repoPath, client := setupOperationsService(t, ctx)

	base := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "a", Mode: "100644", Content: "unchanged"},
		gittest.TreeEntry{Path: "b", Mode: "100644", Content: "base"},
	))

	ours := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(base), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "a", Mode: "100644", Content: "unchanged"},
		gittest.TreeEntry{Path: "b", Mode: "100644", Content: "ours"},
	))

	theirs := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(base), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "a", Mode: "100644", Content: "unchanged"},
		gittest.TreeEntry{Path: "b", Mode: "100644", Content: "theirs"},
	))

	response, err := client.UserSquash(ctx, &gitalypb.UserSquashRequest{
		Repository:    repo,
		User:          gittest.TestUser,
		Author:        gittest.TestUser,
		CommitMessage: commitMessage,
		StartSha:      theirs.String(),
		EndSha:        ours.String(),
	})

	testhelper.RequireGrpcError(t, errWithDetails(t,
		helper.ErrFailedPreconditionf(
			"squashing commits: merge: there are conflicting files",
		),
		&gitalypb.UserSquashError{
			Error: &gitalypb.UserSquashError_RebaseConflict{
				RebaseConflict: &gitalypb.MergeConflictError{
					ConflictingFiles: [][]byte{
						[]byte("b"),
					},
					ConflictingCommitIds: []string{
						theirs.String(),
						ours.String(),
					},
				},
			},
		},
	), err)
	require.Nil(t, response)
}

func TestUserSquash_ancestry(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, repo, repoPath, client := setupOperationsService(t, ctx)

	// We create an empty parent commit and two commits which both branch off from it. As a
	// result, they are not direct ancestors of each other.
	parent := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("p"), gittest.WithTreeEntries())
	commit1 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("1"),
		gittest.WithTreeEntries(gittest.TreeEntry{Path: "a", Mode: "100644", Content: "a-content"}),
		gittest.WithParents(parent),
	)
	commit2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("2"),
		gittest.WithTreeEntries(gittest.TreeEntry{Path: "b", Mode: "100644", Content: "b-content"}),
		gittest.WithParents(parent),
	)

	response, err := client.UserSquash(ctx, &gitalypb.UserSquashRequest{
		Repository:    repo,
		User:          gittest.TestUser,
		Author:        gittest.TestUser,
		CommitMessage: commitMessage,
		StartSha:      commit1.String(),
		EndSha:        commit2.String(),
		Timestamp:     &timestamppb.Timestamp{Seconds: 1234512345},
	})

	require.Nil(t, err)
	testhelper.ProtoEqual(t, &gitalypb.UserSquashResponse{
		SquashSha: "b277ddc0aafcba53f23f3d4d4a46dde42c9e7ad2",
	}, response)
}

func TestUserSquash_gitError(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, _, repo, _, client := setupOperationsService(t, ctx)

	testCases := []struct {
		desc             string
		request          *gitalypb.UserSquashRequest
		expectedErr      error
		expectedResponse *gitalypb.UserSquashResponse
	}{
		{
			desc: "not existing start SHA",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        gittest.TestUser,
				CommitMessage: commitMessage,
				StartSha:      "doesntexisting",
				EndSha:        endSha,
			},
			expectedErr: errWithDetails(t,
				helper.ErrInvalidArgumentf("resolving start revision: reference not found"),
				&gitalypb.UserSquashError{
					Error: &gitalypb.UserSquashError_ResolveRevision{
						ResolveRevision: &gitalypb.ResolveRevisionError{
							Revision: []byte("doesntexisting"),
						},
					},
				},
			),
		},
		{
			desc: "not existing end SHA",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        gittest.TestUser,
				CommitMessage: commitMessage,
				StartSha:      startSha,
				EndSha:        "doesntexisting",
			},
			expectedErr: errWithDetails(t,
				helper.ErrInvalidArgumentf("resolving end revision: reference not found"),
				&gitalypb.UserSquashError{
					Error: &gitalypb.UserSquashError_ResolveRevision{
						ResolveRevision: &gitalypb.ResolveRevisionError{
							Revision: []byte("doesntexisting"),
						},
					},
				},
			),
		},
		{
			desc: "user has no name set",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          &gitalypb.User{Email: gittest.TestUser.Email},
				Author:        gittest.TestUser,
				CommitMessage: commitMessage,
				StartSha:      startSha,
				EndSha:        endSha,
			},
			expectedErr: helper.ErrInvalidArgumentf("UserSquash: empty user name"),
		},
		{
			desc: "author has no name set",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        &gitalypb.User{Email: gittest.TestUser.Email},
				CommitMessage: commitMessage,
				StartSha:      startSha,
				EndSha:        endSha,
			},
			expectedErr: helper.ErrInvalidArgumentf("UserSquash: empty author name"),
		},
		{
			desc: "author has no email set",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        &gitalypb.User{Name: gittest.TestUser.Name},
				CommitMessage: commitMessage,
				StartSha:      startSha,
				EndSha:        endSha,
			},
			expectedErr: helper.ErrInvalidArgumentf("UserSquash: empty author email"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			response, err := client.UserSquash(ctx, tc.request)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
			testhelper.ProtoEqual(t, tc.expectedResponse, response)
		})
	}
}

func TestUserSquash_squashingMerge(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, repo, repoPath, client := setupOperationsService(t, ctx)

	base := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("base"),
		gittest.WithTreeEntries(gittest.TreeEntry{Path: "a", Mode: "100644", Content: "base-content"}),
	)
	ours := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("ours"),
		gittest.WithTreeEntries(gittest.TreeEntry{Path: "a", Mode: "100644", Content: "ours-content"}),
		gittest.WithParents(base),
	)
	theirs := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("theirs"),
		gittest.WithTreeEntries(gittest.TreeEntry{Path: "a", Mode: "100644", Content: "theirs-content"}),
		gittest.WithParents(base),
	)
	oursMergedIntoTheirs := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("merge ours into theirs"),
		gittest.WithTreeEntries(gittest.TreeEntry{Path: "a", Mode: "100644", Content: "ours-content\ntheirs-content"}),
		gittest.WithParents(theirs, ours),
	)
	ours2 := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("ours 2"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "a", Mode: "100644", Content: "ours-content"},
			gittest.TreeEntry{Path: "ours-file", Mode: "100644", Content: "new-content"},
		),
		gittest.WithParents(ours),
	)

	// We had conflicting commit on "ours" and on "theirs",
	// then we have manually merged "ours into "theirs" resolving the conflict,
	// and then we created one non-conflicting commit on branch "ours".
	//
	//        o-------o ours
	//       / \
	// base o   X
	//       \   \
	//        o---o     theirs
	//
	// We're now squashing both commits from "theirs" onto "ours".
	response, err := client.UserSquash(ctx, &gitalypb.UserSquashRequest{
		Repository:    repo,
		User:          gittest.TestUser,
		Author:        gittest.TestUser,
		CommitMessage: commitMessage,
		StartSha:      ours2.String(),
		EndSha:        oursMergedIntoTheirs.String(),
		Timestamp:     &timestamppb.Timestamp{Seconds: 1234512345},
	})

	// With squashing using merge, we should successfully merge without any issues.
	// The new detached commit history will look like this:
	//
	// HEAD o---o---o---o
	//
	// We have one commit from "base", two from "ours"
	// and one squash commit that contains squashed changes from branch "theirs".
	require.Nil(t, err)
	testhelper.ProtoEqual(t, &gitalypb.UserSquashResponse{
		SquashSha: "69d8db2439502c18b9c17c2d1bddb122a82bd448",
	}, response)
	gittest.RequireTree(t, cfg, repoPath, "69d8db2439502c18b9c17c2d1bddb122a82bd448", []gittest.TreeEntry{
		{
			// It should use the version from commit "oursMergedIntoTheirs",
			// as it resolves the pre-existing conflict.
			Content: "ours-content\ntheirs-content",
			Mode:    "100644",
			Path:    "a",
		},
		{
			// This is the file that only existed on branch "ours".
			Content: "new-content",
			Mode:    "100644",
			Path:    "ours-file",
		},
	})
}
