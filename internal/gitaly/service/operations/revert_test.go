package operations

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/featureflag"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
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

func TestUserRevert(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testUserRevert)
}

func testUserRevert(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)

	branchName := "revert-branch"

	type setupData struct {
		expectedCommitID string
		repoPath         string
		request          *gitalypb.UserRevertRequest
		expectedResponse *gitalypb.UserRevertResponse
		expectedError    error
	}

	testCases := []struct {
		desc  string
		setup func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData
	}{
		{
			desc: "successful",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				firstCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(branchName), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "foobar"},
				))
				firstCommit, err := repo.ReadCommit(ctx, firstCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.UserRevertRequest{
						Repository: repoProto,
						User:       gittest.TestUser,
						Commit:     firstCommit,
						BranchName: []byte(branchName),
						Message:    []byte("Reverting " + firstCommitID),
					},
					expectedResponse: &gitalypb.UserRevertResponse{BranchUpdate: &gitalypb.OperationBranchUpdate{}},
					expectedError:    nil,
				}
			},
		},
		{
			desc: "nonexistent branch + start_repository == repository",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				firstCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "foobar"},
				))
				firstCommit, err := repo.ReadCommit(ctx, firstCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.UserRevertRequest{
						Repository:      repoProto,
						User:            gittest.TestUser,
						Commit:          firstCommit,
						BranchName:      []byte(branchName),
						StartBranchName: []byte(git.DefaultBranch),
						Message:         []byte("Reverting " + firstCommitID),
					},
					expectedResponse: &gitalypb.UserRevertResponse{BranchUpdate: &gitalypb.OperationBranchUpdate{
						BranchCreated: true,
					}},
					expectedError: nil,
				}
			},
		},
		{
			desc: "nonexistent branch + start_repository != repository",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				startRepoProto, startRepoPath := gittest.CreateRepository(t, ctx, cfg)
				startRepo := localrepo.NewTestRepo(t, cfg, startRepoProto)

				firstCommitID := gittest.WriteCommit(t, cfg, startRepoPath, gittest.WithBranch(git.DefaultBranch), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "foobar"},
				))
				firstCommit, err := startRepo.ReadCommit(ctx, firstCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.UserRevertRequest{
						Repository:      repoProto,
						User:            gittest.TestUser,
						Commit:          firstCommit,
						BranchName:      []byte(branchName),
						StartBranchName: []byte(git.DefaultBranch),
						StartRepository: startRepoProto,
						Message:         []byte("Reverting " + firstCommitID),
					},
					expectedResponse: &gitalypb.UserRevertResponse{BranchUpdate: &gitalypb.OperationBranchUpdate{BranchCreated: true, RepoCreated: true}},
					expectedError:    nil,
				}
			},
		},
		{
			desc: "successful with dry run",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				firstCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(branchName), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "foobar"},
				))
				firstCommit, err := repo.ReadCommit(ctx, firstCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.UserRevertRequest{
						Repository: repoProto,
						User:       gittest.TestUser,
						Commit:     firstCommit,
						BranchName: []byte(branchName),
						Message:    []byte("Reverting " + firstCommitID),
						DryRun:     true,
					},
					expectedCommitID: firstCommit.Id,
					expectedResponse: &gitalypb.UserRevertResponse{BranchUpdate: &gitalypb.OperationBranchUpdate{}},
					expectedError:    nil,
				}
			},
		},
		{
			desc: "nonexistent branch + start_repository == repository with dry run",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				firstCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "foobar"},
				))
				firstCommit, err := repo.ReadCommit(ctx, firstCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.UserRevertRequest{
						Repository:      repoProto,
						User:            gittest.TestUser,
						Commit:          firstCommit,
						BranchName:      []byte(branchName),
						StartBranchName: []byte(git.DefaultBranch),
						Message:         []byte("Reverting " + firstCommitID),
						DryRun:          true,
					},
					expectedCommitID: firstCommit.Id,
					expectedResponse: &gitalypb.UserRevertResponse{BranchUpdate: &gitalypb.OperationBranchUpdate{
						BranchCreated: true,
					}},
					expectedError: nil,
				}
			},
		},
		{
			desc: "nonexistent branch + start_repository != repository with dry run",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				startRepoProto, startRepoPath := gittest.CreateRepository(t, ctx, cfg)
				startRepo := localrepo.NewTestRepo(t, cfg, startRepoProto)

				firstCommitID := gittest.WriteCommit(t, cfg, startRepoPath, gittest.WithBranch(git.DefaultBranch), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "foobar"},
				))
				firstCommit, err := startRepo.ReadCommit(ctx, firstCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.UserRevertRequest{
						Repository:      repoProto,
						User:            gittest.TestUser,
						Commit:          firstCommit,
						BranchName:      []byte(branchName),
						StartBranchName: []byte(git.DefaultBranch),
						StartRepository: startRepoProto,
						Message:         []byte("Reverting " + firstCommitID),
						DryRun:          true,
					},
					expectedCommitID: firstCommitID.String(),
					expectedResponse: &gitalypb.UserRevertResponse{BranchUpdate: &gitalypb.OperationBranchUpdate{BranchCreated: true, RepoCreated: true}},
					expectedError:    nil,
				}
			},
		},

		{
			desc: "no repository provided",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				return setupData{
					request: &gitalypb.UserRevertRequest{
						Repository: nil,
					},
					expectedError: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
				}
			},
		},
		{
			desc: "empty user",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				return setupData{
					request: &gitalypb.UserRevertRequest{
						Repository: repoProto,
					},
					expectedError: structerr.NewInvalidArgument("empty User"),
				}
			},
		},
		{
			desc: "empty commit",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				return setupData{
					request: &gitalypb.UserRevertRequest{
						Repository: repoProto,
						User:       gittest.TestUser,
					},
					expectedError: structerr.NewInvalidArgument("empty Commit"),
				}
			},
		},
		{
			desc: "empty branch name",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				firstCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(branchName), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "foobar"},
				))
				firstCommit, err := repo.ReadCommit(ctx, firstCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					request: &gitalypb.UserRevertRequest{
						Repository: repoProto,
						User:       gittest.TestUser,
						Commit:     firstCommit,
						Message:    []byte("Reverting " + firstCommitID),
					},
					expectedError: structerr.NewInvalidArgument("empty BranchName"),
				}
			},
		},
		{
			desc: "empty message",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				firstCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(branchName), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "foobar"},
				))
				firstCommit, err := repo.ReadCommit(ctx, firstCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					request: &gitalypb.UserRevertRequest{
						Repository: repoProto,
						User:       gittest.TestUser,
						Commit:     firstCommit,
						BranchName: []byte(branchName),
					},
					expectedError: structerr.NewInvalidArgument("empty Message"),
				}
			},
		},
		{
			desc: "successful + expectedOldOID",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				firstCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(branchName), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "foobar"},
				))
				firstCommit, err := repo.ReadCommit(ctx, firstCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					repoPath: repoPath,
					request: &gitalypb.UserRevertRequest{
						Repository:     repoProto,
						User:           gittest.TestUser,
						Commit:         firstCommit,
						BranchName:     []byte(branchName),
						Message:        []byte("Reverting " + firstCommitID),
						ExpectedOldOid: firstCommitID.String(),
					},
					expectedResponse: &gitalypb.UserRevertResponse{BranchUpdate: &gitalypb.OperationBranchUpdate{}},
					expectedError:    nil,
				}
			},
		},
		{
			desc: "successful + invalid expectedOldOID",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				firstCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(branchName), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "foobar"},
				))
				firstCommit, err := repo.ReadCommit(ctx, firstCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					request: &gitalypb.UserRevertRequest{
						Repository:     repoProto,
						User:           gittest.TestUser,
						Commit:         firstCommit,
						BranchName:     []byte(branchName),
						Message:        []byte("Reverting " + firstCommitID),
						ExpectedOldOid: "foobar",
					},
					expectedError: testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument(fmt.Sprintf(`invalid expected old object ID: invalid object ID: "foobar", expected length %v, got 6`, gittest.DefaultObjectHash.EncodedLen())),
						"old_object_id", "foobar"),
				}
			},
		},
		{
			desc: "expectedOldOID with valid SHA, but not present in repo",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				firstCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(branchName), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "foobar"},
				))
				firstCommit, err := repo.ReadCommit(ctx, firstCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					request: &gitalypb.UserRevertRequest{
						Repository:     repoProto,
						User:           gittest.TestUser,
						Commit:         firstCommit,
						BranchName:     []byte(branchName),
						Message:        []byte("Reverting " + firstCommitID),
						ExpectedOldOid: gittest.DefaultObjectHash.ZeroOID.String(),
					},
					expectedError: testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument("cannot resolve expected old object ID: reference not found"),
						"old_object_id", gittest.DefaultObjectHash.ZeroOID),
				}
			},
		},
		{
			desc: "expectedOldOID pointing to old commit",
			setup: func(t *testing.T, repoPath string, repoProto *gitalypb.Repository, repo *localrepo.Repo) setupData {
				firstCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "bar"},
				))
				secondCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(firstCommitID), gittest.WithBranch(branchName), gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "bolb", Mode: "100644", Content: "foo"},
				))
				secondCommit, err := repo.ReadCommit(ctx, secondCommitID.Revision())
				require.NoError(t, err)

				return setupData{
					request: &gitalypb.UserRevertRequest{
						Repository:     repoProto,
						User:           gittest.TestUser,
						Commit:         secondCommit,
						BranchName:     []byte(branchName),
						Message:        []byte("Reverting " + secondCommitID),
						ExpectedOldOid: firstCommitID.String(),
					},
					expectedError: testhelper.WithInterceptedMetadataItems(
						structerr.NewInternal("update reference with hooks: reference update: reference does not point to expected object"),
						structerr.MetadataItem{Key: "actual_object_id", Value: secondCommitID},
						structerr.MetadataItem{Key: "expected_object_id", Value: firstCommitID},
						structerr.MetadataItem{Key: "reference", Value: "refs/heads/" + branchName},
					),
				}
			},
		},
	}

	for _, tc := range testCases {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			data := tc.setup(t, repoPath, repoProto, repo)

			response, err := client.UserRevert(ctx, data.request)
			testhelper.RequireGrpcError(t, data.expectedError, err)

			if data.expectedError != nil {
				return
			}

			branchCommitID, err := repo.ResolveRevision(ctx, git.Revision(branchName))
			require.NoError(t, err)
			data.expectedResponse.BranchUpdate.CommitId = branchCommitID.String()

			// For dry-run, we only skip the `update-ref` section, so a non-existent branch
			// will be created by `UserRevert`. But, we need to ensure that the
			// expectedCommitID of the branch on which we requested revert doesn't change.
			if data.expectedCommitID != "" {
				require.Equal(t, data.expectedCommitID, branchCommitID.String(), "dry run should point at expected commit")
			}

			testhelper.ProtoEqual(t, data.expectedResponse, response)
		})
	}
}

func TestServer_UserRevert_quarantine(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
		featureflag.ReturnStructuredErrorsInUserRevert,
	).Run(t, testServerUserRevertQuarantine)
}

func testServerUserRevertQuarantine(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	destinationBranch := "reverting-dst"

	// Set up a hook that parses the new object and then aborts the update. Like this, we can
	// assert that the object does not end up in the main repository.
	outputPath := filepath.Join(testhelper.TempDir(t), "output")
	gittest.WriteCustomHook(t, repoPath, "pre-receive", []byte(fmt.Sprintf(
		`#!/bin/sh
		read oldval newval ref &&
		git rev-parse $newval^{commit} >%s &&
		exit 1
	`, outputPath)))

	revertedCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("add apple"),
		gittest.WithBranch(destinationBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
		),
	)

	revertedCommit, err := repo.ReadCommit(ctx, revertedCommitID.Revision())
	require.NoError(t, err)

	response, err := client.UserRevert(ctx, &gitalypb.UserRevertRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     revertedCommit,
		BranchName: []byte(destinationBranch),
		Message:    []byte("Reverting " + revertedCommit.Id),
		Timestamp:  &timestamppb.Timestamp{Seconds: 12345},
	})
	if featureflag.ReturnStructuredErrorsInUserRevert.IsDisabled(ctx) {
		require.NoError(t, err)
		require.NotNil(t, response)
		require.NotEmpty(t, response.PreReceiveError)
	} else {
		expectedError := structerr.NewPermissionDenied("revert: custom hook error").WithDetail(
			&gitalypb.UserRevertError{
				Error: &gitalypb.UserRevertError_CustomHook{
					CustomHook: &gitalypb.CustomHookError{
						HookType: gitalypb.CustomHookError_HOOK_TYPE_PRERECEIVE,
					},
				},
			})
		testhelper.RequireGrpcError(t, expectedError, err)
	}

	objectHash, err := repo.ObjectHash(ctx)
	require.NoError(t, err)

	hookOutput := testhelper.MustReadFile(t, outputPath)
	oid, err := objectHash.FromHex(text.ChompBytes(hookOutput))
	require.NoError(t, err)
	exists, err := repo.HasRevision(ctx, oid.Revision()+"^{commit}")
	require.NoError(t, err)
	require.False(t, exists, "quarantined commit should have been discarded")
}

func TestServer_UserRevert_mergeCommit(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
	).Run(t, testServerUserRevertMergeCommit)
}

func testServerUserRevertMergeCommit(t *testing.T, ctx context.Context) {
	t.Parallel()

	var opts []testserver.GitalyServerOpt
	if featureflag.GPGSigning.IsEnabled(ctx) {
		opts = append(opts, testserver.WithSigningKey("testdata/signing_ssh_key_rsa"))
	}

	ctx, cfg, client := setupOperationsService(t, ctx, opts...)

	if featureflag.GPGSigning.IsEnabled(ctx) {
		testcfg.BuildGitalyGPG(t, cfg)
	}

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	baseCommitID := gittest.WriteCommit(t, cfg, repoPath,
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
	rightCommitID01 := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(baseCommitID),
		gittest.WithMessage("add coconut"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "c", Content: "coconut"},
		))
	rightCommitID02 := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(rightCommitID01),
		gittest.WithMessage("add dragon fruit"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "c", Content: "coconut"},
			gittest.TreeEntry{Mode: "100644", Path: "d", Content: "dragon fruit"},
		))
	mergedCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(leftCommitID, rightCommitID02),
		gittest.WithMessage("merge coconut & dragon fruit into banana"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "b", Content: "banana"},
			gittest.TreeEntry{Mode: "100644", Path: "c", Content: "coconut"},
			gittest.TreeEntry{Mode: "100644", Path: "d", Content: "dragon fruit"},
		))
	mergedCommit, err := repo.ReadCommit(ctx, mergedCommitID.Revision())
	require.NoError(t, err)

	destinationBranch := "reverting-dst"
	gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(mergedCommitID),
		gittest.WithBranch(destinationBranch),
		gittest.WithMessage("add zucchini"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "a", Content: "apple"},
			gittest.TreeEntry{Mode: "100644", Path: "b", Content: "banana"},
			gittest.TreeEntry{Mode: "100644", Path: "c", Content: "coconut"},
			gittest.TreeEntry{Mode: "100644", Path: "d", Content: "dragon fruit"},
			gittest.TreeEntry{Mode: "100644", Path: "z", Content: "zucchini"},
		),
	)

	request := &gitalypb.UserRevertRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     mergedCommit,
		BranchName: []byte(destinationBranch),
		Message:    []byte("Reverting " + mergedCommit.Id),
	}

	response, err := client.UserRevert(ctx, request)
	require.NoError(t, err)

	gittest.RequireTree(t, cfg, repoPath, response.BranchUpdate.CommitId,
		[]gittest.TreeEntry{
			{Mode: "100644", Path: "a", Content: "apple"},
			{Mode: "100644", Path: "b", Content: "banana"},
			{Mode: "100644", Path: "z", Content: "zucchini"},
		})

	if featureflag.GPGSigning.IsEnabled(ctx) {
		data, err := repo.ReadObject(ctx, git.ObjectID(response.BranchUpdate.CommitId))
		require.NoError(t, err)

		gpgsig, dataWithoutGpgSig := signature.ExtractSignature(t, ctx, data)

		signingKey, err := signature.ParseSigningKeys("testdata/signing_ssh_key_rsa")
		require.NoError(t, err)

		require.NoError(t, signingKey.Verify([]byte(gpgsig), []byte(dataWithoutGpgSig)))
	}
}

func TestServer_UserRevert_stableID(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
		featureflag.ReturnStructuredErrorsInUserRevert,
	).Run(t, testServerUserRevertStableID)
}

func testServerUserRevertStableID(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	initCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("add blob1"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "blob1", Content: "foobar1"},
		),
	)
	revertedCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(initCommitID),
		gittest.WithMessage("add blob2"),
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Mode: "100644", Path: "blob1", Content: "foobar1"},
			gittest.TreeEntry{Mode: "100644", Path: "blob2", Content: "foobar2"},
		),
	)

	require.Equal(t,
		git.ObjectID(gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "d7c4ed5ffd773b3316f04fdd5d9d6f13591429ae",
			"sha256": "eb2185ab72ce9d83c8f4f551914266d7de2d97affa208f884b594ae1b60f8130",
		})),
		revertedCommitID,
	)

	revertedCommit, err := repo.ReadCommit(ctx, revertedCommitID.Revision())
	require.NoError(t, err)

	response, err := client.UserRevert(ctx, &gitalypb.UserRevertRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     revertedCommit,
		BranchName: []byte(git.DefaultBranch),
		Message:    []byte("Reverting commit"),
		Timestamp:  &timestamppb.Timestamp{Seconds: 12345},
	})
	require.NoError(t, err)

	require.Equal(t, &gitalypb.OperationBranchUpdate{
		CommitId: gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "d01916c25f522190331cff8f75960881120d22a2",
			"sha256": "28b57208e72bc2317143571997b9cfc444a51b52a43dde1c0282633a2b60de71",
		}),
	}, response.BranchUpdate)
	require.Empty(t, response.CreateTreeError)
	require.Empty(t, response.CreateTreeErrorCode)

	// headCommit is pointed commit after revert
	headCommit, err := repo.ReadCommit(ctx, git.Revision(git.DefaultBranch))
	require.NoError(t, err)

	require.Equal(t, &gitalypb.GitCommit{
		Id: gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "d01916c25f522190331cff8f75960881120d22a2",
			"sha256": "28b57208e72bc2317143571997b9cfc444a51b52a43dde1c0282633a2b60de71",
		}),
		ParentIds: gittest.ObjectHashDependent(t, map[string][]string{
			"sha1":   {"d7c4ed5ffd773b3316f04fdd5d9d6f13591429ae"},
			"sha256": {"eb2185ab72ce9d83c8f4f551914266d7de2d97affa208f884b594ae1b60f8130"},
		}),
		TreeId: gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "b4579012566f2e5e84babb5d2bdad3824ed92bfd",
			"sha256": "58ce56be6b8dda4a4505f9e7f2ed0b82adfbb4f3cae2edcae798710e54e1423b",
		}),
		Subject:  []byte("Reverting commit"),
		Body:     []byte("Reverting commit"),
		BodySize: 16,
		Author: &gitalypb.CommitAuthor{
			Name:     []byte("Jane Doe"),
			Email:    []byte("janedoe@gitlab.com"),
			Date:     &timestamppb.Timestamp{Seconds: 12345},
			Timezone: []byte(gittest.TimezoneOffset),
		},
		Committer: &gitalypb.CommitAuthor{
			Name:     []byte("Jane Doe"),
			Email:    []byte("janedoe@gitlab.com"),
			Date:     &timestamppb.Timestamp{Seconds: 12345},
			Timezone: []byte(gittest.TimezoneOffset),
		},
	}, headCommit)
}

func TestServer_UserRevert_successfulIntoEmptyRepo(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
		featureflag.ReturnStructuredErrorsInUserRevert,
	).Run(t, testServerUserRevertSuccessfulIntoEmptyRepo)
}

func testServerUserRevertSuccessfulIntoEmptyRepo(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	startRepoProto, startRepoPath := gittest.CreateRepository(t, ctx, cfg)
	startRepo := localrepo.NewTestRepo(t, cfg, startRepoProto)

	commit1 := gittest.WriteCommit(t, cfg, startRepoPath,
		gittest.WithMessage("add blob1"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob1", Mode: "100644", Content: "foobar1"},
		),
	)
	commit2 := gittest.WriteCommit(t, cfg, startRepoPath,
		gittest.WithParents(commit1),
		gittest.WithMessage("add blob2"),
		gittest.WithBranch(git.DefaultBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob1", Mode: "100644", Content: "foobar1"},
			gittest.TreeEntry{Path: "blob2", Mode: "100644", Content: "foobar2"},
		),
	)

	revertedCommit, err := startRepo.ReadCommit(ctx, commit2.Revision())
	require.NoError(t, err)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	destinationBranch := "revert-dst"

	request := &gitalypb.UserRevertRequest{
		Repository:      repoProto,
		User:            gittest.TestUser,
		Commit:          revertedCommit,
		BranchName:      []byte(destinationBranch),
		Message:         []byte("Reverting " + revertedCommit.Id),
		StartRepository: startRepoProto,
		StartBranchName: []byte(git.DefaultBranch),
	}

	response, err := client.UserRevert(ctx, request)
	require.NoError(t, err)

	headCommit, err := repo.ReadCommit(ctx, git.Revision(request.BranchName))
	require.NoError(t, err)

	expectedBranchUpdate := &gitalypb.OperationBranchUpdate{
		BranchCreated: true,
		RepoCreated:   true,
		CommitId:      headCommit.Id,
	}

	require.Equal(t, expectedBranchUpdate, response.BranchUpdate)
	require.Empty(t, response.CreateTreeError)
	require.Empty(t, response.CreateTreeErrorCode)

	require.Equal(t, request.Message, headCommit.Subject)
	require.Equal(t, revertedCommit.Id, headCommit.ParentIds[0])
	gittest.RequireTree(t, cfg, repoPath, response.BranchUpdate.CommitId,
		[]gittest.TreeEntry{
			{Path: "blob1", Mode: "100644", Content: "foobar1"},
		})
}

func TestServer_UserRevert_successfulGitHooks(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
		featureflag.ReturnStructuredErrorsInUserRevert,
	).Run(t, testServerUserRevertSuccessfulGitHooks)
}

func testServerUserRevertSuccessfulGitHooks(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	destinationBranch := "revert-dst"

	revertedCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("add blob1"),
		gittest.WithBranch(destinationBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob1", Mode: "100644", Content: "foobar1"},
		),
	)

	revertedCommit, err := repo.ReadCommit(ctx, revertedCommitID.Revision())
	require.NoError(t, err)

	request := &gitalypb.UserRevertRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     revertedCommit,
		BranchName: []byte(destinationBranch),
		Message:    []byte("Reverting " + revertedCommit.Id),
	}

	var hookOutputFiles []string
	for _, hookName := range GitlabHooks {
		hookOutputTempPath := gittest.WriteEnvToCustomHook(t, repoPath, hookName)
		hookOutputFiles = append(hookOutputFiles, hookOutputTempPath)
	}

	response, err := client.UserRevert(ctx, request)
	require.NoError(t, err)
	require.Empty(t, response.PreReceiveError)
	headCommit, err := repo.ReadCommit(ctx, git.Revision(destinationBranch))
	require.NoError(t, err)
	gittest.RequireTree(t, cfg, repoPath, headCommit.Id, nil)

	for _, file := range hookOutputFiles {
		output := string(testhelper.MustReadFile(t, file))
		require.Contains(t, output, "GL_USERNAME="+gittest.TestUser.GlUsername)
	}
}

func TestServer_UserRevert_failedDueToPreReceiveError(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
		featureflag.ReturnStructuredErrorsInUserRevert,
	).Run(t, testServerUserRevertFailedDueToPreReceiveError)
}

func testServerUserRevertFailedDueToPreReceiveError(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	destinationBranch := "revert-dst"

	revertedCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("add blob1"),
		gittest.WithBranch(destinationBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob1", Mode: "100644", Content: "foobar1"},
		),
	)

	revertedCommit, err := repo.ReadCommit(ctx, revertedCommitID.Revision())
	require.NoError(t, err)

	request := &gitalypb.UserRevertRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     revertedCommit,
		BranchName: []byte(destinationBranch),
		Message:    []byte("Reverting " + revertedCommit.Id),
	}

	hookContent := []byte("#!/bin/sh\necho GL_ID=$GL_ID\nexit 1")

	for _, hookName := range GitlabPreHooks {
		t.Run(hookName, func(t *testing.T) {
			gittest.WriteCustomHook(t, repoPath, hookName, hookContent)

			if featureflag.ReturnStructuredErrorsInUserRevert.IsDisabled(ctx) {
				response, err := client.UserRevert(ctx, request)
				require.NoError(t, err)
				require.Contains(t, response.PreReceiveError, "GL_ID="+gittest.TestUser.GlId)
			} else {
				_, err := client.UserRevert(ctx, request)
				actualStatus, _ := status.FromError(err)
				require.Equal(t, actualStatus.Code(), codes.PermissionDenied)
				require.Equal(t, actualStatus.Message(), "revert: custom hook error")
				revertError, ok := actualStatus.Details()[0].(*gitalypb.UserRevertError)
				require.True(t, ok)
				require.Contains(t, revertError.GetCustomHook().String(), "GL_ID="+gittest.TestUser.GlId)
			}
		})
	}
}

func TestServer_UserRevert_failedDueToCreateTreeErrorConflict(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
		featureflag.ReturnStructuredErrorsInUserRevert,
	).Run(t, testServerUserRevertFailedDueToCreateTreeErrorConflict)
}

func testServerUserRevertFailedDueToCreateTreeErrorConflict(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	destinationBranch := "revert-dst"

	baseCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("add blob"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "01\n02\n03"},
		),
	)
	_ = gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(baseCommitID),
		gittest.WithMessage("blob: update 02 to hello"),
		gittest.WithBranch(destinationBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "01\nhello\n03"},
		),
	)
	revertedCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(baseCommitID),
		gittest.WithMessage("blob: update 02 to world"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob", Mode: "100644", Content: "01\nworld\n03"},
		),
	)

	// This revert patch of the following commit cannot be applied to the destinationBranch above
	revertedCommit, err := repo.ReadCommit(ctx, revertedCommitID.Revision())
	require.NoError(t, err)

	request := &gitalypb.UserRevertRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     revertedCommit,
		BranchName: []byte(destinationBranch),
		Message:    []byte("Reverting " + revertedCommit.Id),
	}

	if featureflag.ReturnStructuredErrorsInUserRevert.IsDisabled(ctx) {
		response, err := client.UserRevert(ctx, request)
		require.NoError(t, err)
		require.NotEmpty(t, response.CreateTreeError)
		require.Equal(t, gitalypb.UserRevertResponse_CONFLICT, response.CreateTreeErrorCode)
	} else {
		_, err = client.UserRevert(ctx, request)
		actualStatus, _ := status.FromError(err)
		require.Equal(t, actualStatus.Code(), codes.FailedPrecondition)
		require.Equal(t, actualStatus.Message(), "revert: there are conflicting files")
		revertError, ok := actualStatus.Details()[0].(*gitalypb.UserRevertError)
		require.True(t, ok)
		testhelper.ProtoEqual(t, &gitalypb.MergeConflictError{
			ConflictingFiles: [][]byte{[]byte("blob")},
		}, revertError.GetMergeConflict())
	}
}

func TestServer_UserRevert_failedDueToCreateTreeErrorEmpty(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
		featureflag.ReturnStructuredErrorsInUserRevert,
	).Run(t, testServerUserRevertFailedDueToCreateTreeErrorEmpty)
}

func testServerUserRevertFailedDueToCreateTreeErrorEmpty(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	destinationBranch := "revert-dst"

	baseCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("add blob1 and blob2"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob1", Mode: "100644", Content: "foobar1"},
			gittest.TreeEntry{Path: "blob2", Mode: "100644", Content: "foobar2"},
		),
	)

	leftCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(baseCommitID),
		gittest.WithMessage("add blob3"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob1", Mode: "100644", Content: "foobar1"},
			gittest.TreeEntry{Path: "blob2", Mode: "100644", Content: "foobar2"},
			gittest.TreeEntry{Path: "blob3", Mode: "100644", Content: "foobar3"},
		),
	)
	// Add a commit that deletes blob2 and set to branch head
	_ = gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(leftCommitID),
		gittest.WithMessage("delete blob2"),
		gittest.WithBranch(destinationBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob1", Mode: "100644", Content: "foobar1"},
			gittest.TreeEntry{Path: "blob3", Mode: "100644", Content: "foobar3"},
		),
	)

	// rightCommitID also deletes blob2
	revertedCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithParents(baseCommitID),
		gittest.WithMessage("delete blob2"),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob1", Mode: "100644", Content: "foobar1"},
		),
	)
	revertedCommit, err := repo.ReadCommit(ctx, revertedCommitID.Revision())
	require.NoError(t, err)

	request := &gitalypb.UserRevertRequest{
		Repository: repoProto,
		User:       gittest.TestUser,
		Commit:     revertedCommit,
		BranchName: []byte(destinationBranch),
		Message:    []byte("Reverting " + revertedCommit.Id),
	}

	response, err := client.UserRevert(ctx, request)
	require.NoError(t, err)

	if featureflag.ReturnStructuredErrorsInUserRevert.IsDisabled(ctx) {
		require.Empty(t, response.CreateTreeError)
		require.Equal(t, gitalypb.UserRevertResponse_NONE, response.CreateTreeErrorCode)

		response, err = client.UserRevert(ctx, request)
		require.NoError(t, err)
		require.NotEmpty(t, response.CreateTreeError)
		require.Equal(t, gitalypb.UserRevertResponse_EMPTY, response.CreateTreeErrorCode)
	} else {
		_, err = client.UserRevert(ctx, request)
		expectedError := structerr.NewFailedPrecondition("revert: could not apply because the result was empty").WithDetail(
			&gitalypb.UserRevertError{
				Error: &gitalypb.UserRevertError_ChangesAlreadyApplied{},
			})
		testhelper.RequireGrpcError(t, expectedError, err)
	}
}

func TestServer_UserRevert_failedDueToCommitError(t *testing.T) {
	t.Parallel()

	testhelper.NewFeatureSets(
		featureflag.GPGSigning,
		featureflag.ReturnStructuredErrorsInUserRevert,
	).Run(t, testServerUserRevertFailedDueToCommitError)
}

func testServerUserRevertFailedDueToCommitError(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsService(t, ctx)
	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	sourceBranch := "revert-src"
	destinationBranch := "revert-dst"

	revertedCommitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("add blob1"),
		gittest.WithBranch(sourceBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob1", Mode: "100644", Content: "foobar1"},
		),
	)

	// new commit added to destinationBranch
	_ = gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithMessage("add blob2"),
		gittest.WithBranch(destinationBranch),
		gittest.WithTreeEntries(
			gittest.TreeEntry{Path: "blob1", Mode: "100644", Content: "foobar1"},
			gittest.TreeEntry{Path: "blob2", Mode: "100644", Content: "foobar2"},
		),
	)

	revertedCommit, err := repo.ReadCommit(ctx, revertedCommitID.Revision())
	require.NoError(t, err)

	request := &gitalypb.UserRevertRequest{
		Repository:      repoProto,
		User:            gittest.TestUser,
		Commit:          revertedCommit,
		BranchName:      []byte(destinationBranch),
		Message:         []byte("Reverting " + revertedCommit.Id),
		StartBranchName: []byte(sourceBranch),
	}

	response, err := client.UserRevert(ctx, request)

	if featureflag.ReturnStructuredErrorsInUserRevert.IsDisabled(ctx) {
		require.NoError(t, err)
		require.Equal(t, "Branch diverged", response.CommitError)
	} else {
		actualStatus, _ := status.FromError(err)
		require.Equal(t, actualStatus.Code(), codes.FailedPrecondition)
		require.Equal(t, actualStatus.Message(), "revert: branch diverged")
		revertError, ok := actualStatus.Details()[0].(*gitalypb.UserRevertError)
		require.True(t, ok)
		require.NotNil(t, revertError.GetNotAncestor())
	}
}
