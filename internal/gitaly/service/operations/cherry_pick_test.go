package operations

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/signature"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestUserCherryPick(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserCherryPick)
}

func testUserCherryPick(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	destinationBranch := "dst-branch"

	type setupData struct {
		cfg                    config.Cfg
		repoPath               string
		repoProto              *gitalypb.Repository
		cherryPickedCommit     *gitalypb.GitCommit
		copyRepoPath           string
		copyRepoProto          *gitalypb.Repository
		copyCherryPickedCommit *gitalypb.GitCommit
		mainCommit             string
	}

	testCases := []struct {
		desc  string
		setup func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error)
	}{
		{
			desc: "branch exists",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				gittest.Exec(t, data.cfg, "-C", data.repoPath, "branch", destinationBranch, git.DefaultBranch)

				return &gitalypb.UserCherryPickRequest{
						Repository: data.repoProto,
						User:       gittest.TestUser,
						Commit:     data.cherryPickedCommit,
						BranchName: []byte(destinationBranch),
						Message:    []byte("Cherry-picking " + data.cherryPickedCommit.Id),
					},
					&gitalypb.UserCherryPickResponse{
						BranchUpdate: &gitalypb.OperationBranchUpdate{},
					},
					nil
			},
		},
		{
			desc: "branch exists + ExpectedOldOId",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				gittest.Exec(t, data.cfg, "-C", data.repoPath, "branch", destinationBranch, git.DefaultBranch)

				return &gitalypb.UserCherryPickRequest{
						Repository:     data.repoProto,
						User:           gittest.TestUser,
						Commit:         data.cherryPickedCommit,
						BranchName:     []byte(destinationBranch),
						Message:        []byte("Cherry-picking " + data.cherryPickedCommit.Id),
						ExpectedOldOid: data.mainCommit,
					},
					&gitalypb.UserCherryPickResponse{
						BranchUpdate: &gitalypb.OperationBranchUpdate{},
					},
					nil
			},
		},
		{
			desc: "nonexistent branch + start_repository == repository",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				return &gitalypb.UserCherryPickRequest{
						Repository:      data.repoProto,
						User:            gittest.TestUser,
						Commit:          data.cherryPickedCommit,
						BranchName:      []byte(destinationBranch),
						Message:         []byte("Cherry-picking " + data.cherryPickedCommit.Id),
						StartBranchName: []byte(git.DefaultBranch),
					},
					&gitalypb.UserCherryPickResponse{
						BranchUpdate: &gitalypb.OperationBranchUpdate{BranchCreated: true},
					},
					nil
			},
		},
		{
			desc: "nonexistent branch + start_repository != repository",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				return &gitalypb.UserCherryPickRequest{
						Repository:      data.repoProto,
						User:            gittest.TestUser,
						Commit:          data.cherryPickedCommit,
						BranchName:      []byte(destinationBranch),
						Message:         []byte("Cherry-picking " + data.cherryPickedCommit.Id),
						StartBranchName: []byte(git.DefaultBranch),
						StartRepository: data.copyRepoProto,
					},
					&gitalypb.UserCherryPickResponse{
						BranchUpdate: &gitalypb.OperationBranchUpdate{BranchCreated: true},
					},
					nil
			},
		},
		{
			desc: "nonexistent branch + empty start_repository",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				return &gitalypb.UserCherryPickRequest{
						Repository:      data.repoProto,
						User:            gittest.TestUser,
						Commit:          data.cherryPickedCommit,
						BranchName:      []byte(destinationBranch),
						Message:         []byte("Cherry-picking " + data.cherryPickedCommit.Id),
						StartBranchName: []byte(git.DefaultBranch),
					},
					&gitalypb.UserCherryPickResponse{
						BranchUpdate: &gitalypb.OperationBranchUpdate{BranchCreated: true},
					},
					nil
			},
		},
		{
			desc: "branch exists with dry run",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				gittest.Exec(t, data.cfg, "-C", data.repoPath, "branch", destinationBranch, git.DefaultBranch)

				return &gitalypb.UserCherryPickRequest{
						Repository:      data.repoProto,
						User:            gittest.TestUser,
						Commit:          data.cherryPickedCommit,
						BranchName:      []byte(destinationBranch),
						Message:         []byte("Cherry-picking " + data.cherryPickedCommit.Id),
						StartBranchName: []byte(git.DefaultBranch),
						DryRun:          true,
					},
					&gitalypb.UserCherryPickResponse{
						BranchUpdate: &gitalypb.OperationBranchUpdate{},
					},
					nil
			},
		},
		{
			desc: "nonexistent branch + start_repository == repository with dry run",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				return &gitalypb.UserCherryPickRequest{
						Repository:      data.repoProto,
						User:            gittest.TestUser,
						Commit:          data.cherryPickedCommit,
						BranchName:      []byte(destinationBranch),
						Message:         []byte("Cherry-picking " + data.cherryPickedCommit.Id),
						StartBranchName: []byte(git.DefaultBranch),
						DryRun:          true,
					},
					&gitalypb.UserCherryPickResponse{
						BranchUpdate: &gitalypb.OperationBranchUpdate{BranchCreated: true},
					},
					nil
			},
		},
		{
			desc: "nonexistent branch + start_repository != repository with dry run",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				return &gitalypb.UserCherryPickRequest{
						Repository:      data.repoProto,
						User:            gittest.TestUser,
						Commit:          data.cherryPickedCommit,
						BranchName:      []byte(destinationBranch),
						Message:         []byte("Cherry-picking " + data.cherryPickedCommit.Id),
						StartBranchName: []byte(git.DefaultBranch),
						StartRepository: data.copyRepoProto,
						DryRun:          true,
					},
					&gitalypb.UserCherryPickResponse{
						BranchUpdate: &gitalypb.OperationBranchUpdate{BranchCreated: true},
					},
					nil
			},
		},
		{
			desc: "nonexistent branch + empty start_repository with dry run",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				return &gitalypb.UserCherryPickRequest{
						Repository:      data.repoProto,
						User:            gittest.TestUser,
						Commit:          data.cherryPickedCommit,
						BranchName:      []byte(destinationBranch),
						Message:         []byte("Cherry-picking " + data.cherryPickedCommit.Id),
						StartBranchName: []byte(git.DefaultBranch),
						DryRun:          true,
					},
					&gitalypb.UserCherryPickResponse{
						BranchUpdate: &gitalypb.OperationBranchUpdate{BranchCreated: true},
					},
					nil
			},
		},
		{
			desc: "invalid ExpectedOldOId",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				gittest.Exec(t, data.cfg, "-C", data.repoPath, "branch", destinationBranch, git.DefaultBranch)

				return &gitalypb.UserCherryPickRequest{
						Repository:     data.repoProto,
						User:           gittest.TestUser,
						Commit:         data.cherryPickedCommit,
						BranchName:     []byte(destinationBranch),
						Message:        []byte("Cherry-picking " + data.cherryPickedCommit.Id),
						ExpectedOldOid: "foobar",
					},
					&gitalypb.UserCherryPickResponse{},
					testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument(fmt.Sprintf("invalid expected old object ID: invalid object ID: \"foobar\", expected length %v, got 6", gittest.DefaultObjectHash.EncodedLen())),
						"old_object_id", "foobar")
			},
		},
		{
			desc: "valid but non-existent ExpectedOldOId",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				gittest.Exec(t, data.cfg, "-C", data.repoPath, "branch", destinationBranch, git.DefaultBranch)

				return &gitalypb.UserCherryPickRequest{
						Repository:     data.repoProto,
						User:           gittest.TestUser,
						Commit:         data.cherryPickedCommit,
						BranchName:     []byte(destinationBranch),
						Message:        []byte("Cherry-picking " + data.cherryPickedCommit.Id),
						ExpectedOldOid: gittest.DefaultObjectHash.ZeroOID.String(),
					},
					&gitalypb.UserCherryPickResponse{},
					testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument("cannot resolve expected old object ID: reference not found"),
						"old_object_id", gittest.DefaultObjectHash.ZeroOID)
			},
		},
		{
			desc: "incorrect ExpectedOldOId",
			setup: func(data setupData) (*gitalypb.UserCherryPickRequest, *gitalypb.UserCherryPickResponse, error) {
				gittest.Exec(t, data.cfg, "-C", data.repoPath, "branch", destinationBranch, git.DefaultBranch)

				commit := gittest.WriteCommit(t, data.cfg, data.repoPath,
					gittest.WithParents(git.ObjectID(data.mainCommit)),
					gittest.WithBranch(git.DefaultBranch),
					gittest.WithTreeEntries(
						gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
					),
				)

				return &gitalypb.UserCherryPickRequest{
						Repository:     data.repoProto,
						User:           gittest.TestUser,
						Commit:         data.cherryPickedCommit,
						BranchName:     []byte(git.DefaultBranch),
						Message:        []byte("Cherry-picking " + data.cherryPickedCommit.Id),
						ExpectedOldOid: data.mainCommit,
					},
					&gitalypb.UserCherryPickResponse{},
					testhelper.WithInterceptedMetadataItems(
						structerr.NewInternal("update reference with hooks: reference update: reference does not point to expected object"),
						structerr.MetadataItem{Key: "actual_object_id", Value: commit},
						structerr.MetadataItem{Key: "expected_object_id", Value: data.mainCommit},
						structerr.MetadataItem{Key: "reference", Value: "refs/heads/main"},
					)
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
			mainCommitID := gittest.WriteCommit(t, cfg, repoPath,
				gittest.WithBranch(git.DefaultBranch),
				gittest.WithTreeEntries(
					gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
				),
			)
			cherryPickCommitID := gittest.WriteCommit(t, cfg, repoPath,
				gittest.WithParents(mainCommitID),
				gittest.WithTreeEntries(
					gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
					gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
				))
			repo := localrepo.NewTestRepo(t, cfg, repoProto)
			cherryPickedCommit, err := repo.ReadCommit(ctx, cherryPickCommitID.Revision())
			require.NoError(t, err)

			copyRepoProto, copyRepoPath := gittest.CreateRepository(t, ctx, cfg)
			mainCommitCopyID := gittest.WriteCommit(t, cfg, copyRepoPath,
				gittest.WithBranch(git.DefaultBranch),
				gittest.WithTreeEntries(
					gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
				),
			)
			cherryPickCommitCopyID := gittest.WriteCommit(t, cfg, copyRepoPath,
				gittest.WithParents(mainCommitCopyID),
				gittest.WithTreeEntries(
					gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
					gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
				))
			copyRepo := localrepo.NewTestRepo(t, cfg, repoProto)
			copyCherryPickedCommit, err := copyRepo.ReadCommit(ctx, cherryPickCommitCopyID.Revision())
			require.NoError(t, err)

			req, expectedResp, expectedErr := tc.setup(setupData{
				cfg:                    cfg,
				repoPath:               repoPath,
				repoProto:              repoProto,
				cherryPickedCommit:     cherryPickedCommit,
				copyRepoPath:           copyRepoPath,
				copyRepoProto:          copyRepoProto,
				copyCherryPickedCommit: copyCherryPickedCommit,
				mainCommit:             mainCommitCopyID.String(),
			})

			resp, err := client.UserCherryPick(ctx, req)
			if expectedErr != nil || err != nil {
				testhelper.RequireGrpcError(t, expectedErr, err)
				return
			}

			headCommit, err := repo.ReadCommit(ctx, git.Revision(destinationBranch))
			require.NoError(t, err)
			expectedResp.BranchUpdate.CommitId = headCommit.Id

			testhelper.ProtoEqual(t, expectedResp, resp)
		})
	}
}

func TestServer_UserCherryPick_successfulGitHooks(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserCherryPickSuccessfulGitHooks)
}

func testServerUserCherryPickSuccessfulGitHooks(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	mainCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
		),
	)
	cherryPickCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(mainCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
		))
	cherryPickedCommit, err := repo.ReadCommit(ctx, cherryPickCommitID.Revision())
	require.NoError(t, err)

	destinationBranch := "cherry-picking-dst"
	gittest.Exec(t, cfg, "-C", repoPath, "branch", destinationBranch, git.DefaultBranch)

	request := &gitalypb.UserCherryPickRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     cherryPickedCommit,
		BranchName: []byte(destinationBranch),
		Message:    []byte("Cherry-picking " + cherryPickedCommit.Id),
	}

	var hookOutputFiles []string
	for _, hookName := range GitlabHooks {
		hookOutputTempPath := gittest.WriteEnvToCustomHook(t, repoPath, hookName)
		hookOutputFiles = append(hookOutputFiles, hookOutputTempPath)
	}

	_, err = client.UserCherryPick(ctx, request)
	require.NoError(t, err)

	for _, file := range hookOutputFiles {
		output := string(testhelper.MustReadFile(t, file))
		require.Contains(t, output, "GL_USERNAME="+gittest.TestUser.GlUsername)
	}
}

func TestServer_UserCherryPick_mergeCommit(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserCherryPickMergeCommit)
}

func testServerUserCherryPickMergeCommit(t *testing.T, ctx context.Context) {
	t.Parallel()

	var opts []testserver.GitalyServerOpt
	if featureflag.GPGSigning.IsEnabled(ctx) {
		opts = append(opts, testserver.WithSigningKey("testdata/signing_ssh_key_ecdsa"))
	}

	ctx, cfg, client := setupOperationsService(t, ctx, opts...)

	if featureflag.GPGSigning.IsEnabled(ctx) {
		testcfg.BuildGitalyGPG(t, cfg)
	}

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	baseCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithMessage("add apple"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
		),
	)

	leftCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(baseCommitID),
		gittest.WithMessage("add banana"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "b", Content: "banana"},
		))
	rightCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(baseCommitID),
		gittest.WithMessage("add coconut"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "c", Content: "coconut"},
		))
	rightCommitID = gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(rightCommitID),
		gittest.WithMessage("add dragon fruit"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "c", Content: "coconut"},
			gittest.TreeEntry{Mode: "100644", Path: "d", Content: "dragon fruit"},
		))
	cherryPickCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(leftCommitID, rightCommitID),
		gittest.WithMessage("merge coconut & dragon fruit into banana"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "b", Content: "banana"},
			gittest.TreeEntry{Mode: "100644", Path: "c", Content: "coconut"},
			gittest.TreeEntry{Mode: "100644", Path: "d", Content: "dragon fruit"},
		))
	cherryPickedCommit, err := repo.ReadCommit(ctx, cherryPickCommitID.Revision())
	require.NoError(t, err)

	destinationBranch := "cherry-picking-dst"
	gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(baseCommitID),
		gittest.WithBranch(destinationBranch),
		gittest.WithMessage("add zucchini"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "z", Content: "zucchini"},
		),
	)

	request := &gitalypb.UserCherryPickRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     cherryPickedCommit,
		BranchName: []byte(destinationBranch),
		Message:    []byte("Cherry-picking " + cherryPickedCommit.Id),
	}

	response, err := client.UserCherryPick(ctx, request)
	require.NoError(t, err)

	gittest.RequireTree(t, cfg, repoPath, response.BranchUpdate.CommitId,
		[]gittest.TreeEntry{
			{Mode: "100644", Path: "a", Content: "apple"},
			{Mode: "100644", Path: "c", Content: "coconut"},
			{Mode: "100644", Path: "d", Content: "dragon fruit"},
			{Mode: "100644", Path: "z", Content: "zucchini"},
		})

	if featureflag.GPGSigning.IsEnabled(ctx) {
		data, err := repo.ReadObject(ctx, git.ObjectID(response.BranchUpdate.CommitId))
		require.NoError(t, err)

		gpgsig, dataWithoutGpgSig := signature.ExtractSignature(t, ctx, data)

		signingKey, err := signature.ParseSigningKeys("testdata/signing_ssh_key_ecdsa")
		require.NoError(t, err)

		require.NoError(t, signingKey.Verify([]byte(gpgsig), []byte(dataWithoutGpgSig)))
	}
}

func TestServer_UserCherryPick_stableID(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserCherryPickStableID)
}

func testServerUserCherryPickStableID(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	tests := []struct {
		desc                 string
		commitAuthorName     []byte
		commitAuthorEmail    []byte
		expectedCommitID     string
		expectedCommitAuthor *gitalypb.CommitAuthor
	}{
		{
			desc: "when commit author name and email are not specified",
			expectedCommitID: gittest.ObjectHashDependent(t, map[string]string{
				"sha1":   "e4e27d5484b1862f887391fe3417fecb95ee4fef",
				"sha256": "e0cc4d237718edca19b01bfe90d8742a940b82aa7596124d46f6ac02818bf232",
			}),
			expectedCommitAuthor: gittest.DefaultCommitAuthor,
		},
		{
			desc:              "when commit author name and email are specified",
			commitAuthorName:  gittest.TestUser.Name,
			commitAuthorEmail: gittest.TestUser.Email,
			expectedCommitID: gittest.ObjectHashDependent(t, map[string]string{
				"sha1":   "6a7a5c497966ebabda9518c9271d73863288e047",
				"sha256": "0074c3a0c3a1fe99b9eb2d1331e9716afcc15c08f477d19e66794950363dfebb",
			}),
			expectedCommitAuthor: &gitalypb.CommitAuthor{
				Name:  gittest.TestUser.Name,
				Email: gittest.TestUser.Email,
				Date: &timestamppb.Timestamp{
					Seconds: 12345,
				},
				Timezone: []byte(gittest.TimezoneOffset),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			mainCommitID := gittest.WriteCommit(t, cfg, repoPath,
				gittest.WithBranch(git.DefaultBranch),
				gittest.WithTreeEntries(
					gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
				),
			)
			cherryPickCommitID := gittest.WriteCommit(t, cfg, repoPath,
				gittest.WithParents(mainCommitID),
				gittest.WithTreeEntries(
					gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
					gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
				))
			require.Equal(t,
				git.ObjectID(gittest.ObjectHashDependent(t, map[string]string{
					"sha1":   "9f5cd015ffce347a87946a31884d85c2d5ac76c6",
					"sha256": "685ed70f40309daf137b214b116084bb3cb64948ffe21894da60cffdacb328d9",
				})),
				cherryPickCommitID)

			cherryPickedCommit, err := repo.ReadCommit(ctx, cherryPickCommitID.Revision())
			require.NoError(t, err)

			destinationBranch := "cherry-picking-dst"
			gittest.Exec(t, cfg, "-C", repoPath, "branch", destinationBranch, git.DefaultBranch)

			request := &gitalypb.UserCherryPickRequest{
				Repository:        repoProto,
				User:              gittest.TestUser,
				Commit:            cherryPickedCommit,
				BranchName:        []byte(destinationBranch),
				Message:           []byte("Cherry-picking " + cherryPickedCommit.Id),
				Timestamp:         &timestamppb.Timestamp{Seconds: 12345},
				CommitAuthorName:  tc.commitAuthorName,
				CommitAuthorEmail: tc.commitAuthorEmail,
			}

			response, err := client.UserCherryPick(ctx, request)
			require.NoError(t, err)

			require.Equal(t, tc.expectedCommitID, response.BranchUpdate.CommitId)

			pickCommit, err := repo.ReadCommit(ctx, git.Revision(response.BranchUpdate.CommitId))
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.GitCommit{
				Id:      tc.expectedCommitID,
				Subject: []byte("Cherry-picking " + cherryPickedCommit.Id),
				Body:    []byte("Cherry-picking " + cherryPickedCommit.Id),
				BodySize: gittest.ObjectHashDependent(t, map[string]int64{
					"sha1":   55,
					"sha256": 79,
				}),
				ParentIds: gittest.ObjectHashDependent(t, map[string][]string{
					"sha1":   {"6bb1e1bbf6de505981564b780ca28dc31cd64838"},
					"sha256": {"996522a8ec52ad134174a543b8679edd6e7759b5149b5bf99edb463283588dd8"},
				}),
				TreeId: gittest.ObjectHashDependent(t, map[string]string{
					"sha1":   "0ff2fa5961cbe4fc6371bc5cb1a4eb3b314f308f",
					"sha256": "bda92f49a13c07c80c816b7c9965c6c7e76f0f5f75aa9ee0453f7109bfb27f7b",
				}),
				Author: tc.expectedCommitAuthor,
				Committer: &gitalypb.CommitAuthor{
					Name:  gittest.TestUser.Name,
					Email: gittest.TestUser.Email,
					Date: &timestamppb.Timestamp{
						Seconds: 12345,
					},
					Timezone: []byte(gittest.TimezoneOffset),
				},
			}, pickCommit)
		})
	}
}

func TestServer_UserCherryPick_failedValidations(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserCherryPickFailedValidations)
}

func testServerUserCherryPickFailedValidations(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	mainCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
		),
	)
	cherryPickCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(mainCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
		))
	cherryPickedCommit, err := repo.ReadCommit(ctx, cherryPickCommitID.Revision())
	require.NoError(t, err)

	destinationBranch := "cherry-picking-dst"
	gittest.Exec(t, cfg, "-C", repoPath, "branch", destinationBranch, git.DefaultBranch)

	testCases := []struct {
		desc        string
		request     *gitalypb.UserCherryPickRequest
		expectedErr error
	}{
		{
			desc: "no repository provided",
			request: &gitalypb.UserCherryPickRequest{
				Repository: nil,
			},
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc: "empty user",
			request: &gitalypb.UserCherryPickRequest{
				Repository: repoProto,
				User:       nil,
				Commit:     cherryPickedCommit,
				BranchName: []byte(destinationBranch),
				Message:    []byte("Cherry-picking " + cherryPickedCommit.Id),
			},
			expectedErr: status.Error(codes.InvalidArgument, "empty User"),
		},
		{
			desc: "empty commit",
			request: &gitalypb.UserCherryPickRequest{
				Repository: repoProto,
				User:       gittest.TestUser,
				Commit:     nil,
				BranchName: []byte(destinationBranch),
				Message:    []byte("Cherry-picking " + cherryPickedCommit.Id),
			},
			expectedErr: status.Error(codes.InvalidArgument, "empty Commit"),
		},
		{
			desc: "empty branch name",
			request: &gitalypb.UserCherryPickRequest{
				Repository: repoProto,
				User:       gittest.TestUser,
				Commit:     cherryPickedCommit,
				BranchName: nil,
				Message:    []byte("Cherry-picking " + cherryPickedCommit.Id),
			},
			expectedErr: status.Error(codes.InvalidArgument, "empty BranchName"),
		},
		{
			desc: "empty message",
			request: &gitalypb.UserCherryPickRequest{
				Repository: repoProto,
				User:       gittest.TestUser,
				Commit:     cherryPickedCommit,
				BranchName: []byte(destinationBranch),
				Message:    nil,
			},
			expectedErr: status.Error(codes.InvalidArgument, "empty Message"),
		},
		{
			desc: "commit not found",
			request: &gitalypb.UserCherryPickRequest{
				Repository: repoProto,
				User:       gittest.TestUser,
				Commit:     &gitalypb.GitCommit{Id: "will-not-be-found"},
				BranchName: []byte(destinationBranch),
				Message:    []byte("Cherry-picking not found"),
			},
			expectedErr: status.Error(codes.NotFound, `cherry-pick: commit lookup: commit not found: "will-not-be-found"`),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			_, err := client.UserCherryPick(ctx, testCase.request)
			testhelper.RequireGrpcError(t, testCase.expectedErr, err)
		})
	}
}

func TestServer_UserCherryPick_failedWithPreReceiveError(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserCherryPickFailedWithPreReceiveError)
}

func testServerUserCherryPickFailedWithPreReceiveError(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	mainCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
		),
	)
	cherryPickCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(mainCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
		))
	cherryPickedCommit, err := repo.ReadCommit(ctx, cherryPickCommitID.Revision())
	require.NoError(t, err)

	destinationBranch := "cherry-picking-dst"
	gittest.Exec(t, cfg, "-C", repoPath, "branch", destinationBranch, git.DefaultBranch)

	request := &gitalypb.UserCherryPickRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     cherryPickedCommit,
		BranchName: []byte(destinationBranch),
		Message:    []byte("Cherry-picking " + cherryPickedCommit.Id),
	}

	hookContent := []byte("#!/bin/sh\necho GL_ID=$GL_ID\nexit 1")

	for _, hookName := range GitlabPreHooks {
		t.Run(hookName, func(t *testing.T) {
			gittest.WriteCustomHook(t, repoPath, hookName, hookContent)

			response, err := client.UserCherryPick(ctx, request)
			require.Nil(t, response)
			testhelper.RequireGrpcError(t, structerr.NewFailedPrecondition("access check failed").WithDetail(
				&gitalypb.UserCherryPickError{
					Error: &gitalypb.UserCherryPickError_AccessCheck{
						AccessCheck: &gitalypb.AccessCheckError{
							ErrorMessage: "GL_ID=user-123",
						},
					},
				},
			), err)
		})
	}
}

func TestServer_UserCherryPick_failedWithCreateTreeError(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserCherryPickFailedWithCreateTreeError)
}

func testServerUserCherryPickFailedWithCreateTreeError(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	mainCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
		),
	)
	cherryPickCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(mainCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
		))
	cherryPickedCommit, err := repo.ReadCommit(ctx, cherryPickCommitID.Revision())
	require.NoError(t, err)

	destinationBranch := "cherry-picking-dst"
	gittest.Exec(t, cfg, "-C", repoPath, "branch", destinationBranch, git.DefaultBranch)

	request := &gitalypb.UserCherryPickRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     cherryPickedCommit,
		BranchName: []byte(destinationBranch),
		Message:    []byte("Cherry-picking " + cherryPickedCommit.Id),
	}

	response, err := client.UserCherryPick(ctx, request)
	require.Nil(t, response)
	testhelper.RequireGrpcError(t, structerr.NewFailedPrecondition("cherry-pick: could not apply because the result was empty").WithDetail(
		&gitalypb.UserCherryPickError{
			Error: &gitalypb.UserCherryPickError_ChangesAlreadyApplied{},
		},
	), err)
}

func TestServer_UserCherryPick_failedWithCommitError(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserCherryPickFailedWithCommitError)
}

func testServerUserCherryPickFailedWithCommitError(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	mainCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
		),
	)

	destinationBranch := "cherry-picking-dst"
	gittest.Exec(t, cfg, "-C", repoPath, "branch", destinationBranch, git.DefaultBranch)

	sourceBranch := "cherry-pick-src"
	cherryPickCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(sourceBranch),
		gittest.WithParents(mainCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "b", Content: "banana"},
		),
	)
	cherryPickedCommit, err := repo.ReadCommit(ctx, cherryPickCommitID.Revision())
	require.NoError(t, err)

	request := &gitalypb.UserCherryPickRequest{
		Repository:      repoProto,
		User:            gittest.TestUser,
		Commit:          cherryPickedCommit,
		BranchName:      []byte(sourceBranch),
		Message:         []byte("Cherry-picking " + cherryPickedCommit.Id),
		StartBranchName: []byte(destinationBranch),
	}

	response, err := client.UserCherryPick(ctx, request)
	require.Nil(t, response)
	s, ok := status.FromError(err)
	require.True(t, ok)

	details := s.Details()
	require.Len(t, details, 1)
	detailedErr, ok := details[0].(*gitalypb.UserCherryPickError)
	require.True(t, ok)

	targetBranchDivergedErr, ok := detailedErr.Error.(*gitalypb.UserCherryPickError_TargetBranchDiverged)
	require.True(t, ok)
	assert.Equal(t, []byte(cherryPickCommitID.String()), targetBranchDivergedErr.TargetBranchDiverged.ParentRevision)
}

func TestServer_UserCherryPick_failedWithConflict(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserCherryPickFailedWithConflict)
}

func testServerUserCherryPickFailedWithConflict(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	mainCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
		),
	)
	cherryPickCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(mainCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
		))
	cherryPickedCommit, err := repo.ReadCommit(ctx, cherryPickCommitID.Revision())
	require.NoError(t, err)

	destinationBranch := "cherry-picking-dst"
	gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(mainCommitID),
		gittest.WithBranch(destinationBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "buzz"},
		),
	)

	request := &gitalypb.UserCherryPickRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     cherryPickedCommit,
		BranchName: []byte(destinationBranch),
		Message:    []byte("Cherry-picking " + cherryPickedCommit.Id),
	}

	response, err := client.UserCherryPick(ctx, request)
	require.Nil(t, response)
	s, ok := status.FromError(err)
	require.True(t, ok)

	details := s.Details()
	require.Len(t, details, 1)
	detailedErr, ok := details[0].(*gitalypb.UserCherryPickError)
	require.True(t, ok)

	conflictErr, ok := detailedErr.Error.(*gitalypb.UserCherryPickError_CherryPickConflict)
	require.True(t, ok)
	require.Len(t, conflictErr.CherryPickConflict.ConflictingFiles, 1)
	assert.Equal(t, []byte("foo"), conflictErr.CherryPickConflict.ConflictingFiles[0])
}

func TestServer_UserCherryPick_successfulWithGivenCommits(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserCherryPickSuccessfulWithGivenCommits)
}

func testServerUserCherryPickSuccessfulWithGivenCommits(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	mainCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
		),
	)
	cherryPickCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(mainCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
		))

	testCases := []struct {
		desc           string
		startRevision  git.Revision
		cherryRevision git.Revision
	}{
		{
			desc:           "merge commit",
			startRevision:  mainCommitID.Revision(),
			cherryRevision: cherryPickCommitID.Revision(),
		},
	}

	for i, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			destinationBranch := fmt.Sprintf("cherry-picking-%d", i)

			gittest.Exec(t, cfg, "-C", repoPath, "branch", destinationBranch, testCase.startRevision.String())

			commit, err := repo.ReadCommit(ctx, testCase.cherryRevision)
			require.NoError(t, err)

			request := &gitalypb.UserCherryPickRequest{
				Repository: repoProto,
				User:       gittest.TestUser,
				Commit:     commit,
				BranchName: []byte(destinationBranch),
				Message:    []byte("Cherry-picking " + testCase.cherryRevision.String()),
			}

			response, err := client.UserCherryPick(ctx, request)
			require.NoError(t, err)

			newHead, err := repo.ReadCommit(ctx, git.Revision(destinationBranch))
			require.NoError(t, err)

			expectedResponse := &gitalypb.UserCherryPickResponse{
				BranchUpdate: &gitalypb.OperationBranchUpdate{CommitId: newHead.Id},
			}

			testhelper.ProtoEqual(t, expectedResponse, response)

			require.Equal(t, request.Message, newHead.Subject)
			require.Equal(t, testCase.startRevision.String(), newHead.ParentIds[0])
		})
	}
}

func TestServer_UserCherryPick_quarantine(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserCherryPickQuarantine)
}

func testServerUserCherryPickQuarantine(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	mainCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
		),
	)

	cherryPickCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(mainCommitID),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "foo", Content: "bar"},
		))
	cherryPickedCommit, err := repo.ReadCommit(ctx, cherryPickCommitID.Revision())
	require.NoError(t, err)

	// Set up a hook that parses the new object and then aborts the update. Like this, we can
	// assert that the object does not end up in the main repository.
	outputPath := filepath.Join(testhelper.TempDir(t), "output")
	gittest.WriteCustomHook(t, repoPath, "pre-receive", []byte(fmt.Sprintf(
		`#!/bin/sh
		read oldval newval ref &&
		git rev-parse $newval^{commit} >%s &&
		exit 1
		`, outputPath)))

	request := &gitalypb.UserCherryPickRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     cherryPickedCommit,
		BranchName: []byte("refs/heads/main"),
		Message:    []byte("Message"),
	}

	response, err := client.UserCherryPick(ctx, request)
	require.Nil(t, response)
	require.NotNil(t, err)
	require.Contains(t, err.Error(), "access check failed")

	objectHash, err := repo.ObjectHash(ctx)
	require.NoError(t, err)

	hookOutput := testhelper.MustReadFile(t, outputPath)
	oid, err := objectHash.FromHex(text.ChompBytes(hookOutput))
	require.NoError(t, err)
	exists, err := repo.HasRevision(ctx, oid.Revision()+"^{commit}")
	require.NoError(t, err)

	require.False(t, exists, "quarantined commit should have been discarded")
}

func TestServer_UserCherryPick_reverse(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserCherryPickReverse)
}

func testServerUserCherryPickReverse(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	// All the tree entries that will eventually end up in the destinationBranch
	treeEntries := []gittest.TreeEntry{
		{Mode: "100644", Path: "a", Content: "apple"},
		{Mode: "100644", Path: "b", Content: "banana"},
		// The above are in the destination, the below are committed one by one in the source
		{Mode: "100644", Path: "c", Content: "coconut"},
		{Mode: "100644", Path: "d", Content: "dragon fruit"},
		{Mode: "100644", Path: "e", Content: "eggplant"},
		{Mode: "100644", Path: "f", Content: "fig"},
	}

	mainCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(treeEntries[:1]...),
	)
	destinationBranch := "cherry-picking-dst"

	var destinationTree, cherryTree []gittest.TreeEntry
	destinationTree = append(destinationTree, treeEntries[:2]...)
	gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(mainCommitID),
		gittest.WithBranch(destinationBranch),
		gittest.WithTreeEntries(destinationTree...),
	)

	cherryTree = append(cherryTree, treeEntries[0])

	var cherryPicking []git.ObjectID
	parentCommitID := mainCommitID

	for _, entry := range treeEntries[2:] {
		cherryTree = append(cherryTree, entry)

		commitID := gittest.WriteCommit(t, cfg, repoPath,
			gittest.WithParents(parentCommitID),
			gittest.WithTreeEntries(cherryTree...),
			gittest.WithMessage("planting "+entry.Content+" trees"),
		)
		cherryPicking = append(cherryPicking, commitID)

		parentCommitID = commitID
	}

	for i := len(cherryPicking) - 1; i >= 0; i-- {
		cherryPickCommit, err := repo.ReadCommit(ctx, cherryPicking[i].Revision())
		require.NoError(t, err)

		request := &gitalypb.UserCherryPickRequest{
			Repository: repoProto,
			User:       gittest.TestUser,
			Commit:     cherryPickCommit,
			BranchName: []byte(destinationBranch),
			Message:    []byte("Cherry-picking " + cherryPickCommit.Id),
		}

		response, err := client.UserCherryPick(ctx, request)
		require.NoError(t, err)

		gittest.RequireTree(t, cfg, repoPath, response.BranchUpdate.CommitId,
			append(destinationTree, treeEntries[i+2:]...),
		)
	}
}
