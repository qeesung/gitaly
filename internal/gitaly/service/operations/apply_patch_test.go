package operations

import (
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/transaction"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/backchannel"
	"gitlab.com/gitlab-org/gitaly/v16/internal/grpc/metadata"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestUserApplyPatch(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, client := setupOperationsService(t, ctx)

	errPatchingFailed := status.Error(
		codes.FailedPrecondition,
		"Patch failed at 0002 commit subject")

	// patchDescription is the description of a patch that gets derived from diffing the old
	// tree with the new tree.
	type patchDescription struct {
		// oldTree is the old tree to compare against. If unset, we will instead us the base
		// tree.
		oldTree []gittest.TreeEntry
		// newTree is the tree with the changes applied.
		newTree []gittest.TreeEntry
	}

	type expected struct {
		oldOID         string
		err            error
		branchCreation bool
		tree           []gittest.TreeEntry
	}

	for _, tc := range []struct {
		desc string
		// sends a request to a non-existent repository
		nonExistentRepository bool
		// baseTree contanis the tree entry that form the tree of the base commit.
		baseTree []gittest.TreeEntry
		// baseReference is the branch where baseCommit is, by default git.DefaultBranch
		baseReference git.ReferenceName
		// notSentByAuthor marks the patch as being sent by someone else than the author.
		notSentByAuthor bool
		// targetBranch is the branch where the patched commit goes.
		targetBranch string
		// extraBranches are created with empty commits for verifying the correct base branch
		// gets selected.
		extraBranches []string
		// patches describe how to build each commit that gets applied as a patch.
		// Each patch is series of actions that are applied on top of the baseCommit.
		// Each action produces one commit. The patch is then generated from the last commit
		// in the series to its parent.
		//
		// After the patches are generated, they are applied sequentially on the base commit.
		patches  []patchDescription
		expected func(t *testing.T, repoPath string) expected
	}{
		{
			desc:                  "non-existent repository",
			targetBranch:          git.DefaultBranch,
			nonExistentRepository: true,
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					err: testhelper.ToInterceptedMetadata(
						structerr.New("%w", storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, "doesnt-exist")),
					),
				}
			},
		},
		{
			desc: "creating the first branch does not work",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			targetBranch: git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "base-content"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					err: status.Error(codes.Internal, "no default branch"),
				}
			},
		},
		{
			desc: "creating a new branch from HEAD works",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			baseReference: "HEAD",
			extraBranches: []string{git.DefaultRef.String(), "refs/heads/some-extra-branch"},
			targetBranch:  "new-branch",
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 1"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					branchCreation: true,
					tree: []gittest.TreeEntry{
						{Mode: "100644", Path: "file", Content: "patch 1"},
					},
				}
			},
		},
		{
			desc: "multiple patches apply cleanly",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			baseReference: git.DefaultRef,
			targetBranch:  git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 1"},
					},
				},
				{
					oldTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 1"},
					},
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 2"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					tree: []gittest.TreeEntry{
						{Mode: "100644", Path: "file", Content: "patch 2"},
					},
				}
			},
		},
		{
			desc: "author in from field in body set correctly",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			baseReference:   git.DefaultRef,
			notSentByAuthor: true,
			targetBranch:    git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 1"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					tree: []gittest.TreeEntry{
						{Mode: "100644", Path: "file", Content: "patch 1"},
					},
				}
			},
		},
		{
			desc: "multiple patches apply via fallback three-way merge",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			baseReference: git.DefaultRef,
			targetBranch:  git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 1"},
					},
				},
				{
					oldTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 2"},
					},
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 1"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					tree: []gittest.TreeEntry{
						{Mode: "100644", Path: "file", Content: "patch 1"},
					},
				}
			},
		},
		{
			desc: "patching fails due to modify-modify conflict",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			baseReference: git.DefaultRef,
			targetBranch:  git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 1"},
					},
				},
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 2"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					err: errPatchingFailed,
				}
			},
		},
		{
			desc: "patching fails due to add-add conflict",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			baseReference: git.DefaultRef,
			targetBranch:  git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "added-file", Mode: "100644", Content: "content-1"},
					},
				},
				{
					newTree: []gittest.TreeEntry{
						{Path: "added-file", Mode: "100644", Content: "content-2"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					err: errPatchingFailed,
				}
			},
		},
		{
			desc: "patch applies using rename detection",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "line 1\nline 2\nline 3\nline 4\n"},
			},
			baseReference: git.DefaultRef,
			targetBranch:  git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "moved-file", Mode: "100644", Content: "line 1\nline 2\nline 3\nline 4\n"},
					},
				},
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "line 1\nline 2\nline 3\nline 4\nadded\n"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					tree: []gittest.TreeEntry{
						{Mode: "100644", Path: "moved-file", Content: "line 1\nline 2\nline 3\nline 4\nadded\n"},
					},
				}
			},
		},
		{
			desc: "patching fails due to delete-modify conflict",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			baseReference: git.DefaultRef,
			targetBranch:  git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{},
				},
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "updated content"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					err: errPatchingFailed,
				}
			},
		},
		{
			desc: "existing branch + correct expectedOldOID",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			baseReference: git.DefaultRef,
			targetBranch:  git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 1"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				oid := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", git.DefaultBranch))
				return expected{
					tree: []gittest.TreeEntry{
						{Mode: "100644", Path: "file", Content: "patch 1"},
					},
					oldOID: oid,
				}
			},
		},
		{
			desc: "existing branch + invalid expectedOldOID",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			baseReference: git.DefaultRef,
			targetBranch:  git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 1"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					oldOID: "foo",
					err:    structerr.NewInternal(fmt.Sprintf(`expected old object id not expected SHA format: invalid object ID: "foo", expected length %v, got 3`, gittest.DefaultObjectHash.EncodedLen())),
				}
			},
		},
		{
			desc: "existing branch + valid but unavailable expectedOldOID",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			baseReference: git.DefaultRef,
			targetBranch:  git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 1"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				return expected{
					oldOID: gittest.DefaultObjectHash.ZeroOID.String(),
					err:    structerr.NewInternal("expected old object cannot be resolved: reference not found"),
				}
			},
		},
		{
			desc: "existing branch + expectedOldOID set to an old commit OID",
			baseTree: []gittest.TreeEntry{
				{Path: "file", Mode: "100644", Content: "base-content"},
			},
			baseReference: git.DefaultRef,
			targetBranch:  git.DefaultBranch,
			patches: []patchDescription{
				{
					newTree: []gittest.TreeEntry{
						{Path: "file", Mode: "100644", Content: "patch 1"},
					},
				},
			},
			expected: func(t *testing.T, repoPath string) expected {
				currentCommit := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", git.DefaultBranch))
				// add a new commit to the default branch so we can point at
				// the old one, this is because by default the test only
				// creates one commit
				futureCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(git.ObjectID(currentCommit)), gittest.WithBranch(git.DefaultBranch))
				return expected{
					oldOID: currentCommit,
					err: testhelper.WithInterceptedMetadataItems(
						structerr.NewInternal("update reference: reference update: reference does not point to expected object"),
						structerr.MetadataItem{Key: "actual_object_id", Value: futureCommit},
						structerr.MetadataItem{Key: "expected_object_id", Value: currentCommit},
						structerr.MetadataItem{Key: "reference", Value: "refs/heads/main"},
					),
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			authorTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			committerTime := authorTime.Add(time.Hour)
			commitMessage := "commit subject\n\ncommit message body\n\n\n"

			var baseCommit git.ObjectID
			if tc.baseTree != nil {
				baseCommit = gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithTreeEntries(tc.baseTree...),
					gittest.WithReference(string(tc.baseReference)),
				)
			}

			if tc.extraBranches != nil {
				emptyCommit := gittest.WriteCommit(t, cfg, repoPath)
				for _, extraBranch := range tc.extraBranches {
					gittest.WriteRef(t, cfg, repoPath, git.NewReferenceNameFromBranchName(extraBranch), emptyCommit)
				}
			}

			var patches [][]byte
			for _, patch := range tc.patches {
				oldCommit := baseCommit

				if patch.oldTree != nil {
					oldCommit = gittest.WriteCommit(t, cfg, repoPath,
						gittest.WithTreeEntries(patch.oldTree...),
					)
				}

				newCommit := gittest.WriteCommit(t, cfg, repoPath,
					gittest.WithMessage(commitMessage),
					gittest.WithTreeEntries(patch.newTree...),
					gittest.WithParents(oldCommit),
				)

				formatPatchArgs := []string{"-C", repoPath, "format-patch", "--stdout"}
				if tc.notSentByAuthor {
					formatPatchArgs = append(formatPatchArgs, "--from=Test Sender <sender@example.com>")
				}

				if baseCommit == "" {
					formatPatchArgs = append(formatPatchArgs, "--root", newCommit.String())
				} else {
					formatPatchArgs = append(formatPatchArgs, oldCommit.String()+".."+newCommit.String())
				}

				patches = append(patches, gittest.Exec(t, cfg, formatPatchArgs...))
			}

			stream, err := client.UserApplyPatch(ctx)
			require.NoError(t, err)

			requestTime := committerTime.Add(time.Hour)
			requestTimestamp := timestamppb.New(requestTime)

			if tc.nonExistentRepository {
				repoProto.RelativePath = "doesnt-exist"
			}

			expectedValues := tc.expected(t, repoPath)

			require.NoError(t, stream.Send(&gitalypb.UserApplyPatchRequest{
				UserApplyPatchRequestPayload: &gitalypb.UserApplyPatchRequest_Header_{
					Header: &gitalypb.UserApplyPatchRequest_Header{
						Repository:     repoProto,
						User:           gittest.TestUser,
						TargetBranch:   []byte(tc.targetBranch),
						Timestamp:      requestTimestamp,
						ExpectedOldOid: expectedValues.oldOID,
					},
				},
			}))

		outerLoop:
			for _, patch := range patches {
				// we stream the patches one rune at a time to exercise the streaming code
				for _, r := range patch {
					err := stream.Send(&gitalypb.UserApplyPatchRequest{
						UserApplyPatchRequestPayload: &gitalypb.UserApplyPatchRequest_Patches{
							Patches: []byte{r},
						},
					})

					// In case the request we're sending to the server results
					// in an error it can happen that the server already noticed
					// the request and thus returned an error while we're still
					// streaming the actual patch data. If so, the server closes
					// the stream and we are left unable to continue sending,
					// which means we get an EOF here.
					//
					// Abort the loop here so that we can observe the actual
					// error in `CloseAndRecv()`.
					if errors.Is(err, io.EOF) {
						break outerLoop
					}

					require.NoError(t, err)
				}
			}

			actualResponse, err := stream.CloseAndRecv()
			if expectedValues.err != nil {
				testhelper.RequireGrpcErrorContains(t, expectedValues.err, err)
				return
			}

			require.NoError(t, err)

			commitID := actualResponse.GetBranchUpdate().GetCommitId()
			actualResponse.GetBranchUpdate().CommitId = ""
			testhelper.ProtoEqual(t, &gitalypb.UserApplyPatchResponse{
				BranchUpdate: &gitalypb.OperationBranchUpdate{
					RepoCreated:   false,
					BranchCreated: expectedValues.branchCreation,
				},
			}, actualResponse)

			targetBranchCommit, err := repo.ResolveRevision(ctx,
				git.NewReferenceNameFromBranchName(tc.targetBranch).Revision()+"^{commit}")
			require.NoError(t, err)
			require.Equal(t, targetBranchCommit.String(), commitID)

			actualCommit, err := repo.ReadCommit(ctx, git.Revision(commitID))
			require.NoError(t, err)
			require.NotEmpty(t, actualCommit.ParentIds)
			actualCommit.ParentIds = nil // the parent changes with the patches, we just check it is set
			actualCommit.TreeId = ""     // treeID is asserted via its contents below

			expectedBody := []byte("commit subject\n\ncommit message body\n")
			testhelper.ProtoEqual(t,
				&gitalypb.GitCommit{
					Id:      commitID,
					Subject: []byte("commit subject"),
					Body:    expectedBody,
					Author:  gittest.DefaultCommitAuthor,
					Committer: &gitalypb.CommitAuthor{
						Name:     gittest.TestUser.Name,
						Email:    gittest.TestUser.Email,
						Date:     requestTimestamp,
						Timezone: []byte("+0800"),
					},
					BodySize: int64(len(expectedBody)),
				},
				actualCommit,
			)

			gittest.RequireTree(t, cfg, repoPath, commitID, expectedValues.tree)
		})
	}
}

func TestUserApplyPatch_stableID(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)

	ctx, cfg, client := setupOperationsService(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	parentCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "file", Mode: "100644", Content: "change me\n"},
	))
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	stream, err := client.UserApplyPatch(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&gitalypb.UserApplyPatchRequest{
		UserApplyPatchRequestPayload: &gitalypb.UserApplyPatchRequest_Header_{
			Header: &gitalypb.UserApplyPatchRequest_Header{
				Repository:   repoProto,
				User:         gittest.TestUser,
				TargetBranch: []byte("branch"),
				Timestamp:    &timestamppb.Timestamp{Seconds: 1234512345},
			},
		},
	}))

	require.NoError(t, stream.Send(&gitalypb.UserApplyPatchRequest{
		UserApplyPatchRequestPayload: &gitalypb.UserApplyPatchRequest_Patches{
			Patches: []byte(fmt.Sprintf(`From %s Mon Sep 17 00:00:00 2001
From: Patch User <patchuser@gitlab.org>
Date: Thu, 18 Oct 2018 13:40:35 +0200
Subject: [PATCH] A commit from a patch

---
 file | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)

diff --git a/file b/file
index 3742e48..e40a3b9 100644
--- a/file
+++ b/file
@@ -1 +1 @@
-change me
+changed
--
2.19.1
`, parentCommitID)),
		},
	}))

	response, err := stream.CloseAndRecv()
	require.NoError(t, err)
	require.True(t, response.BranchUpdate.BranchCreated)

	patchedCommit, err := repo.ReadCommit(ctx, git.Revision("branch"))
	require.NoError(t, err)
	require.Equal(t, &gitalypb.GitCommit{
		Id: gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "0a40a105159a00a5f7804bd4484dc73986d3d9bf",
			"sha256": "d1312a73a59c1b7ad9f91885a940b6663ada4a9d33b5904d056f30ac36c74b71",
		}),
		TreeId: gittest.ObjectHashDependent(t, map[string]string{
			"sha1":   "9aa427f7ab21b39efaa3efd02ead282a0584268c",
			"sha256": "8619b637949c69641affc727b7a7772fb68125f12f2d4224b55700551ba0f68a",
		}),
		ParentIds: []string{
			parentCommitID.String(),
		},
		Subject:  []byte("A commit from a patch"),
		Body:     []byte("A commit from a patch\n"),
		BodySize: 22,
		Author: &gitalypb.CommitAuthor{
			Name:     []byte("Patch User"),
			Email:    []byte("patchuser@gitlab.org"),
			Date:     &timestamppb.Timestamp{Seconds: 1539862835},
			Timezone: []byte("+0200"),
		},
		Committer: &gitalypb.CommitAuthor{
			Name:     gittest.TestUser.Name,
			Email:    gittest.TestUser.Email,
			Date:     &timestamppb.Timestamp{Seconds: 1234512345},
			Timezone: []byte("+0800"),
		},
	}, patchedCommit)
}

func TestUserApplyPatch_transactional(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	txManager := transaction.NewTrackingManager()
	ctx, cfg, client := setupOperationsService(t, ctx, testserver.WithTransactionManager(txManager))

	repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
	parentCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch(git.DefaultBranch), gittest.WithTreeEntries(
		gittest.TreeEntry{Path: "file", Mode: "100644", Content: "change me\n"},
	))

	// Reset the transaction manager as the setup call above creates a repository which
	// ends up creating some votes with Praefect enabled.
	txManager.Reset()

	ctx, err := txinfo.InjectTransaction(ctx, 1, "node", true)
	require.NoError(t, err)
	ctx = peer.NewContext(ctx, &peer.Peer{
		AuthInfo: backchannel.WithID(nil, 1234),
	})
	ctx = metadata.IncomingToOutgoing(ctx)

	stream, err := client.UserApplyPatch(ctx)
	require.NoError(t, err)
	require.NoError(t, stream.Send(&gitalypb.UserApplyPatchRequest{
		UserApplyPatchRequestPayload: &gitalypb.UserApplyPatchRequest_Header_{
			Header: &gitalypb.UserApplyPatchRequest_Header{
				Repository:   repoProto,
				User:         gittest.TestUser,
				TargetBranch: []byte("branch"),
				Timestamp:    &timestamppb.Timestamp{Seconds: 1234512345},
			},
		},
	}))
	require.NoError(t, stream.Send(&gitalypb.UserApplyPatchRequest{
		UserApplyPatchRequestPayload: &gitalypb.UserApplyPatchRequest_Patches{
			Patches: []byte(fmt.Sprintf(`From %s Mon Sep 17 00:00:00 2001
From: Patch User <patchuser@gitlab.org>
Date: Thu, 18 Oct 2018 13:40:35 +0200
Subject: [PATCH] A commit from a patch

---
 file | 2 +-
 1 file changed, 1 insertion(+), 1 deletion(-)

diff --git a/file b/file
index 3742e48..e40a3b9 100644
--- a/file
+++ b/file
@@ -1 +1 @@
-change me
+changed
--
2.19.1
`, parentCommitID)),
		},
	}))

	response, err := stream.CloseAndRecv()
	require.NoError(t, err)
	require.True(t, response.BranchUpdate.BranchCreated)
	require.Equal(t, 15, len(txManager.Votes()))
}

func TestUserApplyPatch_validation(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	ctx, cfg, client := setupOperationsService(t, ctx)

	repo, _ := gittest.CreateRepository(t, ctx, cfg)

	for _, tc := range []struct {
		desc        string
		repo        *gitalypb.Repository
		user        *gitalypb.User
		branchName  string
		expectedErr error
	}{
		{
			desc:        "missing Repository",
			branchName:  "new-branch",
			user:        gittest.TestUser,
			expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
		},
		{
			desc:        "missing Branch",
			repo:        repo,
			user:        gittest.TestUser,
			expectedErr: status.Error(codes.InvalidArgument, "missing Branch"),
		},
		{
			desc:        "empty BranchName",
			repo:        repo,
			user:        gittest.TestUser,
			branchName:  "",
			expectedErr: status.Error(codes.InvalidArgument, "missing Branch"),
		},
		{
			desc:        "missing User",
			branchName:  "new-branch",
			repo:        repo,
			expectedErr: status.Error(codes.InvalidArgument, "missing User"),
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			stream, err := client.UserApplyPatch(ctx)
			require.NoError(t, err)

			err = stream.Send(&gitalypb.UserApplyPatchRequest{
				UserApplyPatchRequestPayload: &gitalypb.UserApplyPatchRequest_Header_{
					Header: &gitalypb.UserApplyPatchRequest_Header{
						Repository:   tc.repo,
						User:         tc.user,
						TargetBranch: []byte(tc.branchName),
					},
				},
			})
			require.NoError(t, err)

			_, err = stream.CloseAndRecv()
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
		})
	}
}
