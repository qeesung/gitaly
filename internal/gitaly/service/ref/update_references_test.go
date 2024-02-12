package ref

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/updateref"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb/testproto"
)

func TestUpdateReferences(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRefService(t)

	missingCommitID := bytes.Repeat([]byte("1"), gittest.DefaultObjectHash.EncodedLen())

	type setupData struct {
		requests                  []*gitalypb.UpdateReferencesRequest
		expectedErr               error
		expectedRefs              []git.Reference
		skipReferenceVerification bool
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "unset repository",
			setup: func(t *testing.T) setupData {
				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: nil,
						},
					},
					expectedErr: testhelper.ToInterceptedMetadata(
						structerr.NewInvalidArgument("repository not set"),
					),
					skipReferenceVerification: true,
				}
			},
		},
		{
			desc: "missing repository",
			setup: func(t *testing.T) setupData {
				relativePath := gittest.NewRepositoryName(t)

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: &gitalypb.Repository{
								StorageName:  cfg.Storages[0].Name,
								RelativePath: relativePath,
							},
						},
					},
					expectedErr: testhelper.ToInterceptedMetadata(
						structerr.NewNotFound("%w", storage.NewRepositoryNotFoundError(cfg.Storages[0].Name, relativePath)),
					),
					skipReferenceVerification: true,
				}
			},
		},
		{
			desc: "missing updates",
			setup: func(t *testing.T) setupData {
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
						},
					},
					expectedErr: structerr.NewInvalidArgument("no updates specified"),
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
					},
				}
			},
		},
		{
			desc: "invalid reference name",
			setup: func(t *testing.T) setupData {
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference: []byte("foobar"),
								},
							},
						},
					},
					expectedErr: structerr.NewInvalidArgument("validating reference: reference is not fully qualified").
						WithDetail(&testproto.ErrorMetadata{
							Key: []byte("reference"), Value: []byte("foobar"),
						}).
						WithDetail(&gitalypb.UpdateReferencesError{
							Error: &gitalypb.UpdateReferencesError_InvalidFormat{
								InvalidFormat: &gitalypb.InvalidRefFormatError{
									Refs: [][]byte{[]byte("foobar")},
								},
							},
						}),
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
					},
				}
			},
		},
		{
			desc: "HEAD cannot be updated",
			setup: func(t *testing.T) setupData {
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference: []byte("HEAD"),
								},
							},
						},
					},
					expectedErr: structerr.NewInvalidArgument("validating reference: HEAD reference not allowed").
						WithDetail(&testproto.ErrorMetadata{
							Key: []byte("reference"), Value: []byte("HEAD"),
						}).
						WithDetail(&gitalypb.UpdateReferencesError{
							Error: &gitalypb.UpdateReferencesError_InvalidFormat{
								InvalidFormat: &gitalypb.InvalidRefFormatError{
									Refs: [][]byte{[]byte("HEAD")},
								},
							},
						}),
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
					},
				}
			},
		},
		{
			desc: "invalid old object ID",
			setup: func(t *testing.T) setupData {
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/branch"),
									OldObjectId: []byte("invalid-hex"),
								},
							},
						},
					},
					expectedErr: testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument(
							"validating old object ID: invalid object ID: %q, expected length %d, got 11",
							"invalid-hex",
							gittest.DefaultObjectHash.EncodedLen(),
						),
						"old_object_id", "invalid-hex",
					),
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
					},
				}
			},
		},
		{
			desc: "invalid new object ID",
			setup: func(t *testing.T) setupData {
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/branch"),
									NewObjectId: []byte("invalid-hex"),
								},
							},
						},
					},
					expectedErr: testhelper.WithInterceptedMetadata(
						structerr.NewInvalidArgument(
							"validating new object ID: invalid object ID: %q, expected length %d, got 11",
							"invalid-hex",
							gittest.DefaultObjectHash.EncodedLen(),
						),
						"new_object_id", "invalid-hex",
					),
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
					},
				}
			},
		},
		{
			desc: "missing object",
			setup: func(t *testing.T) setupData {
				repoProto, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/branch"),
									NewObjectId: missingCommitID,
								},
							},
						},
					},
					expectedErr: testhelper.WithInterceptedMetadataItems(
						structerr.NewNotFound("target object missing"),
						structerr.MetadataItem{Key: "missing_object", Value: string(missingCommitID)},
						structerr.MetadataItem{Key: "reference", Value: "refs/heads/branch"},
					),
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
					},
				}
			},
		},
		{
			desc: "mismatching old object ID",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))
				newCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(oldCommitID))

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/branch"),
									OldObjectId: missingCommitID,
									NewObjectId: []byte(newCommitID),
								},
							},
						},
					},
					expectedErr: structerr.NewAborted("reference does not point to expected object").
						WithDetail(&testproto.ErrorMetadata{
							Key: []byte("actual_object_id"), Value: []byte(oldCommitID),
						}).
						WithDetail(&testproto.ErrorMetadata{
							Key: []byte("expected_object_id"), Value: missingCommitID,
						}).
						WithDetail(&testproto.ErrorMetadata{
							Key: []byte("reference"), Value: []byte("refs/heads/branch"),
						}).
						WithDetail(&gitalypb.UpdateReferencesError{
							Error: &gitalypb.UpdateReferencesError_ReferenceStateMismatch{
								ReferenceStateMismatch: &gitalypb.ReferenceStateMismatchError{
									ReferenceName:    []byte("refs/heads/branch"),
									ExpectedObjectId: missingCommitID,
									ActualObjectId:   []byte(oldCommitID),
								},
							},
						}),
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
						git.NewReference("refs/heads/branch", oldCommitID),
					},
				}
			},
		},
		{
			desc: "locked reference",
			setup: func(t *testing.T) setupData {
				testhelper.SkipWithReftable(t, "refLockedRegex doesn't match error thrown by reftable backend")

				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)
				repo := localrepo.NewTestRepo(t, cfg, repoProto)

				oldCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))

				// Prepare a reference update that locks the reference, but don't commit it so that the
				// reference stays locked.
				updater, err := updateref.New(ctx, repo)
				require.NoError(t, err)
				require.NoError(t, updater.Start())
				require.NoError(t, updater.Delete("refs/heads/branch"))
				require.NoError(t, updater.Prepare())

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/branch"),
									OldObjectId: []byte(oldCommitID),
									NewObjectId: []byte(gittest.DefaultObjectHash.ZeroOID),
								},
							},
						},
					},
					expectedErr: structerr.NewAborted("reference is already locked").
						WithDetail(&testproto.ErrorMetadata{
							Key: []byte("reference"), Value: []byte("refs/heads/branch"),
						}).
						WithDetail(&gitalypb.UpdateReferencesError{
							Error: &gitalypb.UpdateReferencesError_ReferencesLocked{
								ReferencesLocked: &gitalypb.ReferencesLockedError{
									Refs: [][]byte{[]byte("refs/heads/branch")},
								},
							},
						}),
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
						git.NewReference("refs/heads/branch", oldCommitID),
					},
				}
			},
		},
		{
			desc: "subset of references fails",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				oldCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("branch"))
				newCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(oldCommitID))

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/new"),
									OldObjectId: []byte(gittest.DefaultObjectHash.ZeroOID),
									NewObjectId: []byte(newCommitID),
								},
								{
									Reference:   []byte("refs/heads/branch"),
									OldObjectId: missingCommitID,
									NewObjectId: []byte(newCommitID),
								},
							},
						},
					},
					expectedErr: structerr.NewAborted("reference does not point to expected object").
						WithDetail(&testproto.ErrorMetadata{
							Key: []byte("actual_object_id"), Value: []byte(oldCommitID),
						}).
						WithDetail(&testproto.ErrorMetadata{
							Key: []byte("expected_object_id"), Value: missingCommitID,
						}).
						WithDetail(&testproto.ErrorMetadata{
							Key: []byte("reference"), Value: []byte("refs/heads/branch"),
						}).
						WithDetail(&gitalypb.UpdateReferencesError{
							Error: &gitalypb.UpdateReferencesError_ReferenceStateMismatch{
								ReferenceStateMismatch: &gitalypb.ReferenceStateMismatchError{
									ReferenceName:    []byte("refs/heads/branch"),
									ExpectedObjectId: missingCommitID,
									ActualObjectId:   []byte(oldCommitID),
								},
							},
						}),
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
						git.NewReference("refs/heads/branch", oldCommitID),
					},
				}
			},
		},
		{
			desc: "forced branch creation succeeds",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				newCommitID := gittest.WriteCommit(t, cfg, repoPath)

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/new"),
									NewObjectId: []byte(newCommitID),
								},
							},
						},
					},
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
						git.NewReference("refs/heads/new", newCommitID),
					},
				}
			},
		},
		{
			desc: "raceless branch creation succeeds",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				newCommitID := gittest.WriteCommit(t, cfg, repoPath)

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/new"),
									OldObjectId: []byte(gittest.DefaultObjectHash.ZeroOID),
									NewObjectId: []byte(newCommitID),
								},
							},
						},
					},
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
						git.NewReference("refs/heads/new", newCommitID),
					},
				}
			},
		},
		{
			desc: "raceless branch update succeeds",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				existingCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("update"))
				newCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(existingCommitID))

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/update"),
									OldObjectId: []byte(existingCommitID),
									NewObjectId: []byte(newCommitID),
								},
							},
						},
					},
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
						git.NewReference("refs/heads/update", newCommitID),
					},
				}
			},
		},
		{
			desc: "forced branch update succeeds",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				existingCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("update"))
				newCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(existingCommitID))

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/update"),
									NewObjectId: []byte(newCommitID),
								},
							},
						},
					},
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
						git.NewReference("refs/heads/update", newCommitID),
					},
				}
			},
		},
		{
			desc: "forced branch deletion succeeds",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("delete-me"))

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/delete-me"),
									NewObjectId: []byte(gittest.DefaultObjectHash.ZeroOID),
								},
							},
						},
					},
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
					},
				}
			},
		},
		{
			desc: "raceless branch deletion succeeds",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				existingCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("update"))

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/update"),
									OldObjectId: []byte(existingCommitID),
									NewObjectId: []byte(gittest.DefaultObjectHash.ZeroOID),
								},
							},
						},
					},
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
					},
				}
			},
		},
		{
			desc: "multiple updates in single request",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				updateCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("update-me"), gittest.WithBranch("update-me"))
				deleteCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("delete-me"), gittest.WithBranch("delete-me"))
				createCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("create-me"))

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/update-me"),
									OldObjectId: []byte(updateCommitID),
									NewObjectId: []byte(deleteCommitID),
								},
								{
									Reference:   []byte("refs/heads/delete-me"),
									OldObjectId: []byte(deleteCommitID),
									NewObjectId: []byte(gittest.DefaultObjectHash.ZeroOID),
								},
								{
									Reference:   []byte("refs/heads/create-me"),
									OldObjectId: []byte(gittest.DefaultObjectHash.ZeroOID),
									NewObjectId: []byte(createCommitID),
								},
							},
						},
					},
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
						git.NewReference("refs/heads/create-me", createCommitID),
						git.NewReference("refs/heads/update-me", deleteCommitID),
					},
				}
			},
		},
		{
			desc: "multiple updates in multiple requests",
			setup: func(t *testing.T) setupData {
				repoProto, repoPath := gittest.CreateRepository(t, ctx, cfg)

				updateCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("update-me"), gittest.WithBranch("update-me"))
				deleteCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("delete-me"), gittest.WithBranch("delete-me"))
				createCommitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithMessage("create-me"))

				return setupData{
					requests: []*gitalypb.UpdateReferencesRequest{
						{
							Repository: repoProto,
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/update-me"),
									OldObjectId: []byte(updateCommitID),
									NewObjectId: []byte(deleteCommitID),
								},
							},
						},
						{
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/delete-me"),
									OldObjectId: []byte(deleteCommitID),
									NewObjectId: []byte(gittest.DefaultObjectHash.ZeroOID),
								},
							},
						},
						{
							Updates: []*gitalypb.UpdateReferencesRequest_Update{
								{
									Reference:   []byte("refs/heads/create-me"),
									OldObjectId: []byte(gittest.DefaultObjectHash.ZeroOID),
									NewObjectId: []byte(createCommitID),
								},
							},
						},
					},
					expectedRefs: []git.Reference{
						git.NewSymbolicReference("HEAD", "refs/heads/main"),
						git.NewReference("refs/heads/create-me", createCommitID),
						git.NewReference("refs/heads/update-me", deleteCommitID),
					},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			client, err := client.UpdateReferences(ctx)
			require.NoError(t, err)

			for _, request := range setup.requests {
				require.NoError(t, client.Send(request))
			}

			_, err = client.CloseAndRecv()
			testhelper.RequireGrpcError(t, setup.expectedErr, err)

			if !setup.skipReferenceVerification {
				repo := localrepo.NewTestRepo(t, cfg, setup.requests[0].GetRepository())
				refs, err := repo.GetReferences(ctx)
				require.NoError(t, err)
				defaultBranch, err := repo.HeadReference(ctx)
				require.NoError(t, err)
				require.Equal(t, setup.expectedRefs, append([]git.Reference{
					git.NewSymbolicReference("HEAD", defaultBranch),
				}, refs...))
			}
		})
	}
}
