package commit

import (
	"bufio"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/localrepo"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"golang.org/x/text/encoding/charmap"
	"google.golang.org/grpc/metadata"
)

func TestFindCommit(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupCommitService(t, ctx)

	writeCommit := func(t *testing.T, repoProto *gitalypb.Repository, opts ...gittest.WriteCommitOption) (git.ObjectID, *gitalypb.GitCommit) {
		t.Helper()

		repo := localrepo.NewTestRepo(t, cfg, repoProto)
		repoPath, err := repo.Path(ctx)
		require.NoError(t, err)

		commitID := gittest.WriteCommit(t, cfg, repoPath, opts...)
		commitProto, err := repo.ReadCommit(ctx, commitID.Revision())
		require.NoError(t, err)

		return commitID, commitProto
	}

	type setupData struct {
		request        *gitalypb.FindCommitRequest
		expectedErr    error
		expectedCommit *gitalypb.GitCommit
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "Repository is nil",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: nil,
					},
					expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
				}
			},
		},
		{
			desc: "unknown storage",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: &gitalypb.Repository{
							StorageName:  "fake",
							RelativePath: "path",
						},
					},
					expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
						"%w", storage.NewStorageNotFoundError("fake"),
					)),
				}
			},
		},
		{
			desc: "unset revision",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
					},
					expectedErr: structerr.NewInvalidArgument("empty revision"),
				}
			},
		},
		{
			desc: "invalid revision",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte("-master"),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't start with '-'"),
				}
			},
		},
		{
			desc: "invalid revision",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte("mas:ter"),
					},
					expectedErr: structerr.NewInvalidArgument("revision can't contain ':'"),
				}
			},
		},
		{
			desc: "non-existent reference",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte("does-not-exist"),
					},
				}
			},
		},
		{
			desc: "non-existent object ID",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte(gittest.DefaultObjectHash.HashData([]byte("nonexistent-commit"))),
					},
				}
			},
		},
		{
			desc: "by branch name",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)
				_, commit := writeCommit(t, repo, gittest.WithBranch("branch"))

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte("branch"),
					},
					expectedCommit: commit,
				}
			},
		},
		{
			desc: "by tag",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, repo)
				gittest.WriteTag(t, cfg, repoPath, "v1.0.0", commitID.Revision(), gittest.WriteTagConfig{
					Message: "tag message",
				})

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte("v1.0.0"),
					},
					expectedCommit: commit,
				}
			},
		},
		{
			desc: "by commit ID",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)
				commitID, commit := writeCommit(t, repo)

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte(commitID),
					},
					expectedCommit: commit,
				}
			},
		},
		{
			desc: "without commit trailer",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)
				commitID, commit := writeCommit(t, repo, gittest.WithMessage("subject\n\nbody\n\nSigned-off-by: foo\n"))

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Trailers:   false,
					},
					expectedCommit: commit,
				}
			},
		},
		{
			desc: "with commit trailer",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, repo, gittest.WithMessage("subject\n\nbody\n\nSigned-off-by: foo\n"))
				commit.Trailers = []*gitalypb.CommitTrailer{
					{Key: []byte("Signed-off-by"), Value: []byte("foo")},
				}

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte(commitID),
						Trailers:   true,
					},
					expectedCommit: commit,
				}
			},
		},
		{
			desc: "with non-UTF8 encoding recognized by Git",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				windows1250, err := charmap.Windows1250.NewEncoder().String("üöä")
				require.NoError(t, err)

				commitID, commit := writeCommit(t, repo, gittest.WithEncoding("windows-1250"), gittest.WithMessage(windows1250))
				commit.Body = []byte(windows1250)
				commit.Encoding = "windows-1250"

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte(commitID),
					},
					expectedCommit: commit,
				}
			},
		},
		{
			desc: "with non-UTF8 encoding not recognized by Git",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, repo, gittest.WithMessage("Hello�orld"))
				commit.Body = []byte("Hello�orld")
				commit.Encoding = ""

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte(commitID),
					},
					expectedCommit: commit,
				}
			},
		},
		{
			desc: "very large commit message",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				bigMessage := "An empty commit with REALLY BIG message\n\n" + strings.Repeat("MOAR!\n", 20*1024)
				commitID, commit := writeCommit(t, repo, gittest.WithMessage(bigMessage))
				commit.Body = []byte(bigMessage[:helper.MaxCommitOrTagMessageSize])

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte(commitID),
					},
					expectedCommit: commit,
				}
			},
		},
		{
			desc: "with different author and committer",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, repo, gittest.WithAuthorName("author"), gittest.WithCommitterName("committer"))
				commit.Author.Name = []byte("author")
				commit.Committer.Name = []byte("committer")

				return setupData{
					request: &gitalypb.FindCommitRequest{
						Repository: repo,
						Revision:   []byte(commitID),
					},
					expectedCommit: commit,
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			response, err := client.FindCommit(ctx, setup.request)
			testhelper.RequireGrpcError(t, setup.expectedErr, err)
			testhelper.ProtoEqual(t, setup.expectedCommit, response.GetCommit())
		})
	}
}

func BenchmarkFindCommitNoCache(b *testing.B) {
	benchmarkFindCommit(b, false)
}

func BenchmarkFindCommitWithCache(b *testing.B) {
	benchmarkFindCommit(b, true)
}

func benchmarkFindCommit(b *testing.B, withCache bool) {
	ctx := testhelper.Context(b)

	cfg, client := setupCommitService(b, ctx)

	repo, _ := gittest.CreateRepository(b, ctx, cfg, gittest.CreateRepositoryConfig{
		Seed: "benchmark.git",
	})

	// get a list of revisions
	gitCmdFactory := gittest.NewCommandFactory(b, cfg)
	logCmd, err := gitCmdFactory.New(ctx, repo, git.Command{
		Name: "log",
		Flags: []git.Option{
			git.Flag{Name: "--format=format:%H"},
		},
	}, git.WithSetupStdout())
	require.NoError(b, err)

	logScanner := bufio.NewScanner(logCmd)

	var revisions []string
	for logScanner.Scan() {
		revisions = append(revisions, logScanner.Text())
	}

	require.NoError(b, logCmd.Wait())

	for i := 0; i < b.N; i++ {
		revision := revisions[b.N%len(revisions)]
		if withCache {
			md := metadata.New(map[string]string{
				"gitaly-session-id": "abc123",
			})

			ctx = metadata.NewOutgoingContext(ctx, md)
		}
		_, err := client.FindCommit(ctx, &gitalypb.FindCommitRequest{
			Repository: repo,
			Revision:   []byte(revision),
		})
		require.NoError(b, err)
	}
}

func TestFindCommit_cached(t *testing.T) {
	t.Parallel()
	ctx := testhelper.Context(t)

	cfg, client := setupCommitService(t, ctx)

	repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

	var commitIDs []git.ObjectID
	for i := 0; i < 10; i++ {
		var parents []git.ObjectID
		if i > 0 {
			parents = []git.ObjectID{commitIDs[i-1]}
		}

		commitIDs = append(commitIDs, gittest.WriteCommit(t, cfg, repoPath, gittest.WithParents(parents...)))
	}

	ctx = metadata.NewOutgoingContext(ctx, metadata.New(map[string]string{
		"gitaly-session-id": "abc123",
	}))

	for _, commitID := range commitIDs {
		_, err := client.FindCommit(ctx, &gitalypb.FindCommitRequest{
			Repository: repo,
			Revision:   []byte(commitID),
		})
		require.NoError(t, err)
	}
}
