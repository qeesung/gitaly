package commit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage/storagemgr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper/testserver"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"golang.org/x/text/encoding/charmap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestFindCommits(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupCommitService(t, ctx)

	writeCommit := func(t *testing.T, repoProto *gitalypb.Repository, opts ...gittest.WriteCommitOption) (git.ObjectID, *gitalypb.GitCommit) {
		t.Helper()

		repo := localrepo.NewTestRepo(t, cfg, repoProto)
		repoPath, err := repo.Path()
		require.NoError(t, err)

		commitID := gittest.WriteCommit(t, cfg, repoPath, opts...)
		commitProto, err := repo.ReadCommit(ctx, commitID.Revision())
		require.NoError(t, err)

		return commitID, commitProto
	}

	type setupData struct {
		request         *gitalypb.FindCommitsRequest
		expectedErr     error
		expectedCommits []*gitalypb.GitCommit
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "unset repository",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: nil,
					},
					expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
				}
			},
		},
		{
			desc: "paths with empty string",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Paths:      [][]byte{[]byte("")},
					},
					expectedErr: status.Error(codes.InvalidArgument, "path is empty string"),
				}
			},
		},
		{
			desc: "invalid revision",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte("--output=/meow"),
					},
					expectedErr: status.Error(codes.InvalidArgument, "revision can't start with '-'"),
				}
			},
		},

		{
			desc: "plain commit",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)
				commitID, commit := writeCommit(t, repo)

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Limit:      1,
					},
					expectedCommits: []*gitalypb.GitCommit{commit},
				}
			},
		},
		{
			desc: "encoded commit message",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				windows1250, err := charmap.Windows1250.NewEncoder().String("üöä")
				require.NoError(t, err)

				commitID, commit := writeCommit(t, repo, gittest.WithMessage(windows1250), gittest.WithEncoding("windows-1250"))
				commit.Body = []byte(windows1250)
				commit.Encoding = "windows-1250"

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Limit:      1,
					},
					expectedCommits: []*gitalypb.GitCommit{commit},
				}
			},
		},
		{
			desc: "without commit trailers",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, repo, gittest.WithMessage("message\n\nSigned-off-by: me\n"))
				commit.Trailers = nil

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Trailers:   false,
						Limit:      1,
					},
					expectedCommits: []*gitalypb.GitCommit{commit},
				}
			},
		},
		{
			desc: "with commit trailers",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, repo, gittest.WithMessage("message\n\nSigned-off-by: me\n"))
				commit.Trailers = []*gitalypb.CommitTrailer{
					{Key: []byte("Signed-off-by"), Value: []byte("me")},
				}

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Trailers:   true,
						Limit:      1,
					},
					expectedCommits: []*gitalypb.GitCommit{commit},
				}
			},
		},
		{
			desc: "by author",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				parentID, parent := writeCommit(t, repo, gittest.WithAuthorName("Some author"))
				childID, _ := writeCommit(t, repo, gittest.WithParents(parentID))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(childID),
						Author:     []byte("Some author"),
						Limit:      9000,
					},
					expectedCommits: []*gitalypb.GitCommit{parent},
				}
			},
		},
		{
			desc: "limit by count",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitAID, _ := writeCommit(t, repo)
				commitBID, commitB := writeCommit(t, repo, gittest.WithParents(commitAID))
				commitCID, commitC := writeCommit(t, repo, gittest.WithParents(commitBID))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitCID),
						Limit:      2,
					},
					expectedCommits: []*gitalypb.GitCommit{commitC, commitB},
				}
			},
		},
		{
			desc: "default limit returns no commits",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)
				commitID, _ := writeCommit(t, repo)

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Limit:      0,
					},
				}
			},
		},
		{
			desc: "limit by path",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitAID, commitA := writeCommit(t, repo, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a", Mode: "100644", Content: "a"},
				))
				commitBID, _ := writeCommit(t, repo, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a", Mode: "100644", Content: "a"},
					gittest.TreeEntry{Path: "b", Mode: "100644", Content: "b"},
				), gittest.WithParents(commitAID))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitBID),
						Paths: [][]byte{
							[]byte("a"),
						},
						Limit: 9000,
					},
					expectedCommits: []*gitalypb.GitCommit{commitA},
				}
			},
		},
		{
			desc: "limit by path with wildcard",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitAID, commitA := writeCommit(t, repo, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a-foo", Mode: "100644", Content: "a"},
				))
				commitBID, _ := writeCommit(t, repo, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a-foo", Mode: "100644", Content: "a"},
					gittest.TreeEntry{Path: "b-foo", Mode: "100644", Content: "b"},
				), gittest.WithParents(commitAID))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitBID),
						Paths: [][]byte{
							[]byte("a-*"),
						},
						Limit: 9000,
					},
					expectedCommits: []*gitalypb.GitCommit{commitA},
				}
			},
		},
		{
			desc: "limit by path with non-existent literal pathspec",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitAID, _ := writeCommit(t, repo, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a-foo", Mode: "100644", Content: "a"},
				))
				commitBID, _ := writeCommit(t, repo, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "a-foo", Mode: "100644", Content: "a"},
					gittest.TreeEntry{Path: "b-foo", Mode: "100644", Content: "b"},
				), gittest.WithParents(commitAID))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitBID),
						Paths: [][]byte{
							[]byte("a-*"),
						},
						Limit:         9000,
						GlobalOptions: &gitalypb.GlobalOptions{LiteralPathspecs: true},
					},
					expectedCommits: nil,
				}
			},
		},
		{
			desc: "empty revision uses default branch",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				_, commit := writeCommit(t, repo, gittest.WithBranch(git.DefaultBranch))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Limit:      9000,
					},
					expectedCommits: []*gitalypb.GitCommit{commit},
				}
			},
		},
		{
			desc: "limit by date",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				oldDate := time.Date(2000, 1, 1, 1, 1, 1, 0, time.UTC)
				medDate := time.Date(2010, 1, 1, 1, 1, 1, 0, time.UTC)
				newDate := time.Date(2020, 1, 1, 1, 1, 1, 0, time.UTC)

				oldCommitID, _ := writeCommit(t, repo, gittest.WithCommitterDate(oldDate))
				medCommitID, medCommit := writeCommit(t, repo, gittest.WithCommitterDate(medDate), gittest.WithParents(oldCommitID))
				newCommitID, _ := writeCommit(t, repo, gittest.WithCommitterDate(newDate), gittest.WithParents(medCommitID))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(newCommitID),
						Before:     timestamppb.New(newDate.Add(-1 * time.Second)),
						After:      timestamppb.New(oldDate.Add(+1 * time.Second)),
						Limit:      9000,
					},
					expectedCommits: []*gitalypb.GitCommit{medCommit},
				}
			},
		},
		{
			desc: "skip merges",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				mergeBaseID, mergeBase := writeCommit(t, repo)
				leftCommitID, leftCommit := writeCommit(t, repo, gittest.WithMessage("left"), gittest.WithParents(mergeBaseID))
				rightCommitID, rightCommit := writeCommit(t, repo, gittest.WithMessage("right"), gittest.WithParents(mergeBaseID))
				mergeCommitID, _ := writeCommit(t, repo, gittest.WithParents(leftCommitID, rightCommitID))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(mergeCommitID),
						SkipMerges: true,
						Limit:      9000,
					},
					expectedCommits: []*gitalypb.GitCommit{leftCommit, rightCommit, mergeBase},
				}
			},
		},
		{
			desc: "follow renames",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitAID, commitA := writeCommit(t, repo, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "rename-me", Mode: "100644", Content: "something\n"},
				))
				commitBID, commitB := writeCommit(t, repo, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "renamed", Mode: "100644", Content: "something\n"},
				), gittest.WithParents(commitAID))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitBID),
						Paths: [][]byte{
							[]byte("renamed"),
						},
						Follow: true,
						Limit:  9000,
					},
					expectedCommits: []*gitalypb.GitCommit{commitB, commitA},
				}
			},
		},
		{
			desc: "all references",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				_, commitA := writeCommit(t, repo, gittest.WithMessage("a"), gittest.WithBranch("a"))
				_, commitB := writeCommit(t, repo, gittest.WithMessage("b"), gittest.WithBranch("b"))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						All:        true,
						Limit:      9000,
					},
					expectedCommits: []*gitalypb.GitCommit{commitB, commitA},
				}
			},
		},
		{
			desc: "first parents",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				mergeBaseID, mergeBase := writeCommit(t, repo)
				leftCommitID, leftCommit := writeCommit(t, repo, gittest.WithMessage("left"), gittest.WithParents(mergeBaseID))
				rightCommitID, _ := writeCommit(t, repo, gittest.WithMessage("right"), gittest.WithParents(mergeBaseID))
				mergeCommitID, mergeCommit := writeCommit(t, repo, gittest.WithParents(leftCommitID, rightCommitID))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository:  repo,
						Revision:    []byte(mergeCommitID),
						FirstParent: true,
						Limit:       9000,
					},
					expectedCommits: []*gitalypb.GitCommit{mergeCommit, leftCommit, mergeBase},
				}
			},
		},
		{
			desc: "order by none",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				mergeBaseID, mergeBase := writeCommit(t, repo)
				leftCommitID, leftCommit := writeCommit(t, repo, gittest.WithParents(mergeBaseID), gittest.WithCommitterDate(time.Date(2020, 1, 1, 1, 1, 1, 0, time.UTC)))
				rightCommitID, rightCommit := writeCommit(t, repo, gittest.WithParents(mergeBaseID), gittest.WithCommitterDate(time.Date(2000, 1, 1, 1, 1, 1, 0, time.UTC)))
				mergeCommitID, mergeCommit := writeCommit(t, repo, gittest.WithParents(leftCommitID, rightCommitID))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(mergeCommitID),
						Order:      gitalypb.FindCommitsRequest_NONE,
						Limit:      9000,
					},
					expectedCommits: []*gitalypb.GitCommit{mergeCommit, leftCommit, mergeBase, rightCommit},
				}
			},
		},
		{
			desc: "order by topo",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				mergeBaseID, mergeBase := writeCommit(t, repo)
				leftCommitID, leftCommit := writeCommit(t, repo, gittest.WithParents(mergeBaseID), gittest.WithCommitterDate(time.Date(2020, 1, 1, 1, 1, 1, 0, time.UTC)))
				rightCommitID, rightCommit := writeCommit(t, repo, gittest.WithParents(mergeBaseID), gittest.WithCommitterDate(time.Date(2000, 1, 1, 1, 1, 1, 0, time.UTC)))
				mergeCommitID, mergeCommit := writeCommit(t, repo, gittest.WithParents(leftCommitID, rightCommitID))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(mergeCommitID),
						Order:      gitalypb.FindCommitsRequest_TOPO,
						Limit:      9000,
					},
					// Note how the right commit is sorted immediately after the merge commit now
					// and compare this with the preceding test where it is sorted towards the end.
					expectedCommits: []*gitalypb.GitCommit{mergeCommit, rightCommit, leftCommit, mergeBase},
				}
			},
		},
		{
			desc: "ambiguous reference",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				// The second commit is created with the first commit's object ID as branch name, thus
				// creating ambiguity.
				commitAID, commitA := writeCommit(t, repo, gittest.WithMessage("a"))
				writeCommit(t, repo, gittest.WithMessage("b"), gittest.WithBranch(commitAID.String()))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitAID),
						Limit:      9000,
					},
					expectedCommits: []*gitalypb.GitCommit{commitA},
				}
			},
		},
		{
			desc: "exceeding offset",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitID, _ := writeCommit(t, repo, gittest.WithMessage("a"))

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Limit:      9000,
						Offset:     9000,
					},
					expectedCommits: nil,
				}
			},
		},
		{
			desc: "referenced by tags and branches",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, repo, gittest.WithBranch("branch"))
				gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision())

				commit.ReferencedBy = [][]byte{
					[]byte("refs/tags/v1.0.0"),
					[]byte("refs/heads/branch"),
				}

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Limit:      9000,
						IncludeReferencedBy: [][]byte{
							[]byte("refs/heads/"),
							[]byte("refs/tags"),
						},
					},
					expectedCommits: []*gitalypb.GitCommit{commit},
				}
			},
		},
		{
			desc: "referenced by tags only",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, repo, gittest.WithBranch("branch"))
				gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision())
				commit.ReferencedBy = [][]byte{
					[]byte("refs/tags/v1.0.0"),
				}

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Limit:      9000,
						IncludeReferencedBy: [][]byte{
							[]byte("refs/tags"),
						},
					},
					expectedCommits: []*gitalypb.GitCommit{commit},
				}
			},
		},
		{
			desc: "referenced by HEAD",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, repo, gittest.WithBranch(git.DefaultBranch))
				commit.ReferencedBy = [][]byte{
					[]byte("HEAD"),
				}

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Limit:      9000,
						IncludeReferencedBy: [][]byte{
							[]byte("HEAD"),
						},
					},
					expectedCommits: []*gitalypb.GitCommit{commit},
				}
			},
		},
		{
			desc: "with short stats",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitAID, commitA := writeCommit(t, repo, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "deleted", Mode: "100644", Content: "to be deleted\n"},
					gittest.TreeEntry{Path: "modified", Mode: "100644", Content: "to be modified\n"},
				))
				commitA.ShortStats = &gitalypb.CommitStatInfo{
					Additions: 2, ChangedFiles: 2,
				}

				commitBID, commitB := writeCommit(t, repo, gittest.WithTreeEntries(
					gittest.TreeEntry{Path: "created", Mode: "100644", Content: "created\n"},
					gittest.TreeEntry{Path: "modified", Mode: "100644", Content: "modified\n"},
				), gittest.WithParents(commitAID))
				commitB.ShortStats = &gitalypb.CommitStatInfo{
					Additions: 2, Deletions: 2, ChangedFiles: 3,
				}

				return setupData{
					request: &gitalypb.FindCommitsRequest{
						Repository:       repo,
						Revision:         []byte(commitBID),
						Limit:            9000,
						IncludeShortstat: true,
					},
					expectedCommits: []*gitalypb.GitCommit{commitB, commitA},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			commits, err := getCommits(t, ctx, client, setup.request)
			testhelper.RequireGrpcError(t, setup.expectedErr, err)
			testhelper.ProtoEqual(t, setup.expectedCommits, commits)
		})
	}
}

func TestFindCommits_quarantine(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupCommitService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	altObjectsDir := "./alt-objects"
	commitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithAlternateObjectDirectory(filepath.Join(repoPath, altObjectsDir)),
	)

	for _, tc := range []struct {
		desc          string
		altDirs       []string
		expectedCount int
		expectedErr   error
	}{
		{
			desc:          "present GIT_ALTERNATE_OBJECT_DIRECTORIES",
			altDirs:       []string{altObjectsDir},
			expectedCount: 1,
			expectedErr:   nil,
		},
		{
			desc:          "empty GIT_ALTERNATE_OBJECT_DIRECTORIES",
			altDirs:       []string{},
			expectedCount: 0,
			expectedErr: testhelper.ToInterceptedMetadata(
				structerr.NewNotFound("commits not found").WithMetadata("error", "exit status 128").WithMetadata("stderr",
					fmt.Sprintf("fatal: bad object %s\n", commitID.String()))),
		},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			repo.GitAlternateObjectDirectories = tc.altDirs

			// Rails sends the repository's relative path from the access checks as provided by Gitaly. If transactions are enabled,
			// this is the snapshot's relative path. Include the metadata in the test as well as we're testing requests with quarantine
			// as if they were coming from access checks.
			ctx := metadata.AppendToOutgoingContext(ctx, storagemgr.MetadataKeySnapshotRelativePath,
				// Gitaly sends the snapshot's relative path to Rails from `pre-receive` and Rails
				// sends it back to Gitaly when it performs requests in the access checks. The repository
				// would have already been rewritten by Praefect, so we have to adjust for that as well.
				gittest.RewrittenRepository(t, ctx, cfg, repo).RelativePath,
			)

			commits, err := getCommits(t, ctx, client, &gitalypb.FindCommitsRequest{
				Repository: repo,
				Revision:   []byte(commitID.String()),
				Limit:      1,
			})
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
			require.Len(t, commits, tc.expectedCount)
		})
	}
}

func TestFindCommits_simulateGitLogWaitError(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	logger := testhelper.NewLogger(t)
	cfg, client := setupCommitService(t, ctx, testserver.WithLogger(logger))

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)
	// altObjectsDir is used to trigger git log wait error
	// we will set this to empty string to trigger the error
	altObjectsDir := "./alt-objects"
	commitID := gittest.WriteCommit(t, cfg, repoPath,
		gittest.WithAlternateObjectDirectory(filepath.Join(repoPath, altObjectsDir)),
	)

	for _, tc := range []struct {
		desc          string
		altDirs       []string
		expectedCount int
		expectedErr   error
		limit         int32
	}{
		{
			desc:          "git log exit with error, with limit 0",
			altDirs:       []string{},
			limit:         0,
			expectedCount: 0,
			expectedErr:   nil,
		},
		{
			desc:          "limit 1, git log exit with error",
			altDirs:       []string{},
			limit:         1,
			expectedCount: 0,
			expectedErr: testhelper.ToInterceptedMetadata(
				structerr.NewNotFound("commits not found").
					WithMetadata("error", "exit status 128").WithMetadata("stderr",
					fmt.Sprintf("fatal: bad object %s\n", commitID.String()))),
		},
	} {
		tc := tc
		t.Run(tc.desc, func(t *testing.T) {
			repo.GitAlternateObjectDirectories = tc.altDirs
			commits, err := getCommits(t, ctx, client, &gitalypb.FindCommitsRequest{
				Repository: repo,
				Revision:   []byte(commitID.String()),
				Limit:      tc.limit,
			})
			testhelper.RequireGrpcError(t, tc.expectedErr, err)
			require.Len(t, commits, tc.expectedCount)
		})
	}
}

func TestFindCommits_followWithOffset(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupCommitService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	var commitID git.ObjectID
	for i := 0; i < 10; i++ {
		var parents []git.ObjectID
		if commitID != "" {
			parents = []git.ObjectID{commitID}
		}

		commitID = gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(parents...), gittest.WithTreeEntries(
			gittest.TreeEntry{Path: fmt.Sprintf("%d", i), Mode: "100644", Content: "content that is being moved around\n"},
		))
	}

	for offset := 0; offset < 10; offset++ {
		t.Run(fmt.Sprintf("testing with offset %d", offset), func(t *testing.T) {
			commits, err := getCommits(t, ctx, client, &gitalypb.FindCommitsRequest{
				Repository: repo,
				Revision:   []byte(commitID),
				Follow:     true,
				Paths:      [][]byte{[]byte("9")},
				Offset:     int32(offset),
				Limit:      9000,
			})
			require.NoError(t, err)
			require.Len(t, commits, 10-offset)
		})
	}
}

func getCommits(t *testing.T, ctx context.Context, client gitalypb.CommitServiceClient, request *gitalypb.FindCommitsRequest) ([]*gitalypb.GitCommit, error) {
	t.Helper()

	stream, err := client.FindCommits(ctx, request)
	require.NoError(t, err)

	return testhelper.ReceiveAndFold(stream.Recv, func(
		result []*gitalypb.GitCommit,
		response *gitalypb.FindCommitsResponse,
	) []*gitalypb.GitCommit {
		if response == nil {
			return result
		}

		return append(result, response.GetCommits()...)
	})
}

func BenchmarkCommitStats(b *testing.B) {
	ctx := testhelper.Context(b)
	cfg, client := setupCommitService(b, ctx)

	repo, _ := gittest.CreateRepository(b, ctx, cfg, gittest.CreateRepositoryConfig{
		Seed: "benchmark.git",
	})

	request := &gitalypb.FindCommitsRequest{
		Repository: repo,
		Limit:      100,
		SkipMerges: true,
		All:        true,
		Trailers:   true,
	}

	b.Run("without include_shortstat(N+1 query)", func(b *testing.B) {
		benchmarkCommitStatsN(b, ctx, request, repo, client)
	})

	b.Run("with include_shortstat", func(b *testing.B) {
		benchmarkFindCommitsWithStat(b, ctx, request, client)
	})
}

func benchmarkCommitStatsN(b *testing.B, ctx context.Context, request *gitalypb.FindCommitsRequest,
	repo *gitalypb.Repository, client gitalypb.CommitServiceClient,
) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		stream, err := client.FindCommits(ctx, request)
		require.NoError(b, err)

		for {
			response, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(b, err)

			for _, commit := range response.Commits {
				_, err = client.CommitStats(ctx, &gitalypb.CommitStatsRequest{
					Repository: repo,
					Revision:   []byte(commit.GetId()),
				})
				require.NoError(b, err)
			}
		}
	}
}

func benchmarkFindCommitsWithStat(b *testing.B, ctx context.Context, request *gitalypb.FindCommitsRequest,
	client gitalypb.CommitServiceClient,
) {
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		stream, err := client.FindCommits(ctx, request)
		require.NoError(b, err)

		for {
			_, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			require.NoError(b, err)
		}
	}
}
