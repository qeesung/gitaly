//go:build !gitaly_test_sha256

package operations

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v15/internal/backchannel"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v15/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata"
	"gitlab.com/gitlab-org/gitaly/v15/internal/metadata/featureflag"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testcfg"
	"gitlab.com/gitlab-org/gitaly/v15/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v15/internal/transaction/txinfo"
	"gitlab.com/gitlab-org/gitaly/v15/proto/go/gitalypb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestUserDeleteTag_successful(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, repo, repoPath, client := setupOperationsService(t, ctx)

	tagNameInput := "to-be-deleted-soon-tag"

	gittest.Exec(t, cfg, "-C", repoPath, "tag", tagNameInput)

	request := &gitalypb.UserDeleteTagRequest{
		Repository: repo,
		TagName:    []byte(tagNameInput),
		User:       gittest.TestUser,
	}

	response, err := client.UserDeleteTag(ctx, request)
	require.NoError(t, err)
	require.Empty(t, response.PreReceiveError)

	tags := gittest.Exec(t, cfg, "-C", repoPath, "tag")
	require.NotContains(t, string(tags), tagNameInput, "tag name still exists in tags list")
}

func TestUserDeleteTag_hooks(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, repo, repoPath, client := setupOperationsService(t, ctx)

	tagNameInput := "to-be-déleted-soon-tag"

	request := &gitalypb.UserDeleteTagRequest{
		Repository: repo,
		TagName:    []byte(tagNameInput),
		User:       gittest.TestUser,
	}

	for _, hookName := range GitlabHooks {
		t.Run(hookName, func(t *testing.T) {
			gittest.Exec(t, cfg, "-C", repoPath, "tag", tagNameInput)

			hookOutputTempPath := gittest.WriteEnvToCustomHook(t, repoPath, hookName)

			_, err := client.UserDeleteTag(ctx, request)
			require.NoError(t, err)

			output := testhelper.MustReadFile(t, hookOutputTempPath)
			require.Contains(t, string(output), "GL_USERNAME="+gittest.TestUser.GlUsername)
		})
	}
}

func writeAssertObjectTypePreReceiveHook(t *testing.T, repoPath, expectedObjectType string) {
	t.Helper()

	gittest.WriteCustomHook(t, repoPath, "pre-receive", []byte(fmt.Sprintf(`#!/bin/bash
		i=0
		while read oldvalue newvalue reference
		do
			i=$((i+1))

			if [[ "${reference}" =~ skip-type-check- ]]
			then
				continue
			fi

			type="$(git cat-file -t "${newvalue}")"
			if test "%[1]s" != "${type}"
			then
				echo "expected %[1]s, got ${type}" >&2
				exit 1
			fi
		done

		if test "$i" -ne 1
		then
			echo "expected exactly one reference update, got ${i}" >&2
			exit 1
		fi
	`, expectedObjectType)))
}

func writeAssertObjectTypeUpdateHook(t *testing.T, repoPath, expectedObjectType string) {
	t.Helper()

	gittest.WriteCustomHook(t, repoPath, "update", []byte(fmt.Sprintf(`#!/bin/bash
		if [[ "$1" =~ skip-type-check- ]]
		then
			exit 0
		fi

		type="$(git cat-file -t "$3")"
		if test "%[1]s" != "${type}"
		then
			echo "expected %[1]s, got ${type}" >&2
			exit 1
		fi
	`, expectedObjectType)))
}

func TestUserCreateTag_successful(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagSuccessful)
}

func testUserCreateTagSuccessful(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents())

	targetRevisionCommit, err := repo.ReadCommit(ctx, commitID.Revision())
	require.NoError(t, err)

	inputTagName := "to-be-créated-soon"

	for _, tc := range []struct {
		desc               string
		tagName            string
		message            string
		targetRevision     git.Revision
		expectedObjectType string
		expectedResponse   *gitalypb.UserCreateTagResponse
	}{
		{
			desc:           "lightweight tag to commit",
			tagName:        inputTagName,
			targetRevision: commitID.Revision(),
			expectedResponse: &gitalypb.UserCreateTagResponse{
				Tag: &gitalypb.Tag{
					Name:         []byte(inputTagName),
					Id:           commitID.String(),
					TargetCommit: targetRevisionCommit,
				},
			},
			expectedObjectType: "commit",
		},
		{
			desc:           "annotated tag to commit",
			tagName:        inputTagName,
			targetRevision: commitID.Revision(),
			message:        "This is an annotated tag",
			expectedResponse: &gitalypb.UserCreateTagResponse{
				Tag: &gitalypb.Tag{
					Name:         []byte(inputTagName),
					Id:           "6c6134431f05e3d22726a3876cc1fecea7df18b5",
					TargetCommit: targetRevisionCommit,
					Message:      []byte("This is an annotated tag"),
					MessageSize:  24,
				},
			},
			expectedObjectType: "tag",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			writeAssertObjectTypePreReceiveHook(t, repoPath, tc.expectedObjectType)
			writeAssertObjectTypeUpdateHook(t, repoPath, tc.expectedObjectType)

			response, err := client.UserCreateTag(ctx, &gitalypb.UserCreateTagRequest{
				Repository:     repoProto,
				TagName:        []byte(tc.tagName),
				TargetRevision: []byte(tc.targetRevision),
				User:           gittest.TestUser,
				Message:        []byte(tc.message),
				Timestamp:      timestamppb.New(time.Unix(1600000000, 0)),
			})
			require.NoError(t, err)
			testhelper.ProtoEqual(t, tc.expectedResponse, response)

			defer gittest.Exec(t, cfg, "-C", repoPath, "tag", "-d", inputTagName)

			tag := gittest.Exec(t, cfg, "-C", repoPath, "tag")
			require.Contains(t, string(tag), inputTagName)
		})
	}
}

func TestUserCreateTag_transactional(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagTransactional)
}

func testUserCreateTagTransactional(t *testing.T, ctx context.Context) {
	t.Parallel()

	cfg := testcfg.Build(t)
	cfg.SocketPath = runOperationServiceServer(t, cfg, testserver.WithDisablePraefect())

	transactionServer := &testTransactionServer{}

	// We're using internal gitaly socket to connect to the server.
	// This is kind of a hack when running tests with Praefect:
	// if we directly connect to the server created above, then our call
	// would be intercepted by Praefect, which would in turn replace the
	// transaction information we inject further down below. So we instead
	// use internal socket so we can circumvent Praefect and just talk
	// to Gitaly directly.
	client := newMuxedOperationClient(t, ctx, "unix://"+cfg.InternalSocketPath(), cfg.Auth.Token,
		backchannel.NewClientHandshaker(
			testhelper.NewDiscardingLogEntry(t),
			func() backchannel.Server {
				srv := grpc.NewServer()
				gitalypb.RegisterRefTransactionServer(srv, transactionServer)
				return srv
			},
		),
	)

	for _, tc := range []struct {
		desc    string
		primary bool
		message string
	}{
		{
			desc:    "primary creates a lightweight tag",
			primary: true,
		},
		{
			desc:    "secondary creates a lightweight tag",
			primary: false,
		},
		{
			desc:    "primary creates an annotated tag",
			primary: true,
			message: "foobar",
		},
		{
			desc:    "secondary creates an annotated tag",
			primary: false,
			message: "foobar",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			*transactionServer = testTransactionServer{}

			repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			hooksOutputDir := testhelper.TempDir(t)
			hooksOutputPath := filepath.Join(hooksOutputDir, "output")

			// We're creating a set of custom hooks which simply
			// write to a file. The intention is that we want to
			// check that the hooks only run on the primary node.
			hooks := []string{"pre-receive", "update", "post-receive"}
			for _, hook := range hooks {
				gittest.WriteCustomHook(t, repoPath, hook,
					[]byte(fmt.Sprintf("#!/bin/sh\necho %s >>%s\n", hook, hooksOutputPath)),
				)
			}

			commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents())
			targetCommit, err := repo.ReadCommit(ctx, commitID.Revision())
			require.NoError(t, err)

			// We need to convert to an incoming context first in
			// order to preserve the feature flag.
			ctx := metadata.OutgoingToIncoming(ctx)
			ctx, err = txinfo.InjectTransaction(ctx, 1, "node", tc.primary)
			require.NoError(t, err)
			ctx = metadata.IncomingToOutgoing(ctx)

			response, err := client.UserCreateTag(ctx, &gitalypb.UserCreateTagRequest{
				Repository:     repoProto,
				TagName:        []byte("v1.0.0"),
				Message:        []byte(tc.message),
				TargetRevision: []byte(commitID),
				User:           gittest.TestUser,
			})
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.UserCreateTagResponse{
				Tag: &gitalypb.Tag{
					Name:         []byte("v1.0.0"),
					Message:      []byte(tc.message),
					MessageSize:  int64(len(tc.message)),
					Id:           text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "refs/tags/v1.0.0")),
					TargetCommit: targetCommit,
				},
			}, response)

			peeledTagID := text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "refs/tags/v1.0.0^{commit}"))
			require.Equal(t, commitID.String(), peeledTagID)

			// Only the primary node should've executed hooks.
			if tc.primary {
				contents := testhelper.MustReadFile(t, hooksOutputPath)
				require.Equal(t, "pre-receive\nupdate\npost-receive\n", string(contents))
			} else {
				require.NoFileExists(t, hooksOutputPath)
			}

			require.Equal(t, 2, transactionServer.called)
			transactionServer.called = 0
		})
	}
}

func TestUserCreateTag_quarantine(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagQuarantine)
}

func testUserCreateTagQuarantine(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents())

	tagIDOutputPath := filepath.Join(testhelper.TempDir(t), "tag-id")

	// We set up a custom "pre-receive" hook which simply prints the new tag to stdout and then
	// exits with an error. Like this, we can both assert that the hook can see the quarantined
	// tag, and it allows us to fail the RPC before we migrate quarantined objects. Furthermore,
	// we also try whether we can print the tag's tagged object to assert that we can see
	// objects which are not part of the object quarantine.
	gittest.WriteCustomHook(t, repoPath, "pre-receive", []byte(fmt.Sprintf(
		`#!/bin/sh
		read oldval newval ref &&
		git cat-file -p $newval^{commit} >/dev/null &&
		echo "$newval" >%q &&
		git cat-file -p $newval^{tag} &&
		exit 1
	`, tagIDOutputPath)))

	response, err := client.UserCreateTag(ctx, &gitalypb.UserCreateTagRequest{
		Repository:     repoProto,
		TagName:        []byte("quarantined-tag"),
		TargetRevision: []byte(commitID),
		User:           gittest.TestUser,
		Timestamp:      timestamppb.New(time.Unix(1600000000, 0)),
		Message:        []byte("message"),
	})

	expectedObject := fmt.Sprintf(`object %s
type commit
tag quarantined-tag
tagger Jane Doe <janedoe@gitlab.com> 1600000000 +0800

message`, commitID)

	if featureflag.UserCreateTagStructuredErrors.IsEnabled(ctx) {
		testhelper.RequireGrpcError(t, errWithDetails(t,
			helper.ErrPermissionDeniedf("reference update denied by custom hooks"),
			&gitalypb.UserCreateTagError{
				Error: &gitalypb.UserCreateTagError_CustomHook{
					CustomHook: &gitalypb.CustomHookError{
						HookType: gitalypb.CustomHookError_HOOK_TYPE_PRERECEIVE,
						Stdout:   []byte(expectedObject),
					},
				},
			},
		), err)
		require.Nil(t, response)
	} else {
		require.NoError(t, err)
		// Conveniently, the pre-receive error will now contain output from our custom hook and thus
		// the tag's contents.
		testhelper.ProtoEqual(t, &gitalypb.UserCreateTagResponse{
			PreReceiveError: expectedObject,
		}, response)
	}

	tagID := text.ChompBytes(testhelper.MustReadFile(t, tagIDOutputPath))

	// In case we use an object quarantine directory, the tag should not exist in the target
	// repository because the RPC failed to update the revision.
	tagExists, err := repo.HasRevision(ctx, git.Revision(tagID+"^{tag}"))
	require.NoError(t, err)
	require.False(t, tagExists, "tag should not have been migrated")
}

func TestUserCreateTag_message(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagMessage)
}

func testUserCreateTagMessage(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	for _, tc := range []struct {
		desc               string
		message            string
		expectedObjectType string
		expectedErr        error
		expectedMessage    string
	}{
		{
			desc:        "error: contains null byte",
			message:     "\000",
			expectedErr: helper.ErrInvalidArgumentf("validating request: tag message contains NUL byte"),
		},
		{
			desc:               "annotated: some control characters",
			message:            "\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008",
			expectedObjectType: "tag",
			expectedMessage:    "\u0001\u0002\u0003\u0004\u0005\u0006\u0007\u0008",
		},
		{
			desc:               "lightweight: empty message",
			message:            "",
			expectedObjectType: "commit",
		},
		{
			desc:               "lightweight: simple whitespace",
			message:            " \t\t",
			expectedObjectType: "commit",
		},
		{
			desc:               "lightweight: whitespace with newlines",
			message:            "\t\n\f\r ",
			expectedObjectType: "commit",
		},
		{
			desc:               "annotated: simple Unicode whitespace",
			message:            "\u00a0",
			expectedObjectType: "tag",
			expectedMessage:    "\u00a0",
		},
		{
			desc:               "lightweight: lots of Unicode whitespace",
			message:            "\u0020\u00a0\u1680\u180e\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u200b\u202f\u205f\u3000\ufeff",
			expectedObjectType: "tag",
			expectedMessage:    "\u0020\u00a0\u1680\u180e\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u200b\u202f\u205f\u3000\ufeff",
		},
		{
			desc:               "annotated: dot",
			message:            ".",
			expectedObjectType: "tag",
			expectedMessage:    ".",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents())
			commit, err := repo.ReadCommit(ctx, commitID.Revision())
			require.NoError(t, err)

			writeAssertObjectTypePreReceiveHook(t, repoPath, tc.expectedObjectType)
			writeAssertObjectTypeUpdateHook(t, repoPath, tc.expectedObjectType)

			request := &gitalypb.UserCreateTagRequest{
				Repository:     repoProto,
				TagName:        []byte("what-will-it-be"),
				TargetRevision: []byte(commitID),
				User:           gittest.TestUser,
				Message:        []byte(tc.message),
			}

			response, err := client.UserCreateTag(ctx, request)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
			if tc.expectedErr == nil {
				response.Tag.Id = ""
				testhelper.ProtoEqual(t, &gitalypb.UserCreateTagResponse{
					Tag: &gitalypb.Tag{
						Name:         []byte("what-will-it-be"),
						Message:      []byte(tc.expectedMessage),
						MessageSize:  int64(len(tc.expectedMessage)),
						TargetCommit: commit,
					},
				}, response)
			} else {
				require.Nil(t, response)
			}
		})
	}
}

func TestUserCreateTag_targetRevision(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagTargetRevision)
}

func testUserCreateTagTargetRevision(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	for _, tc := range []struct {
		desc             string
		targetRevision   string
		expectedRevision string
	}{
		{
			desc:             "unqualified tag",
			targetRevision:   "v1.0.0",
			expectedRevision: "refs/tags/v1.0.0",
		},
		{
			desc:             "parent of unqualified tag",
			targetRevision:   "v1.0.0~",
			expectedRevision: "refs/tags/v1.0.0~",
		},
		{
			desc:             "parent of semi-qualified tag",
			targetRevision:   "tags/v1.0.0~",
			expectedRevision: "refs/tags/v1.0.0~",
		},
		{
			desc:             "parent of fully-qualified tag",
			targetRevision:   "refs/tags/v1.0.0~",
			expectedRevision: "refs/tags/v1.0.0~",
		},
		{
			desc:             "unqualified branch",
			targetRevision:   "main",
			expectedRevision: "refs/heads/main",
		},
		{
			desc:             "fully-qualified branch",
			targetRevision:   "refs/heads/main",
			expectedRevision: "refs/heads/main",
		},
		{
			desc:             "ambiguous branch starting with heads",
			targetRevision:   "heads/main",
			expectedRevision: "refs/heads/main",
		},
		{
			desc:             "ambiguated branch",
			targetRevision:   "heads/heads/main",
			expectedRevision: "refs/heads/heads/main",
		},
		{
			desc:             "deep ambiguous branch",
			targetRevision:   "heads/refs/heads/main",
			expectedRevision: "refs/heads/refs/heads/main",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			baseCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(), gittest.WithMessage("1"))

			// We create an ambiguous branching structure that has "refs/heads/main",
			// "refs/heads/heads/main" and "refs/heads/refs/heads/main" to exercise how
			// we resolve the tag's target revision.
			gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(baseCommit), gittest.WithBranch("main"), gittest.WithMessage("2"))
			gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(baseCommit), gittest.WithBranch("heads/main"), gittest.WithMessage("3"))
			gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(baseCommit), gittest.WithBranch("refs/heads/main"), gittest.WithMessage("4"))

			taggedCommit := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(baseCommit), gittest.WithMessage("5"))
			gittest.WriteTag(t, cfg, repoPath, "v1.0.0", taggedCommit.Revision())

			expectedCommit, err := repo.ReadCommit(ctx, git.Revision(tc.expectedRevision))
			require.NoError(t, err)

			response, err := client.UserCreateTag(ctx, &gitalypb.UserCreateTagRequest{
				Repository:     repoProto,
				TagName:        []byte("tag"),
				TargetRevision: []byte(tc.targetRevision),
				User:           gittest.TestUser,
			})
			require.NoError(t, err)

			testhelper.ProtoEqual(t, response, &gitalypb.UserCreateTagResponse{
				Tag: &gitalypb.Tag{
					Id:           expectedCommit.Id,
					Name:         []byte("tag"),
					TargetCommit: expectedCommit,
				},
			})

			// Perform another sanity check to verify that the tag really does point to
			// the commit we expect it to.
			parsedID := gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "tag")
			require.Equal(t, response.Tag.TargetCommit.Id, text.ChompBytes(parsedID))
		})
	}
}

func TestUserCreateTag_nonCommitTarget(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagNonCommitTarget)
}

func testUserCreateTagNonCommitTarget(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)
	blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("content"))
	treeID := gittest.WriteTree(t, cfg, repoPath, []gittest.TreeEntry{
		{Path: "file", Mode: "100644", Content: "something"},
	})

	for _, tc := range []struct {
		desc               string
		tagName            string
		message            string
		targetRevision     git.Revision
		expectedTag        *gitalypb.Tag
		expectedObjectType string
	}{
		{
			desc:           "lightweight tag to tree",
			tagName:        "lightweight-to-tree",
			targetRevision: treeID.Revision(),
			expectedTag: &gitalypb.Tag{
				Name: []byte("lightweight-to-tree"),
				Id:   treeID.String(),
			},
			expectedObjectType: "tree",
		},
		{
			desc:           "lightweight tag to blob",
			tagName:        "lightweight-to-blob",
			targetRevision: blobID.Revision(),
			expectedTag: &gitalypb.Tag{
				Name: []byte("lightweight-to-blob"),
				Id:   blobID.String(),
			},
			expectedObjectType: "blob",
		},
		{
			desc:           "annotated tag to tree",
			tagName:        "annotated-to-tree",
			targetRevision: treeID.Revision(),
			message:        "This is an annotated tag",
			expectedTag: &gitalypb.Tag{
				Name:        []byte("annotated-to-tree"),
				Message:     []byte("This is an annotated tag"),
				MessageSize: 24,
			},
			expectedObjectType: "tag",
		},
		{
			desc:           "annotated tag to blob",
			tagName:        "annotated-to-blob",
			targetRevision: blobID.Revision(),
			message:        "This is an annotated tag",
			expectedTag: &gitalypb.Tag{
				Name:        []byte("annotated-to-blob"),
				Message:     []byte("This is an annotated tag"),
				MessageSize: 24,
			},
			expectedObjectType: "tag",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			writeAssertObjectTypePreReceiveHook(t, repoPath, tc.expectedObjectType)
			writeAssertObjectTypeUpdateHook(t, repoPath, tc.expectedObjectType)

			response, err := client.UserCreateTag(ctx, &gitalypb.UserCreateTagRequest{
				Repository:     repo,
				TagName:        []byte(tc.tagName),
				TargetRevision: []byte(tc.targetRevision),
				User:           gittest.TestUser,
				Message:        []byte(tc.message),
			})
			require.NoError(t, err)

			// We cannot know the object ID of the annotated tags beforehand, so we just
			// fill in this detail now.
			if len(tc.expectedTag.Id) == 0 {
				tc.expectedTag.Id = text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", tc.tagName))
			}
			testhelper.ProtoEqual(t, &gitalypb.UserCreateTagResponse{
				Tag: tc.expectedTag,
			}, response)

			peeledID := gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", tc.tagName+"^{}")
			require.Equal(t, tc.targetRevision.String(), text.ChompBytes(peeledID))
		})
	}
}

func TestUserCreateTag_nestedTags(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagNestedTags)
}

func testUserCreateTagNestedTags(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, repoProto, repoPath, client := setupOperationsService(t, ctx)

	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	for _, tc := range []struct {
		desc             string
		targetObject     string
		targetObjectType string
		expectedTag      *gitalypb.Tag
	}{
		{
			desc:             "nested tags to commit",
			targetObject:     "c7fbe50c7c7419d9701eebe64b1fdacc3df5b9dd",
			targetObjectType: "commit",
		},
		{
			desc:             "nested tags to tree",
			targetObjectType: "tree",
			targetObject:     "612036fac47c5d31c212b17268e2f3ba807bce1e",
		},
		{
			desc:             "nested tags to blob",
			targetObject:     "dfaa3f97ca337e20154a98ac9d0be76ddd1fcc82",
			targetObjectType: "blob",
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			// We resolve down to commit/tree/blob, but we'll only ever push a "tag"
			// here.
			writeAssertObjectTypePreReceiveHook(t, repoPath, "tag")
			writeAssertObjectTypeUpdateHook(t, repoPath, "tag")

			targetObject := tc.targetObject
			nestLevel := 2
			for i := 0; i <= nestLevel; i++ {
				tagName := fmt.Sprintf("nested-tag-%v", i)
				tagMessage := fmt.Sprintf("This is level %v of a nested annotated tag to %v", i, tc.targetObject)
				request := &gitalypb.UserCreateTagRequest{
					Repository:     repoProto,
					TagName:        []byte(tagName),
					TargetRevision: []byte(targetObject),
					User:           gittest.TestUser,
					Message:        []byte(tagMessage),
				}
				response, err := client.UserCreateTag(ctx, request)
				require.NoError(t, err)
				require.Empty(t, response.PreReceiveError)
				defer gittest.Exec(t, cfg, "-C", repoPath, "tag", "-d", tagName)

				createdID := gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", tagName)
				createdIDStr := text.ChompBytes(createdID)
				responseOk := &gitalypb.UserCreateTagResponse{
					Tag: &gitalypb.Tag{
						Name: request.TagName,
						Id:   createdIDStr,
						// TargetCommit: is dynamically determined, filled in below
						Message:     request.Message,
						MessageSize: int64(len(request.Message)),
					},
				}
				// Fake it up for all levels, except for ^{} == "commit"
				responseOk.Tag.TargetCommit = response.Tag.TargetCommit
				if tc.targetObjectType == "commit" {
					responseOk.Tag.TargetCommit, err = repo.ReadCommit(ctx, git.Revision(tc.targetObject))
					require.NoError(t, err)
				}
				testhelper.ProtoEqual(t, responseOk, response)

				peeledID := gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", tagName+"^{}")
				peeledIDStr := text.ChompBytes(peeledID)
				require.Equal(t, tc.targetObject, peeledIDStr)

				// Set up the next level of nesting...
				targetObject = response.Tag.Id

				// Create a *lightweight* tag pointing
				// to our N-level
				// tag->[commit|tree|blob]. The "tag"
				// field name will not match the tag
				// name.
				tagNameLight := fmt.Sprintf("skip-type-check-light-%s", tagName)
				request = &gitalypb.UserCreateTagRequest{
					Repository:     repoProto,
					TagName:        []byte(tagNameLight),
					TargetRevision: []byte(createdIDStr),
					User:           gittest.TestUser,
				}
				response, err = client.UserCreateTag(ctx, request)
				defer gittest.Exec(t, cfg, "-C", repoPath, "tag", "-d", tagNameLight)
				require.NoError(t, err)
				require.Empty(t, response.PreReceiveError)

				responseOk = &gitalypb.UserCreateTagResponse{
					Tag: &gitalypb.Tag{
						Name:         request.TagName,
						Id:           tc.targetObject,
						TargetCommit: responseOk.Tag.TargetCommit,
						Message:      nil,
						MessageSize:  0,
					},
				}
				testhelper.ProtoEqual(t, responseOk, response)

				createdIDLight := gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", tagNameLight)
				createdIDLightStr := text.ChompBytes(createdIDLight)
				require.Equal(t, tc.targetObject, createdIDLightStr)
			}
		})
	}
}

func TestUserCreateTag_stableTagIDs(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagStableTagIDs)
}

func testUserCreateTagStableTagIDs(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents())
	commit, err := repo.ReadCommit(ctx, commitID.Revision())
	require.NoError(t, err)

	response, err := client.UserCreateTag(ctx, &gitalypb.UserCreateTagRequest{
		Repository:     repoProto,
		TagName:        []byte("happy-tag"),
		TargetRevision: []byte(commitID),
		Message:        []byte("my message"),
		User:           gittest.TestUser,
		Timestamp:      &timestamppb.Timestamp{Seconds: 12345},
	})
	require.NoError(t, err)

	require.Equal(t, &gitalypb.Tag{
		Id:           "d877784c740f492d74e6073de649a6b046ab3656",
		Name:         []byte("happy-tag"),
		Message:      []byte("my message"),
		MessageSize:  10,
		TargetCommit: commit,
	}, response.Tag)
}

func TestUserDeleteTag_prefixedTag(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, repo, repoPath, client := setupOperationsService(t, ctx)

	testCases := []struct {
		desc         string
		tagNameInput string
		tagCommit    string
		user         *gitalypb.User
		response     *gitalypb.UserDeleteTagResponse
		err          error
	}{
		{
			desc:         "possible to delete a tag called refs/tags/something",
			tagNameInput: "refs/tags/can-find-this",
			tagCommit:    "c642fe9b8b9f28f9225d7ea953fe14e74748d53b",
			user:         gittest.TestUser,
			response:     &gitalypb.UserDeleteTagResponse{},
			err:          nil,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			gittest.Exec(t, cfg, "-C", repoPath, "tag", testCase.tagNameInput, testCase.tagCommit)

			request := &gitalypb.UserDeleteTagRequest{
				Repository: repo,
				TagName:    []byte(testCase.tagNameInput),
				User:       testCase.user,
			}

			response, err := client.UserDeleteTag(ctx, request)
			testhelper.RequireGrpcError(t, testCase.err, err)
			testhelper.ProtoEqual(t, testCase.response, response)

			refs := gittest.Exec(t, cfg, "-C", repoPath, "for-each-ref", "--", "refs/tags/"+testCase.tagNameInput)
			require.NotContains(t, string(refs), testCase.tagCommit, "tag kept because we stripped off refs/tags/*")
		})
	}
}

func TestUserCreateTag_prefixedTag(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagPrefixedTag)
}

func testUserCreateTagPrefixedTag(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg)
	repo := localrepo.NewTestRepo(t, cfg, repoProto)

	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents())
	commit, err := repo.ReadCommit(ctx, commitID.Revision())
	require.NoError(t, err)

	// We try to create a tag that has a nested name of "refs/tags/refs/tags/".
	response, err := client.UserCreateTag(ctx, &gitalypb.UserCreateTagRequest{
		Repository:     repoProto,
		TagName:        []byte("refs/tags/can-create-this"),
		TargetRevision: []byte(commitID),
		User:           gittest.TestUser,
	})
	require.NoError(t, err)
	testhelper.ProtoEqual(t, &gitalypb.UserCreateTagResponse{
		Tag: &gitalypb.Tag{
			Name:         []byte("refs/tags/can-create-this"),
			Id:           commitID.String(),
			TargetCommit: commit,
		},
	}, response)

	// Verify that the tag indeed has the awkward but expected name.
	require.Equal(t,
		text.ChompBytes(gittest.Exec(t, cfg, "-C", repoPath, "rev-parse", "refs/tags/refs/tags/can-create-this")),
		commitID.String(),
	)
}

func TestUserCreateTag_gitHooks(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagGitHooks)
}

func testUserCreateTagGitHooks(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	for _, hookName := range GitlabHooks {
		t.Run(hookName, func(t *testing.T) {
			repoProto, repoPath := gittest.CreateRepository(ctx, t, cfg)
			repo := localrepo.NewTestRepo(t, cfg, repoProto)

			commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents())
			commit, err := repo.ReadCommit(ctx, commitID.Revision())
			require.NoError(t, err)

			hookOutputTempPath := gittest.WriteEnvToCustomHook(t, repoPath, hookName)

			response, err := client.UserCreateTag(ctx, &gitalypb.UserCreateTagRequest{
				Repository:     repoProto,
				TagName:        []byte("v1.0.0"),
				TargetRevision: []byte(commitID),
				User:           gittest.TestUser,
			})
			require.NoError(t, err)
			testhelper.ProtoEqual(t, &gitalypb.UserCreateTagResponse{
				Tag: &gitalypb.Tag{
					Name:         []byte("v1.0.0"),
					Id:           commitID.String(),
					TargetCommit: commit,
				},
			}, response)

			output := string(testhelper.MustReadFile(t, hookOutputTempPath))
			require.Contains(t, output, "GL_USERNAME="+gittest.TestUser.GlUsername)
			require.Contains(t, output, "GL_PROJECT_PATH=gitlab-org/gitlab-test")
		})
	}
}

func TestUserDeleteTag_invalidArgument(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, _, repo, _, client := setupOperationsService(t, ctx)

	testCases := []struct {
		desc     string
		request  *gitalypb.UserDeleteTagRequest
		response *gitalypb.UserDeleteTagResponse
		err      error
	}{
		{
			desc: "empty user",
			request: &gitalypb.UserDeleteTagRequest{
				Repository: repo,
				TagName:    []byte("does-matter-the-name-if-user-is-empty"),
			},
			response: nil,
			err:      status.Error(codes.InvalidArgument, "empty user"),
		},
		{
			desc: "empty tag name",
			request: &gitalypb.UserDeleteTagRequest{
				Repository: repo,
				User:       gittest.TestUser,
			},
			response: nil,
			err:      status.Error(codes.InvalidArgument, "empty tag name"),
		},
		{
			desc: "non-existent tag name",
			request: &gitalypb.UserDeleteTagRequest{
				Repository: repo,
				User:       gittest.TestUser,
				TagName:    []byte("i-do-not-exist"),
			},
			response: nil,
			err:      status.Errorf(codes.FailedPrecondition, "tag not found: %s", "i-do-not-exist"),
		},
		{
			desc: "space in tag name",
			request: &gitalypb.UserDeleteTagRequest{
				Repository: repo,
				User:       gittest.TestUser,
				TagName:    []byte("a tag"),
			},
			response: nil,
			err:      status.Errorf(codes.FailedPrecondition, "tag not found: %s", "a tag"),
		},
		{
			desc: "newline in tag name",
			request: &gitalypb.UserDeleteTagRequest{
				Repository: repo,
				User:       gittest.TestUser,
				TagName:    []byte("a\ntag"),
			},
			response: nil,
			err:      status.Errorf(codes.FailedPrecondition, "tag not found: %s", "a\ntag"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			response, err := client.UserDeleteTag(ctx, testCase.request)
			testhelper.RequireGrpcError(t, testCase.err, err)
			testhelper.ProtoEqual(t, testCase.response, response)
		})
	}
}

func TestUserDeleteTag_hookFailure(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	ctx, cfg, repo, repoPath, client := setupOperationsService(t, ctx)

	tagNameInput := "to-be-deleted-soon-tag"
	gittest.Exec(t, cfg, "-C", repoPath, "tag", tagNameInput)
	defer gittest.Exec(t, cfg, "-C", repoPath, "tag", "-d", tagNameInput)

	request := &gitalypb.UserDeleteTagRequest{
		Repository: repo,
		TagName:    []byte(tagNameInput),
		User:       gittest.TestUser,
	}

	hookContent := []byte("#!/bin/sh\necho GL_ID=$GL_ID\nexit 1")

	for _, hookName := range gitlabPreHooks {
		t.Run(hookName, func(t *testing.T) {
			gittest.WriteCustomHook(t, repoPath, hookName, hookContent)

			response, err := client.UserDeleteTag(ctx, request)
			require.NoError(t, err)
			require.Contains(t, response.PreReceiveError, "GL_ID="+gittest.TestUser.GlId)

			tags := gittest.Exec(t, cfg, "-C", repoPath, "tag")
			require.Contains(t, string(tags), tagNameInput, "tag name does not exist in tags list")
		})
	}
}

func TestUserCreateTag_hookFailure(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagHookFailure)
}

func testUserCreateTagHookFailure(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	for _, tc := range []struct {
		hook     string
		hookType gitalypb.CustomHookError_HookType
	}{
		{
			hook:     "pre-receive",
			hookType: gitalypb.CustomHookError_HOOK_TYPE_PRERECEIVE,
		},
		{
			hook:     "update",
			hookType: gitalypb.CustomHookError_HOOK_TYPE_UPDATE,
		},
	} {
		t.Run(tc.hook, func(t *testing.T) {
			repo, repoPath := gittest.CreateRepository(ctx, t, cfg)
			commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents())

			gittest.WriteCustomHook(t, repoPath, tc.hook, []byte(
				"#!/bin/sh\necho GL_ID=$GL_ID\nexit 1"),
			)

			response, err := client.UserCreateTag(ctx, &gitalypb.UserCreateTagRequest{
				Repository:     repo,
				TagName:        []byte("new-tag"),
				TargetRevision: []byte(commitID),
				User:           gittest.TestUser,
			})
			if featureflag.UserCreateTagStructuredErrors.IsEnabled(ctx) {
				testhelper.RequireGrpcError(t, errWithDetails(t,
					helper.ErrPermissionDeniedf("reference update denied by custom hooks"),
					&gitalypb.UserCreateTagError{
						Error: &gitalypb.UserCreateTagError_CustomHook{
							CustomHook: &gitalypb.CustomHookError{
								HookType: tc.hookType,
								Stdout: []byte(
									"GL_ID=" + gittest.TestUser.GlId + "\n",
								),
							},
						},
					},
				), err)
				require.Nil(t, response)
			} else {
				require.NoError(t, err)
				require.Contains(t, response.PreReceiveError, "GL_ID="+gittest.TestUser.GlId)
			}
		})
	}
}

func TestUserCreateTag_preexisting(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagPreexisting)
}

func testUserCreateTagPreexisting(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)
	commitID := gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents())
	gittest.WriteTag(t, cfg, repoPath, "v1.1.0", commitID.Revision())

	for _, tc := range []struct {
		desc             string
		tagName          string
		targetRevision   string
		user             *gitalypb.User
		expectedResponse *gitalypb.UserCreateTagResponse
		expectedErr      error
	}{
		{
			desc:           "simple existing tag",
			tagName:        "v1.1.0",
			targetRevision: commitID.String(),
			user:           gittest.TestUser,
			expectedErr: func() error {
				if featureflag.UserCreateTagStructuredErrors.IsDisabled(ctx) {
					return nil
				}

				return errWithDetails(t,
					helper.ErrAlreadyExistsf("tag reference exists already"),
					&gitalypb.UserCreateTagError{
						Error: &gitalypb.UserCreateTagError_ReferenceExists{
							ReferenceExists: &gitalypb.ReferenceExistsError{
								ReferenceName: []byte("refs/tags/v1.1.0"),
								Oid:           commitID.String(),
							},
						},
					},
				)
			}(),
			expectedResponse: func() *gitalypb.UserCreateTagResponse {
				if featureflag.UserCreateTagStructuredErrors.IsEnabled(ctx) {
					return nil
				}

				return &gitalypb.UserCreateTagResponse{
					Exists: true,
				}
			}(),
		},
		{
			desc:           "existing tag nonexisting target revision",
			tagName:        "v1.1.0",
			targetRevision: "does-not-exist",
			user:           gittest.TestUser,
			expectedErr:    helper.ErrFailedPreconditionf("revspec 'does-not-exist' not found"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			response, err := client.UserCreateTag(ctx, &gitalypb.UserCreateTagRequest{
				Repository:     repo,
				TagName:        []byte(tc.tagName),
				TargetRevision: []byte(tc.targetRevision),
				User:           tc.user,
			})
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
			testhelper.ProtoEqual(t, tc.expectedResponse, response)
		})
	}
}

func TestUserCreateTag_invalidArgument(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testUserCreateTagInvalidArgument)
}

func testUserCreateTagInvalidArgument(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, client := setupOperationsServiceWithoutRepo(t, ctx)

	repo, repoPath := gittest.CreateRepository(ctx, t, cfg)
	gittest.WriteCommit(t, cfg, repoPath, gittest.WithBranch("main"), gittest.WithParents())

	injectedTag := "inject-tag\ntagger . <> 0 +0000\n\nInjected subject\n\n"

	for _, tc := range []struct {
		desc           string
		tagName        string
		targetRevision string
		message        string
		user           *gitalypb.User
		expectedErr    error
	}{
		{
			desc:           "empty target revision",
			tagName:        "shiny-new-tag",
			targetRevision: "",
			user:           gittest.TestUser,
			expectedErr:    helper.ErrInvalidArgumentf("validating request: empty target revision"),
		},
		{
			desc:           "empty user",
			tagName:        "shiny-new-tag",
			targetRevision: "main",
			user:           nil,
			expectedErr:    helper.ErrInvalidArgumentf("validating request: empty user"),
		},
		{
			desc:           "empty starting point",
			tagName:        "new-tag",
			targetRevision: "",
			user:           gittest.TestUser,
			expectedErr:    helper.ErrInvalidArgumentf("validating request: empty target revision"),
		},
		{
			desc:           "non-existing starting point",
			tagName:        "new-tag",
			targetRevision: "i-dont-exist",
			user:           gittest.TestUser,
			expectedErr:    helper.ErrFailedPreconditionf("revspec '%s' not found", "i-dont-exist"),
		},
		{
			desc:           "space in lightweight tag name",
			tagName:        "a tag",
			targetRevision: "main",
			user:           gittest.TestUser,
			expectedErr:    helper.ErrInvalidArgumentf("validating request: invalid tag name: revision can't contain whitespace"),
		},
		{
			desc:           "space in annotated tag name",
			tagName:        "a tag",
			targetRevision: "main",
			message:        "a message",
			user:           gittest.TestUser,
			expectedErr:    helper.ErrInvalidArgumentf("validating request: invalid tag name: revision can't contain whitespace"),
		},
		{
			desc:           "newline in lightweight tag name",
			tagName:        "a\ntag",
			targetRevision: "main",
			user:           gittest.TestUser,
			expectedErr:    helper.ErrInvalidArgumentf("validating request: invalid tag name: revision can't contain whitespace"),
		},
		{
			desc:           "newline in annotated tag name",
			tagName:        "a\ntag",
			targetRevision: "main",
			message:        "a message",
			user:           gittest.TestUser,
			expectedErr:    helper.ErrInvalidArgumentf("validating request: invalid tag name: revision can't contain whitespace"),
		},
		{
			desc:           "injection in lightweight tag name",
			tagName:        injectedTag,
			targetRevision: "main",
			user:           gittest.TestUser,
			expectedErr:    helper.ErrInvalidArgumentf("validating request: invalid tag name: revision can't contain whitespace"),
		},
		{
			desc:           "injection in annotated tag name",
			tagName:        injectedTag,
			targetRevision: "main",
			message:        "a message",
			user:           gittest.TestUser,
			expectedErr:    helper.ErrInvalidArgumentf("validating request: invalid tag name: revision can't contain whitespace"),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			request := &gitalypb.UserCreateTagRequest{
				Repository:     repo,
				TagName:        []byte(tc.tagName),
				TargetRevision: []byte(tc.targetRevision),
				User:           tc.user,
				Message:        []byte(tc.message),
			}

			response, err := client.UserCreateTag(ctx, request)
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
			require.Nil(t, response)
		})
	}
}

func TestTagHookOutput(t *testing.T) {
	t.Parallel()
	testhelper.NewFeatureSets(featureflag.UserCreateTagStructuredErrors).Run(t, testTagHookOutput)
}

func testTagHookOutput(t *testing.T, ctx context.Context) {
	t.Parallel()

	ctx, cfg, repo, repoPath, client := setupOperationsService(t, ctx)

	for _, tc := range []struct {
		desc           string
		hookContent    string
		expectedStdout string
		expectedStderr string
		expectedErr    func(hookPath string) string
	}{
		{
			desc:        "empty stdout and empty stderr",
			hookContent: "#!/bin/sh\nexit 1",
			expectedErr: func(hookPath string) string {
				return fmt.Sprintf("executing custom hooks: error executing %q: exit status 1", hookPath)
			},
		},
		{
			desc:           "empty stdout and some stderr",
			hookContent:    "#!/bin/sh\necho stderr >&2\nexit 1",
			expectedStderr: "stderr\n",
			expectedErr:    func(string) string { return "stderr\n" },
		},
		{
			desc:           "some stdout and empty stderr",
			hookContent:    "#!/bin/sh\necho stdout\nexit 1",
			expectedStdout: "stdout\n",
			expectedErr:    func(string) string { return "stdout\n" },
		},
		{
			desc:           "some stdout and some stderr",
			hookContent:    "#!/bin/sh\necho stdout\necho stderr >&2\nexit 1",
			expectedStdout: "stdout\n",
			expectedStderr: "stderr\n",
			expectedErr:    func(string) string { return "stderr\n" },
		},
		{
			desc:           "whitespace stdout and some stderr",
			hookContent:    "#!/bin/sh\necho '   '\necho stderr >&2\nexit 1",
			expectedStdout: "   \n",
			expectedStderr: "stderr\n",
			expectedErr:    func(string) string { return "stderr\n" },
		},
		{
			desc:           "some stdout and whitespace stderr",
			hookContent:    "#!/bin/sh\necho stdout\necho '   ' >&2\nexit 1",
			expectedStdout: "stdout\n",
			expectedStderr: "   \n",
			expectedErr:    func(string) string { return "stdout\n" },
		},
	} {
		for _, hookTC := range []struct {
			hook     string
			hookType gitalypb.CustomHookError_HookType
		}{
			{
				hook:     "pre-receive",
				hookType: gitalypb.CustomHookError_HOOK_TYPE_PRERECEIVE,
			},
			{
				hook:     "update",
				hookType: gitalypb.CustomHookError_HOOK_TYPE_UPDATE,
			},
		} {
			t.Run(hookTC.hook+"/"+tc.desc, func(t *testing.T) {
				tagNameInput := "some-tag"
				createRequest := &gitalypb.UserCreateTagRequest{
					Repository:     repo,
					TagName:        []byte(tagNameInput),
					TargetRevision: []byte("master"),
					User:           gittest.TestUser,
				}
				deleteRequest := &gitalypb.UserDeleteTagRequest{
					Repository: repo,
					TagName:    []byte(tagNameInput),
					User:       gittest.TestUser,
				}

				hookFilename := gittest.WriteCustomHook(t, repoPath, hookTC.hook, []byte(tc.hookContent))

				createResponse, err := client.UserCreateTag(ctx, createRequest)
				if featureflag.UserCreateTagStructuredErrors.IsEnabled(ctx) {
					testhelper.RequireGrpcError(t, errWithDetails(t,
						helper.ErrPermissionDeniedf("reference update denied by custom hooks"),
						&gitalypb.UserCreateTagError{
							Error: &gitalypb.UserCreateTagError_CustomHook{
								CustomHook: &gitalypb.CustomHookError{
									HookType: hookTC.hookType,
									Stdout:   []byte(tc.expectedStdout),
									Stderr:   []byte(tc.expectedStderr),
								},
							},
						},
					), err)
					require.Nil(t, createResponse)
				} else {
					require.NoError(t, err)

					testhelper.ProtoEqual(t, &gitalypb.UserCreateTagResponse{
						Tag:             createResponse.Tag,
						Exists:          false,
						PreReceiveError: tc.expectedErr(hookFilename),
					}, createResponse)
				}

				defer gittest.Exec(t, cfg, "-C", repoPath, "tag", "-d", tagNameInput)
				gittest.Exec(t, cfg, "-C", repoPath, "tag", tagNameInput)

				deleteResponse, err := client.UserDeleteTag(ctx, deleteRequest)
				require.NoError(t, err)
				deleteResponseOk := &gitalypb.UserDeleteTagResponse{
					PreReceiveError: tc.expectedErr(hookFilename),
				}
				testhelper.ProtoEqual(t, deleteResponseOk, deleteResponse)
			})
		}
	}
}
