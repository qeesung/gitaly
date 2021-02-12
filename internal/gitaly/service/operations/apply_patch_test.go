package operations

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/timestamp"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/internal/git"
	"gitlab.com/gitlab-org/gitaly/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/internal/git2go"
	"gitlab.com/gitlab-org/gitaly/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/proto/go/gitalypb"
	"gitlab.com/gitlab-org/gitaly/streamio"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestUserApplyPatch(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	type actionFunc func(testing.TB, *localrepo.Repo) git2go.Action

	createFile := func(filepath string, content string) actionFunc {
		return func(t testing.TB, repo *localrepo.Repo) git2go.Action {
			fileOID, err := repo.WriteBlob(ctx, filepath, strings.NewReader(content))
			require.NoError(t, err)

			return git2go.CreateFile{Path: filepath, OID: fileOID}
		}
	}

	updateFile := func(filepath string, content string) actionFunc {
		return func(t testing.TB, repo *localrepo.Repo) git2go.Action {
			fileOID, err := repo.WriteBlob(ctx, filepath, strings.NewReader(content))
			require.NoError(t, err)

			return git2go.UpdateFile{Path: filepath, OID: fileOID}
		}
	}

	moveFile := func(oldPath, newPath string) actionFunc {
		return func(testing.TB, *localrepo.Repo) git2go.Action {
			return git2go.MoveFile{Path: oldPath, NewPath: newPath}
		}
	}

	deleteFile := func(filepath string) actionFunc {
		return func(testing.TB, *localrepo.Repo) git2go.Action {
			return git2go.DeleteFile{Path: filepath}
		}
	}

	// commitActions represents actions taken to build a commit.
	type commitActions []actionFunc

	for _, tc := range []struct {
		desc string
		// baseCommit is the commit which the patch commitActions are applied against.
		baseCommit commitActions
		// baseReference is the branch where baseCommit is, by default "master"
		baseReference git.ReferenceName
		// targetBranch is the branch where the patched commit goes
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
		patches       []commitActions
		error         error
		branchCreated bool
		tree          []testhelper.TreeEntry
	}{
		{
			desc:         "creating the first branch does not work",
			targetBranch: "master",
			patches: []commitActions{
				{
					createFile("file", "base-content"),
				},
			},
			error: status.Error(codes.Unknown, "TypeError: no implicit conversion of nil into String"),
		},
		{
			desc:          "creating a new branch from HEAD works",
			baseCommit:    commitActions{createFile("file", "base-content")},
			baseReference: "HEAD",
			extraBranches: []string{"refs/heads/master", "refs/heads/some-extra-branch"},
			targetBranch:  "new-branch",
			patches: []commitActions{
				{
					updateFile("file", "patch 1"),
				},
			},
			branchCreated: true,
			tree: []testhelper.TreeEntry{
				{Mode: "100644", Path: "file", Content: "patch 1"},
			},
		},
		{
			desc:          "creating a new branch from master works",
			baseCommit:    commitActions{createFile("file", "base-content")},
			baseReference: "refs/heads/master",
			extraBranches: []string{"refs/heads/some-extra-branch"},
			targetBranch:  "new-branch",
			patches: []commitActions{
				{
					updateFile("file", "patch 1"),
				},
			},
			branchCreated: true,
			tree: []testhelper.TreeEntry{
				{Mode: "100644", Path: "file", Content: "patch 1"},
			},
		},
		{
			desc:          "creating a new branch from the first listed branch works",
			baseCommit:    commitActions{createFile("file", "base-content")},
			baseReference: "refs/heads/a",
			extraBranches: []string{"refs/heads/b"},
			targetBranch:  "new-branch",
			patches: []commitActions{
				{
					updateFile("file", "patch 1"),
				},
			},
			branchCreated: true,
			tree: []testhelper.TreeEntry{
				{Mode: "100644", Path: "file", Content: "patch 1"},
			},
		},
		{
			desc:          "multiple patches apply cleanly",
			baseCommit:    commitActions{createFile("file", "base-content")},
			baseReference: "refs/heads/master",
			targetBranch:  "master",
			patches: []commitActions{
				{
					updateFile("file", "patch 1"),
				},
				{
					updateFile("file", "patch 1"),
					updateFile("file", "patch 2"),
				},
			},
			tree: []testhelper.TreeEntry{
				{Mode: "100644", Path: "file", Content: "patch 2"},
			},
		},
		{
			desc:          "multiple patches apply via fallback three-way merge",
			baseCommit:    commitActions{createFile("file", "base-content")},
			baseReference: "refs/heads/master",
			targetBranch:  "master",
			patches: []commitActions{
				{
					updateFile("file", "patch 1"),
				},
				{
					updateFile("file", "patch 2"),
					updateFile("file", "patch 1"),
				},
			},
			tree: []testhelper.TreeEntry{
				{Mode: "100644", Path: "file", Content: "patch 1"},
			},
		},
		{
			desc:          "patching fails due to modify-modify conflict",
			baseCommit:    commitActions{createFile("file", "base-content")},
			baseReference: "refs/heads/master",
			targetBranch:  "master",
			patches: []commitActions{
				{
					updateFile("file", "patch 1"),
				},
				{
					updateFile("file", "patch 2"),
				},
			},
			error: status.Error(
				codes.FailedPrecondition,
				`Patch failed at 0002 commit subject
When you have resolved this problem, run "git am --continue".
If you prefer to skip this patch, run "git am --skip" instead.
To restore the original branch and stop patching, run "git am --abort".
`,
			),
		},
		{
			desc:          "patching fails due to add-add conflict",
			baseCommit:    commitActions{createFile("file", "base-content")},
			baseReference: "refs/heads/master",
			targetBranch:  "master",
			patches: []commitActions{
				{
					createFile("added-file", "content-1"),
				},
				{
					createFile("added-file", "content-2"),
				},
			},
			error: status.Error(
				codes.FailedPrecondition,
				`Patch failed at 0002 commit subject
When you have resolved this problem, run "git am --continue".
If you prefer to skip this patch, run "git am --skip" instead.
To restore the original branch and stop patching, run "git am --abort".
`,
			),
		},
		{
			desc:          "patch applies using rename detection",
			baseCommit:    commitActions{createFile("file", "line 1\nline 2\nline 3\nline 4\n")},
			baseReference: "refs/heads/master",
			targetBranch:  "master",
			patches: []commitActions{
				{
					moveFile("file", "moved-file"),
				},
				{
					updateFile("file", "line 1\nline 2\nline 3\nline 4\nadded\n"),
				},
			},
			tree: []testhelper.TreeEntry{
				{Mode: "100644", Path: "moved-file", Content: "line 1\nline 2\nline 3\nline 4\nadded\n"},
			},
		},
		{
			desc:          "patching fails due to delete-modify conflict",
			baseCommit:    commitActions{createFile("file", "base-content")},
			baseReference: "refs/heads/master",
			targetBranch:  "master",
			patches: []commitActions{
				{
					deleteFile("file"),
				},
				{
					updateFile("file", "updated content"),
				},
			},
			error: status.Error(
				codes.FailedPrecondition,
				`Patch failed at 0002 commit subject
When you have resolved this problem, run "git am --continue".
If you prefer to skip this patch, run "git am --skip" instead.
To restore the original branch and stop patching, run "git am --abort".
`,
			),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			serverSocketPath, stop := runOperationServiceServer(t)
			defer stop()

			client, conn := newOperationClient(t, serverSocketPath)
			defer conn.Close()

			repoPb, repoPath, cleanRepo := testhelper.InitBareRepo(t)
			defer cleanRepo()

			repo := localrepo.New(repoPb, config.Config)

			executor := git2go.New(filepath.Join(config.Config.BinDir, "gitaly-git2go"), config.Config.Git.BinPath)

			authorTime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
			committerTime := authorTime.Add(time.Hour)
			author := git2go.NewSignature("Test Author", "author@example.com", authorTime)
			committer := git2go.NewSignature("Overridden By Request User", "overridden@example.com", committerTime)
			commitMessage := "commit subject\n\n\ncommit message body\n\n\n"

			var baseCommit string
			for _, action := range tc.baseCommit {
				var err error
				baseCommit, err = executor.Commit(ctx, git2go.CommitParams{
					Repository: repoPath,
					Author:     author,
					Committer:  committer,
					Message:    commitMessage,
					Parent:     baseCommit,
					Actions:    []git2go.Action{action(t, repo)},
				})
				require.NoError(t, err)
			}

			if baseCommit != "" {
				require.NoError(t, repo.UpdateRef(ctx, tc.baseReference, git.ObjectID(baseCommit), git.ZeroOID))
			}

			if tc.extraBranches != nil {
				emptyCommit, err := executor.Commit(ctx, git2go.CommitParams{
					Repository: repoPath,
					Author:     author,
					Committer:  committer,
					Message:    "empty commit",
				})
				require.NoError(t, err)

				for _, extraBranch := range tc.extraBranches {
					require.NoError(t, repo.UpdateRef(ctx,
						git.NewReferenceNameFromBranchName(extraBranch), git.ObjectID(emptyCommit), git.ZeroOID),
					)
				}
			}

			var patches [][]byte
			for _, commitActions := range tc.patches {
				commit := baseCommit
				for _, action := range commitActions {
					var err error
					commit, err = executor.Commit(ctx, git2go.CommitParams{
						Repository: repoPath,
						Author:     author,
						Committer:  committer,
						Message:    commitMessage,
						Parent:     commit,
						Actions:    []git2go.Action{action(t, repo)},
					})
					require.NoError(t, err)
				}

				formatPatchArgs := []string{"-C", repoPath, "format-patch", "--stdout"}
				if baseCommit == "" {
					formatPatchArgs = append(formatPatchArgs, "--root", commit)
				} else {
					formatPatchArgs = append(formatPatchArgs, commit+"~1.."+commit)
				}

				patches = append(patches, testhelper.MustRunCommand(t, nil, "git", formatPatchArgs...))
			}

			stream, err := client.UserApplyPatch(ctx)
			require.NoError(t, err)

			requestTime := committerTime.Add(time.Hour)
			timestamp, err := ptypes.TimestampProto(requestTime)
			require.NoError(t, err)
			require.NoError(t, stream.Send(&gitalypb.UserApplyPatchRequest{
				UserApplyPatchRequestPayload: &gitalypb.UserApplyPatchRequest_Header_{
					Header: &gitalypb.UserApplyPatchRequest_Header{
						Repository:   repoPb,
						User:         testhelper.TestUser,
						TargetBranch: []byte(tc.targetBranch),
						Timestamp:    timestamp,
					},
				},
			}))

			for _, patch := range patches {
				// we stream the patches one rune at a time to exercise the streaming code
				for _, r := range patch {
					require.NoError(t, stream.Send(&gitalypb.UserApplyPatchRequest{
						UserApplyPatchRequestPayload: &gitalypb.UserApplyPatchRequest_Patches{
							Patches: []byte{r},
						},
					}))
				}
			}

			actualResponse, err := stream.CloseAndRecv()
			if tc.error != nil {
				require.Equal(t, tc.error, err)
				return
			}

			require.NoError(t, err)

			commitID := actualResponse.GetBranchUpdate().GetCommitId()
			actualResponse.GetBranchUpdate().CommitId = ""
			require.Equal(t, &gitalypb.UserApplyPatchResponse{
				BranchUpdate: &gitalypb.OperationBranchUpdate{
					RepoCreated:   false,
					BranchCreated: tc.branchCreated,
				},
			}, actualResponse)

			targetBranchCommit, err := repo.ResolveRevision(ctx,
				git.NewReferenceNameFromBranchName(tc.targetBranch).Revision()+"^{commit}")
			require.NoError(t, err)
			require.Equal(t, targetBranchCommit.String(), commitID)

			actualCommit := git2go.GetCommitAssertion(ctx, t, repo, commitID)
			require.NotEmpty(t, actualCommit.Parent)
			actualCommit.Parent = "" // the parent changes with the patches, we just check it is set

			require.Equal(t,
				git2go.CommitAssertion{
					Author:    author,
					Committer: git2go.NewSignature(string(testhelper.TestUser.Name), string(testhelper.TestUser.Email), requestTime),
					Message:   "commit subject\n\ncommit message body\n",
				},
				actualCommit,
			)

			testhelper.RequireTree(t, repoPath, commitID, tc.tree)
		})
	}
}

func TestSuccessfulUserApplyPatch(t *testing.T) {
	ctx, cancel := testhelper.Context()
	defer cancel()

	testSuccessfulUserApplyPatch(t, ctx)

}

func testSuccessfulUserApplyPatch(t *testing.T, ctx context.Context) {
	serverSocketPath, stop := runOperationServiceServer(t)
	defer stop()

	client, conn := newOperationClient(t, serverSocketPath)
	defer conn.Close()

	repoProto, repoPath, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()
	repo := localrepo.New(repoProto, config.Config)

	testPatchReadme := "testdata/0001-A-commit-from-a-patch.patch"
	testPatchFeature := "testdata/0001-This-does-not-apply-to-the-feature-branch.patch"

	testCases := []struct {
		desc           string
		branchName     string
		branchCreated  bool
		patches        []string
		commitMessages []string
	}{
		{
			desc:           "a new branch",
			branchName:     "patched-branch",
			branchCreated:  true,
			patches:        []string{testPatchReadme},
			commitMessages: []string{"A commit from a patch"},
		},
		{
			desc:           "an existing branch",
			branchName:     "feature",
			branchCreated:  false,
			patches:        []string{testPatchReadme},
			commitMessages: []string{"A commit from a patch"},
		},
		{
			desc:           "multiple patches",
			branchName:     "branch-with-multiple-patches",
			branchCreated:  true,
			patches:        []string{testPatchReadme, testPatchFeature},
			commitMessages: []string{"A commit from a patch", "This does not apply to the `feature` branch"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			stream, err := client.UserApplyPatch(ctx)
			require.NoError(t, err)

			headerRequest := applyPatchHeaderRequest(repoProto, testhelper.TestUser, testCase.branchName)
			require.NoError(t, stream.Send(headerRequest))

			writer := streamio.NewWriter(func(p []byte) error {
				patchRequest := applyPatchPatchesRequest(p)

				return stream.Send(patchRequest)
			})

			for _, patchFileName := range testCase.patches {
				func() {
					file, err := os.Open(patchFileName)
					require.NoError(t, err)
					defer file.Close()

					byteReader := iotest.OneByteReader(file)
					_, err = io.Copy(writer, byteReader)
					require.NoError(t, err)
				}()
			}

			response, err := stream.CloseAndRecv()
			require.NoError(t, err)

			response.GetBranchUpdate()
			require.Equal(t, testCase.branchCreated, response.GetBranchUpdate().GetBranchCreated())

			branches := testhelper.MustRunCommand(t, nil, "git", "-C", repoPath, "branch")
			require.Contains(t, string(branches), testCase.branchName)

			maxCount := fmt.Sprintf("--max-count=%d", len(testCase.commitMessages))

			gitArgs := []string{
				"-C",
				repoPath,
				"log",
				testCase.branchName,
				"--format=%H",
				maxCount,
				"--reverse",
			}

			output := testhelper.MustRunCommand(t, nil, "git", gitArgs...)
			shas := strings.Split(string(output), "\n")
			// Throw away the last element, as that's going to be
			// an empty string.
			if len(shas) > 0 {
				shas = shas[:len(shas)-1]
			}

			for index, sha := range shas {
				commit, err := repo.ReadCommit(ctx, git.Revision(sha))
				require.NoError(t, err)

				require.NotNil(t, commit)
				require.Equal(t, string(commit.Subject), testCase.commitMessages[index])
				require.Equal(t, string(commit.Author.Email), "patchuser@gitlab.org")
				require.Equal(t, string(commit.Committer.Email), string(testhelper.TestUser.Email))
			}
		})
	}
}

func TestUserApplyPatch_stableID(t *testing.T) {
	serverSocketPath, stop := runOperationServiceServer(t)
	defer stop()

	client, conn := newOperationClient(t, serverSocketPath)
	defer conn.Close()

	repoProto, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()
	repo := localrepo.New(repoProto, config.Config)

	ctx, cancel := testhelper.Context()
	defer cancel()

	stream, err := client.UserApplyPatch(ctx)
	require.NoError(t, err)

	require.NoError(t, stream.Send(&gitalypb.UserApplyPatchRequest{
		UserApplyPatchRequestPayload: &gitalypb.UserApplyPatchRequest_Header_{
			Header: &gitalypb.UserApplyPatchRequest_Header{
				Repository:   repoProto,
				User:         testhelper.TestUser,
				TargetBranch: []byte("branch"),
				Timestamp:    &timestamp.Timestamp{Seconds: 1234512345},
			},
		},
	}))

	patch, err := ioutil.ReadFile("testdata/0001-A-commit-from-a-patch.patch")
	require.NoError(t, err)
	require.NoError(t, stream.Send(&gitalypb.UserApplyPatchRequest{
		UserApplyPatchRequestPayload: &gitalypb.UserApplyPatchRequest_Patches{
			Patches: patch,
		},
	}))

	response, err := stream.CloseAndRecv()
	require.NoError(t, err)
	require.True(t, response.BranchUpdate.BranchCreated)

	patchedCommit, err := repo.ReadCommit(ctx, git.Revision("branch"))
	require.NoError(t, err)
	require.Equal(t, &gitalypb.GitCommit{
		Id:     "8cd17acdb54178121167078c78d874d3cc09b216",
		TreeId: "98091f327a9fb132fcb4b490a420c276c653c4c6",
		ParentIds: []string{
			"1e292f8fedd741b75372e19097c76d327140c312",
		},
		Subject:  []byte("A commit from a patch"),
		Body:     []byte("A commit from a patch\n"),
		BodySize: 22,
		Author: &gitalypb.CommitAuthor{
			Name:     []byte("Patch User"),
			Email:    []byte("patchuser@gitlab.org"),
			Date:     &timestamp.Timestamp{Seconds: 1539862835},
			Timezone: []byte("+0200"),
		},
		Committer: &gitalypb.CommitAuthor{
			Name:     testhelper.TestUser.Name,
			Email:    testhelper.TestUser.Email,
			Date:     &timestamp.Timestamp{Seconds: 1234512345},
			Timezone: []byte("+0000"),
		},
	}, patchedCommit)
}

func TestFailedPatchApplyPatch(t *testing.T) {
	serverSocketPath, stop := runOperationServiceServer(t)
	defer stop()

	client, conn := newOperationClient(t, serverSocketPath)
	defer conn.Close()

	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	ctx, cancel := testhelper.Context()
	defer cancel()

	testPatch, err := ioutil.ReadFile("testdata/0001-This-does-not-apply-to-the-feature-branch.patch")
	require.NoError(t, err)

	stream, err := client.UserApplyPatch(ctx)
	require.NoError(t, err)

	headerRequest := applyPatchHeaderRequest(testRepo, testhelper.TestUser, "feature")
	require.NoError(t, stream.Send(headerRequest))

	patchRequest := applyPatchPatchesRequest(testPatch)
	require.NoError(t, stream.Send(patchRequest))

	_, err = stream.CloseAndRecv()
	testhelper.RequireGrpcError(t, err, codes.FailedPrecondition)
}

func TestFailedValidationUserApplyPatch(t *testing.T) {
	testRepo, _, cleanupFn := testhelper.NewTestRepo(t)
	defer cleanupFn()

	testCases := []struct {
		desc         string
		errorMessage string
		repo         *gitalypb.Repository
		user         *gitalypb.User
		branchName   string
	}{
		{
			desc:         "missing Repository",
			errorMessage: "missing Repository",
			branchName:   "new-branch",
			user:         testhelper.TestUser,
		},

		{
			desc:         "missing Branch",
			errorMessage: "missing Branch",
			repo:         testRepo,
			user:         testhelper.TestUser,
		},
		{
			desc:         "empty BranchName",
			errorMessage: "missing Branch",
			repo:         testRepo,
			user:         testhelper.TestUser,
			branchName:   "",
		},
		{
			desc:         "missing User",
			errorMessage: "missing User",
			branchName:   "new-branch",
			repo:         testRepo,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.desc, func(t *testing.T) {
			request := applyPatchHeaderRequest(testCase.repo, testCase.user, testCase.branchName)
			err := validateUserApplyPatchHeader(request.GetHeader())

			require.Contains(t, err.Error(), testCase.errorMessage)
		})
	}
}

func applyPatchHeaderRequest(repo *gitalypb.Repository, user *gitalypb.User, branch string) *gitalypb.UserApplyPatchRequest {
	header := &gitalypb.UserApplyPatchRequest_Header_{
		Header: &gitalypb.UserApplyPatchRequest_Header{
			Repository:   repo,
			User:         user,
			TargetBranch: []byte(branch),
		},
	}
	return &gitalypb.UserApplyPatchRequest{
		UserApplyPatchRequestPayload: header,
	}
}

func applyPatchPatchesRequest(patches []byte) *gitalypb.UserApplyPatchRequest {
	requestPatches := &gitalypb.UserApplyPatchRequest_Patches{
		Patches: patches,
	}

	return &gitalypb.UserApplyPatchRequest{
		UserApplyPatchRequestPayload: requestPatches,
	}
}
