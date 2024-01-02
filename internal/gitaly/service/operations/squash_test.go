package operations

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/signature"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/voting"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	author = &gitalypb.User{
		Name:     []byte("John Doe"),
		Email:    []byte("johndoe@gitlab.com"),
		Timezone: gittest.Timezone,
	}
	commitMessage = []byte("Squash message")
)

func TestUserSquash_successful(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserSquashSuccessful)
}

func testUserSquashSuccessful(t *testing.T, ctx context.Context) {
	t.Parallel()

	var opts []testserver.GitalyServerOpt
	if featureflag.GPGSigning.IsEnabled(ctx) {
		opts = append(opts, testserver.WithSigningKey("testdata/signing_ssh_key_ed25519"))
	}

	ctx, cfg, client := setupOperationsService(t, ctx, opts...)
	testcfg.BuildGitalyGPG(t, cfg)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	baseID := gittest.WriteCommit(t, cfg, repoPath)
	theirsID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(baseID), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "theirs", Mode: "100644", Content: "theirs"},
	))
	intermediateID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(baseID), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "ours", Mode: "100644", Content: "intermediate"},
	))
	oursID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(intermediateID), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "ours", Mode: "100644", Content: "ours"},
	))

	response, err := client.UserSquash(ctx, &gitalypb.UserSquashRequest{
		Repository:    repoProto,
		User:          gittest.TestUser,
		Author:        author,
		CommitMessage: commitMessage,
		StartSha:      theirsID.String(),
		EndSha:        oursID.String(),
	})
	require.NoError(t, err)

	commit, err := repo.ReadCommit(ctx, git.Revision(response.SquashSha))
	require.NoError(t, err)
	require.Equal(t, []string{theirsID.String()}, commit.ParentIds)
	require.Equal(t, author.Name, commit.Author.Name)
	require.Equal(t, author.Email, commit.Author.Email)
	require.Equal(t, gittest.TestUser.Name, commit.Committer.Name)
	require.Equal(t, gittest.TestUser.Email, commit.Committer.Email)
	require.Equal(t, gittest.TimezoneOffset, string(commit.Committer.Timezone))
	require.Equal(t, gittest.TimezoneOffset, string(commit.Author.Timezone))
	require.Equal(t, commitMessage, commit.Subject)

	treeData := gittest.Exec(t, cfg, "-C", repoPath, "ls-tree", "--name-only", response.SquashSha)
	files := strings.Fields(text.ChompBytes(treeData))
	require.Equal(t, []string{"ours", "theirs"}, files)

	if featureflag.GPGSigning.IsEnabled(ctx) {
		data, err := repo.ReadObject(ctx, git.ObjectID(response.SquashSha))
		require.NoError(t, err)

		gpgsig, dataWithoutGpgSig := signature.ExtractSignature(t, ctx, data)

		signingKey, err := signature.ParseSigningKeys("testdata/signing_ssh_key_ed25519")
		require.NoError(t, err)

		require.NoError(t, signingKey.Verify([]byte(gpgsig), []byte(dataWithoutGpgSig)))
	}
}

func TestUserSquash_transaction(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserSquashTransactional)
}

func testUserSquashTransactional(t *testing.T, ctx context.Context) {
	t.Parallel()

	txManager := transaction.MockManager{}

	ctx, cfg, client := setupOperationsService(t, ctx,
		testserver.WithTransactionManager(&txManager),
	)

	squashedCommitID := gittest.ObjectHashDependent(t, map[string]string{
		"sha1":   "bd94bb881a5c5466da78a4e911017abfe8998e0c",
		"sha256": "6fdd7d1bd86b796b35f2d3c8b7a06a1adf5897583bea593abfb446cd015c9b3a",
	})
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
				return errors.New("vote failed")
			},
			expectedErr: structerr.NewAborted("preparatory vote on squashed commit: vote failed"),
			expectedVotes: []voting.Vote{
				squashedCommitVote,
			},
			expectedExists: false,
		},
		{
			desc: "committing vote failure",
			voteFn: func(ctx context.Context, tx txinfo.Transaction, vote voting.Vote, phase voting.Phase) error {
				if phase == voting.Committed {
					return errors.New("vote failed")
				}
				return nil
			},
			expectedErr: structerr.NewAborted("committing vote on squashed commit: vote failed"),
			expectedVotes: []voting.Vote{
				squashedCommitVote,
				squashedCommitVote,
			},
			// Even though the committing vote has failed, we expect objects to have
			// been migrated after the preparatory vote. The commit should thus exist in
			// the repository.
			//
			// With transactions, the object is written to the repository if the transaction successfully
			// commits. Vote failure raises an error which causes the transaction to be rolled back. We don't
			// thus expect to have the object in the repository.
			expectedExists: !testhelper.IsWALEnabled(),
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

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			baseID := gittest.WriteCommit(t, cfg, repoPath)
			startID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(baseID), gittest.WithTreeEntries(
				gittest.TreeEntry{Path: "start", Mode: "100644", Content: "start"},
			))
			endID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(baseID), gittest.WithTreeEntries(
				gittest.TreeEntry{Path: "end", Mode: "100644", Content: "end"},
			))

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
				StartSha:      startID.String(),
				EndSha:        endID.String(),
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

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserSquashStableID)
}

func testUserSquashStableID(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

	baseCommitID := gittest.WriteCommit(t, cfg, repoPath)
	startCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(baseCommitID))
	midCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(startCommitID))
	endTreeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "path", Mode: "100644", Content: "content"},
	})
	endCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(midCommitID), gittest.WithTree(endTreeID))

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	response, err := client.UserSquash(ctx, &gitalypb.UserSquashRequest{
		Repository:    repoProto,
		User:          gittest.TestUser,
		Author:        author,
		CommitMessage: []byte("Squashed commit"),
		StartSha:      startCommitID.String(),
		EndSha:        endCommitID.String(),
		Timestamp:     &timestamppb.Timestamp{Seconds: 1234512345},
	})
	require.NoError(t, err)

	commit, err := repo.ReadCommit(ctx, git.Revision(response.SquashSha))
	require.NoError(t, err)
	require.Equal(t, &gitalypb.GitCommit{
		Id: gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "81a602b7dbbc79e6aae871477d62902c61373741",
			"sha256": "fe8de63f9481d39a3db08ebf1ce692844eb35fef6d5baf9ccc748f97d079ff54",
		}),
		TreeId: endTreeID.String(),
		ParentIds: []string{
			startCommitID.String(),
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

func TestUserSquash_threeWayMerge(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserSquashThreeWayMerge)
}

func testUserSquashThreeWayMerge(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	// We create two diverging histories. The left-hand side does a change at the beginning of the file, while the
	// two commits on the right-hand side append two changes to the end of the file. Squash-rebasing the right side
	// onto the left side should thus result in a three-way merge.
	baseID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "path", Mode: "100644", Content: "a\nb\nc\nd\ne\nf\ng\nh\n"},
	))
	leftID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "path", Mode: "100644", Content: "left\nb\nc\nd\ne\nf\ng\nh\n"},
	), gittest.WithParents(baseID))

	rightAID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "path", Mode: "100644", Content: "a\nb\nc\nd\ne\nf\ng\nright-a\n"},
	), gittest.WithParents(baseID))
	rightBID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "path", Mode: "100644", Content: "a\nb\nc\nd\ne\nf\ng\nright-a\nright-b\n"},
	), gittest.WithParents(rightAID))

	response, err := client.UserSquash(ctx, &gitalypb.UserSquashRequest{
		Repository:    repoProto,
		User:          gittest.TestUser,
		Author:        author,
		CommitMessage: commitMessage,
		StartSha:      leftID.String(),
		EndSha:        rightBID.String(),
	})
	require.NoError(t, err)

	commit, err := repo.ReadCommit(ctx, git.Revision(response.SquashSha))
	require.NoError(t, err)
	require.Equal(t, []string{leftID.String()}, commit.ParentIds)
	require.Equal(t, author.Name, commit.Author.Name)
	require.Equal(t, author.Email, commit.Author.Email)
	require.Equal(t, gittest.TestUser.Name, commit.Committer.Name)
	require.Equal(t, gittest.TimezoneOffset, string(commit.Committer.Timezone))
	require.Equal(t, gittest.TimezoneOffset, string(commit.Author.Timezone))
	require.Equal(t, gittest.TestUser.Email, commit.Committer.Email)
	require.Equal(t, commitMessage, commit.Subject)
}

func TestUserSquash_renames(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserSquashRenames)
}

func testUserSquashRenames(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

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

	endBlobID := gittest.WriteBlob(t, cfg, repoPath, []byte("This is another change"))
	endCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(changedCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: originalFilename, Mode: "100644", OID: endBlobID},
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
		{Path: renamedFilename, Mode: "100644", Content: "This is another change", OID: endBlobID},
	})
}

func TestUserSquash_missingFileOnTargetBranch(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserSquashMissingFileOnTargetBranch)
}

func testUserSquashMissingFileOnTargetBranch(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	// We create a new file in our branch that does not exist on the target branch. With the old Ruby-based
	// implementation, this led to an error.
	baseID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "common", Mode: "100644", Content: "common"},
	))
	targetID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "common", Mode: "100644", Content: "changed"},
	), gittest.WithParents(baseID))
	ourID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "common", Mode: "100644", Content: "common"},
		gittest.TreeEntry{Path: "new-file", Mode: "100644", Content: "new-file"},
	), gittest.WithParents(baseID))

	_, err := client.UserSquash(ctx, &gitalypb.UserSquashRequest{
		Repository:    repo,
		User:          gittest.TestUser,
		Author:        author,
		CommitMessage: commitMessage,
		StartSha:      targetID.String(),
		EndSha:        ourID.String(),
	})
	require.NoError(t, err)
}

func TestUserSquash_emptyCommit(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserSquashEmptyCommit)
}

func testUserSquashEmptyCommit(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
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
		desc                string
		ours, theirs        git.ObjectID
		expectedTreeEntries []gittest.TreeEntry
		expectedCommit      *gitalypb.GitCommit
	}{
		{
			desc:   "ours becomes completely empty",
			ours:   ours,
			theirs: theirs,
			expectedTreeEntries: []gittest.TreeEntry{
				{Path: "a", Content: "changed", Mode: "100644"},
			},
			expectedCommit: &gitalypb.GitCommit{
				Id: gittest.ObjectHashDependent(t, map[string]string{
					"sha1":   "0c097018ea50a9c036ba7e98db2b12495e912884",
					"sha256": "73e596f79a214b856f93227d3a899bb7283fb7a6e8dabd80c452a825906bb91a",
				}),
				TreeId: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "a", Content: "changed", Mode: "100644"},
				}).String(),
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
			desc:   "parts of ours become empty",
			ours:   oursWithAdditionalChanges,
			theirs: theirs,
			expectedTreeEntries: []gittest.TreeEntry{
				{Path: "a", Content: "changed", Mode: "100644"},
				{Path: "ours", Content: "ours", Mode: "100644"},
			},
			expectedCommit: &gitalypb.GitCommit{
				Id: gittest.ObjectHashDependent(t, map[string]string{
					"sha1":   "1589b6ee8b29e193b6648f75b7289d95e90dbce1",
					"sha256": "ecfb568a418ed6e4086ddad76fe7133880ffa636e3282716f29c943fd6b72bcd",
				}),
				TreeId: gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
					{Path: "a", Content: "changed", Mode: "100644"},
					{Path: "ours", Content: "ours", Mode: "100644"},
				}).String(),
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
				SquashSha: tc.expectedCommit.Id,
			}, response)

			gittest.RequireTree(t, cfg, repoPath, tc.expectedCommit.Id, tc.expectedTreeEntries)

			commit, err := repo.ReadCommit(ctx, git.Revision(tc.expectedCommit.Id))
			require.NoError(t, err)
			require.Equal(t, tc.expectedCommit, commit)
		})
	}
}

func TestUserSquash_validation(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	commitID := gittest.WriteCommit(t, cfg, repoPath)

	testCases := []struct {
		desc        string
		request     *gitalypb.UserSquashRequest
		expectedErr error
	}{
		{
			desc: "unset repository",
			request: &gitalypb.UserSquashRequest{
				Repository:    nil,
				User:          gittest.TestUser,
				Author:        gittest.TestUser,
				CommitMessage: commitMessage,
				StartSha:      commitID.String(),
				EndSha:        commitID.String(),
			},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "empty User",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          nil,
				Author:        gittest.TestUser,
				CommitMessage: commitMessage,
				StartSha:      commitID.String(),
				EndSha:        commitID.String(),
			},
			expectedErr: status.Error(codes.InvalidArgument, "empty User"),
		},
		{
			desc: "empty StartSha",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        gittest.TestUser,
				CommitMessage: commitMessage,
				StartSha:      "",
				EndSha:        commitID.String(),
			},
			expectedErr: status.Error(codes.InvalidArgument, "empty StartSha"),
		},
		{
			desc: "empty EndSha",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        gittest.TestUser,
				CommitMessage: commitMessage,
				StartSha:      commitID.String(),
				EndSha:        "",
			},
			expectedErr: status.Error(codes.InvalidArgument, "empty EndSha"),
		},
		{
			desc: "empty Author",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        nil,
				CommitMessage: commitMessage,
				StartSha:      commitID.String(),
				EndSha:        commitID.String(),
			},
			expectedErr: status.Error(codes.InvalidArgument, "empty Author"),
		},
		{
			desc: "empty CommitMessage",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        gittest.TestUser,
				CommitMessage: nil,
				StartSha:      commitID.String(),
				EndSha:        commitID.String(),
			},
			expectedErr: status.Error(codes.InvalidArgument, "empty CommitMessage"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			_, err := client.UserSquash(ctx, testCase.request)
			testhelper.RequireGrpcError(t, testCase.expectedErr, err)
		})
	}
}

func TestUserSquash_conflicts(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserSquashConflicts)
}

func testUserSquashConflicts(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

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

	testhelper.RequireGrpcError(t, structerr.NewFailedPrecondition("squashing commits: merge: there are conflicting files").WithDetail(
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

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserSquashAncestry)
}

func testUserSquashAncestry(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

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
		SquashSha: gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "b277ddc0aafcba53f23f3d4d4a46dde42c9e7ad2",
			"sha256": "d62efb044a263c87cf7eee19e1d85960618cad65f938d8eeaee69a6571d7bcb4",
		}),
	}, response)
}

func TestUserSquash_gitError(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserSquashGitError)
}

func testUserSquashGitError(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	commitID := gittest.WriteCommit(t, cfg, repoPath)

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
				EndSha:        commitID.String(),
			},
			expectedErr: errWithDetails(t,
				structerr.NewInvalidArgument("resolving start revision: reference not found"),
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
				StartSha:      commitID.String(),
				EndSha:        "doesntexisting",
			},
			expectedErr: errWithDetails(t,
				structerr.NewInvalidArgument("resolving end revision: reference not found"),
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
				StartSha:      commitID.String(),
				EndSha:        commitID.String(),
			},
			expectedErr: structerr.NewInvalidArgument("empty user name"),
		},
		{
			desc: "author has no name set",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        &gitalypb.User{Email: gittest.TestUser.Email},
				CommitMessage: commitMessage,
				StartSha:      commitID.String(),
				EndSha:        commitID.String(),
			},
			expectedErr: structerr.NewInvalidArgument("empty author name"),
		},
		{
			desc: "author has no email set",
			request: &gitalypb.UserSquashRequest{
				Repository:    repo,
				User:          gittest.TestUser,
				Author:        &gitalypb.User{Name: gittest.TestUser.Name},
				CommitMessage: commitMessage,
				StartSha:      commitID.String(),
				EndSha:        commitID.String(),
			},
			expectedErr: structerr.NewInvalidArgument("empty author email"),
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

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserSquashSquashingMerge)
}

func testUserSquashSquashingMerge(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

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

	expectedCommitID := gittest.ObjectHashDependent(t, map[string]string{
		"sha1":   "69d8db2439502c18b9c17c2d1bddb122a82bd448",
		"sha256": "e4a254d051b8453f5797052d8cfb16cb6041132b00cbcaabc77cfc6b0f59e5ef",
	})

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
		SquashSha: expectedCommitID,
	}, response)
	gittest.RequireTree(t, cfg, repoPath, expectedCommitID, []gittest.TreeEntry{
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
