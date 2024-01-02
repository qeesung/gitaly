package ref

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/config"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestFindAllTags(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRefService(t)

	sortSetup := setupSortRepository(t, ctx, cfg)

	type setupData struct {
		request      *gitalypb.FindAllTagsRequest
		expectedErr  error
		expectedTags []*gitalypb.Tag
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "unset repository",
			setup: func(t *testing.T) setupData {
				return setupData{
					request:     &gitalypb.FindAllTagsRequest{},
					expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
				}
			},
		},
		{
			desc: "unknown storage",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: &gitalypb.Repository{
							StorageName:  "fake",
							RelativePath: "repo",
						},
					},
					expectedErr: testhelper.ToInterceptedMetadata(structerr.NewInvalidArgument(
						"%w", storage.NewStorageNotFoundError("fake"),
					)),
				}
			},
		},
		{
			desc: "unsupported sorting key",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
						SortBy: &gitalypb.FindAllTagsRequest_SortBy{
							Key: gitalypb.FindAllTagsRequest_SortBy_Key(-1),
						},
					},
					expectedErr: structerr.NewInvalidArgument("unsupported sorting key: -1"),
				}
			},
		},
		{
			desc: "unsupported sorting direction",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
						SortBy: &gitalypb.FindAllTagsRequest_SortBy{
							Key:       gitalypb.FindAllTagsRequest_SortBy_REFNAME,
							Direction: gitalypb.SortDirection(-1),
						},
					},
					expectedErr: structerr.NewInvalidArgument("unsupported sorting direction: -1"),
				}
			},
		},
		{
			desc: "no tags",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
					},
				}
			},
		},
		{
			desc: "lightweight tag",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)
				tagID := gittest.WriteTag(t, cfg, repoPath, commitID.String(), commitID.Revision())

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte(commitID),
							Id:           tagID.String(),
							TargetCommit: commit,
						},
					},
				}
			},
		},
		{
			desc: "annotated tag",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)
				tagID := gittest.WriteTag(t, cfg, repoPath, commitID.String(), commitID.Revision(), gittest.WriteTagConfig{
					Message: "annotated",
				})

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte(commitID),
							Id:           tagID.String(),
							TargetCommit: commit,
							Message:      []byte("annotated"),
							MessageSize:  9,
							Tagger:       gittest.DefaultCommitAuthor,
						},
					},
				}
			},
		},
		{
			desc: "tag pointing to blob",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				blobID := gittest.WriteBlob(t, cfg, repoPath, []byte("tagged data"))
				tagID := gittest.WriteTag(t, cfg, repoPath, "tagged-blob", blobID.Revision(), gittest.WriteTagConfig{
					Message: "annotated",
				})

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:        []byte("tagged-blob"),
							Id:          tagID.String(),
							Message:     []byte("annotated"),
							MessageSize: 9,
							Tagger:      gittest.DefaultCommitAuthor,
						},
					},
				}
			},
		},
		{
			desc: "deeply nested tag",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)

				currentID := commitID
				var expectedTags []*gitalypb.Tag
				for i := 0; i < 20; i++ {
					tagName := fmt.Sprintf("tag-%02d", i)

					currentID = gittest.WriteTag(t, cfg, repoPath, tagName, currentID.Revision(), gittest.WriteTagConfig{
						Message: tagName,
					})

					expectedTags = append(expectedTags, &gitalypb.Tag{
						Name:         []byte(tagName),
						Id:           currentID.String(),
						TargetCommit: commit,
						Message:      []byte(tagName),
						MessageSize:  6,
						Tagger:       gittest.DefaultCommitAuthor,
					})
				}

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
					},
					expectedTags: expectedTags,
				}
			},
		},
		{
			desc: "tag with commit ID as name",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)
				tagID := gittest.WriteTag(t, cfg, repoPath, commitID.String(), commitID.Revision(), gittest.WriteTagConfig{
					Message: "message\n",
				})

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte(commitID),
							Id:           tagID.String(),
							TargetCommit: commit,
							Message:      []byte("message"),
							MessageSize:  7,
							Tagger:       gittest.DefaultCommitAuthor,
						},
					},
				}
			},
		},
		{
			desc: "tag of tag",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)
				parentTagID := gittest.WriteTag(t, cfg, repoPath, "parent-tag", commitID.Revision(), gittest.WriteTagConfig{
					Message: "parent message\n",
				})
				childTagID := gittest.WriteTag(t, cfg, repoPath, "child-tag", parentTagID.Revision(), gittest.WriteTagConfig{
					Message: "child message\n",
				})

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte("child-tag"),
							Id:           childTagID.String(),
							TargetCommit: commit,
							Message:      []byte("child message"),
							MessageSize:  13,
							Tagger:       gittest.DefaultCommitAuthor,
						},
						{
							Name:         []byte("parent-tag"),
							Id:           parentTagID.String(),
							TargetCommit: commit,
							Message:      []byte("parent message"),
							MessageSize:  14,
							Tagger:       gittest.DefaultCommitAuthor,
						},
					},
				}
			},
		},
		{
			desc: "tag of commit with huge commit message",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitMessage := "An empty commit with REALLY BIG message\n\n" + strings.Repeat("a", helper.MaxCommitOrTagMessageSize+1)
				commitID, commit := writeCommit(t, ctx, cfg, repo,
					gittest.WithMessage(commitMessage),
				)
				tagID := gittest.WriteTag(t, cfg, repoPath, "tag", commitID.Revision())

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte("tag"),
							Id:           tagID.String(),
							TargetCommit: commit,
						},
					},
				}
			},
		},
		{
			desc: "tag with huge tag message",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)
				tagMessage := strings.Repeat("a", 11*1024)
				tagID := gittest.WriteTag(t, cfg, repoPath, "tag", commitID.Revision(), gittest.WriteTagConfig{
					Message: tagMessage,
				})

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte("tag"),
							Id:           tagID.String(),
							TargetCommit: commit,
							Message:      []byte(tagMessage[:helper.MaxCommitOrTagMessageSize]),
							MessageSize:  int64(len(tagMessage)),
							Tagger:       gittest.DefaultCommitAuthor,
						},
					},
				}
			},
		},
		{
			desc: "tag with signature",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)
				output := gittest.ExecOpts(t, cfg, gittest.ExecConfig{
					Stdin: strings.NewReader(fmt.Sprintf(
						`object %[1]s
type commit
tag signed-tag
tagger Some Author <some.author@example.com> 100000000 +0100
gpgsig -----BEGIN PGP SIGNATURE-----
 this is a pseudo PGP signature
 -----END PGP SIGNATURE-----

signed tag message
`, commitID)),
				}, "-C", repoPath, "hash-object", "-t", "tag", "--stdin", "-w")
				tagID, err := gittest.DefaultObjectHash.FromHex(text.ChompBytes(output))
				require.NoError(t, err)
				gittest.WriteRef(t, cfg, repoPath, "refs/tags/signed-tag", tagID)

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte("signed-tag"),
							Id:           tagID.String(),
							TargetCommit: commit,
							Message:      []byte("signed tag message"),
							MessageSize:  18,
							Tagger: &gitalypb.CommitAuthor{
								Name:     []byte("Some Author"),
								Email:    []byte("some.author@example.com"),
								Timezone: []byte("+0100"),
								Date:     timestamppb.New(time.Unix(100000000, 0)),
							},
						},
					},
				}
			},
		},
		{
			desc: "duplicated tags",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)
				tagID := gittest.WriteTag(t, cfg, repoPath, "annotated", commitID.Revision(), gittest.WriteTagConfig{
					Message: "annotated tag message",
				})
				gittest.WriteRef(t, cfg, repoPath, "refs/tags/annotated-dup", tagID)
				gittest.WriteRef(t, cfg, repoPath, "refs/tags/lightweight-1", commitID)
				gittest.WriteRef(t, cfg, repoPath, "refs/tags/lightweight-2", commitID)

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte("annotated"),
							Id:           tagID.String(),
							Message:      []byte("annotated tag message"),
							MessageSize:  21,
							Tagger:       gittest.DefaultCommitAuthor,
							TargetCommit: commit,
						},
						{
							Name:         []byte("annotated-dup"),
							Id:           tagID.String(),
							Message:      []byte("annotated tag message"),
							MessageSize:  21,
							Tagger:       gittest.DefaultCommitAuthor,
							TargetCommit: commit,
						},
						{
							Name:         []byte("lightweight-1"),
							Id:           commitID.String(),
							TargetCommit: commit,
						},
						{
							Name:         []byte("lightweight-2"),
							Id:           commitID.String(),
							TargetCommit: commit,
						},
					},
				}
			},
		},
		{
			desc: "pagination with limit exceeding tag count",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)
				gittest.WriteTag(t, cfg, repoPath, "v1", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v2", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v3", commitID.Revision())

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
						PaginationParams: &gitalypb.PaginationParameter{
							Limit: 100,
						},
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte("v1"),
							Id:           commitID.String(),
							TargetCommit: commit,
						},
						{
							Name:         []byte("v2"),
							Id:           commitID.String(),
							TargetCommit: commit,
						},
						{
							Name:         []byte("v3"),
							Id:           commitID.String(),
							TargetCommit: commit,
						},
					},
				}
			},
		},
		{
			desc: "pagination with limit smaller than tag count",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)
				gittest.WriteTag(t, cfg, repoPath, "v1", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v2", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v3", commitID.Revision())

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
						PaginationParams: &gitalypb.PaginationParameter{
							Limit: 2,
						},
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte("v1"),
							Id:           commitID.String(),
							TargetCommit: commit,
						},
						{
							Name:         []byte("v2"),
							Id:           commitID.String(),
							TargetCommit: commit,
						},
					},
				}
			},
		},
		{
			desc: "pagination with page token and no limit",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, _ := writeCommit(t, ctx, cfg, repo)
				gittest.WriteTag(t, cfg, repoPath, "v1", commitID.Revision())

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
						PaginationParams: &gitalypb.PaginationParameter{
							PageToken: "refs/tags/v1",
						},
					},
					// It is certainly unexpected that this case should cause an error. I'm not here
					// to judge though, so I'll let a future developer decide whether this behaviour
					// is desired or not.
					expectedErr: structerr.NewInvalidArgument("could not find page token"),
				}
			},
		},
		{
			desc: "pagination with page token and limit",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)
				gittest.WriteTag(t, cfg, repoPath, "v1", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v2", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v3", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v4", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v5", commitID.Revision())

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
						PaginationParams: &gitalypb.PaginationParameter{
							Limit:     2,
							PageToken: "refs/tags/v2",
						},
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte("v3"),
							Id:           commitID.String(),
							TargetCommit: commit,
						},
						{
							Name:         []byte("v4"),
							Id:           commitID.String(),
							TargetCommit: commit,
						},
					},
				}
			},
		},
		{
			desc: "pagination with page token and reversed sorting",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, commit := writeCommit(t, ctx, cfg, repo)
				gittest.WriteTag(t, cfg, repoPath, "v1", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v2", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v3", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v4", commitID.Revision())
				gittest.WriteTag(t, cfg, repoPath, "v5", commitID.Revision())

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
						PaginationParams: &gitalypb.PaginationParameter{
							Limit:     2,
							PageToken: "refs/tags/v4",
						},
						SortBy: &gitalypb.FindAllTagsRequest_SortBy{
							Key:       gitalypb.FindAllTagsRequest_SortBy_REFNAME,
							Direction: gitalypb.SortDirection_DESCENDING,
						},
					},
					expectedTags: []*gitalypb.Tag{
						{
							Name:         []byte("v3"),
							Id:           commitID.String(),
							TargetCommit: commit,
						},
						{
							Name:         []byte("v2"),
							Id:           commitID.String(),
							TargetCommit: commit,
						},
					},
				}
			},
		},
		{
			desc: "pagination with invalid page token",
			setup: func(t *testing.T) setupData {
				repo, repoPath := gittest.CreateRepository(t, ctx, cfg)

				commitID, _ := writeCommit(t, ctx, cfg, repo)
				gittest.WriteTag(t, cfg, repoPath, "v1", commitID.Revision())

				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: repo,
						PaginationParams: &gitalypb.PaginationParameter{
							PageToken: "refs/tags/v2",
						},
					},
					expectedErr: structerr.NewInvalidArgument("could not find page token"),
				}
			},
		},
		{
			desc: "sort by refname",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: sortSetup.repo,
						SortBy: &gitalypb.FindAllTagsRequest_SortBy{
							Key: gitalypb.FindAllTagsRequest_SortBy_REFNAME,
						},
					},
					expectedTags: []*gitalypb.Tag{
						sortSetup.tag1_0.tag, sortSetup.tag1_10.tag, sortSetup.tag1_2.tag,
					},
				}
			},
		},
		{
			desc: "sort by ascending semantic version",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: sortSetup.repo,
						SortBy: &gitalypb.FindAllTagsRequest_SortBy{
							Key:       gitalypb.FindAllTagsRequest_SortBy_VERSION_REFNAME,
							Direction: gitalypb.SortDirection_ASCENDING,
						},
					},
					expectedTags: []*gitalypb.Tag{
						sortSetup.tag1_0.tag, sortSetup.tag1_2.tag, sortSetup.tag1_10.tag,
					},
				}
			},
		},
		{
			desc: "sort by descending semantic version",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: sortSetup.repo,
						SortBy: &gitalypb.FindAllTagsRequest_SortBy{
							Key:       gitalypb.FindAllTagsRequest_SortBy_VERSION_REFNAME,
							Direction: gitalypb.SortDirection_DESCENDING,
						},
					},
					expectedTags: []*gitalypb.Tag{
						sortSetup.tag1_10.tag, sortSetup.tag1_2.tag, sortSetup.tag1_0.tag,
					},
				}
			},
		},
		{
			desc: "sort by ascending creator date",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: sortSetup.repo,
						SortBy: &gitalypb.FindAllTagsRequest_SortBy{
							Key:       gitalypb.FindAllTagsRequest_SortBy_CREATORDATE,
							Direction: gitalypb.SortDirection_ASCENDING,
						},
					},
					expectedTags: []*gitalypb.Tag{
						sortSetup.tag1_2.tag, sortSetup.tag1_0.tag, sortSetup.tag1_10.tag,
					},
				}
			},
		},
		{
			desc: "sort by descending creator date",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.FindAllTagsRequest{
						Repository: sortSetup.repo,
						SortBy: &gitalypb.FindAllTagsRequest_SortBy{
							Key:       gitalypb.FindAllTagsRequest_SortBy_CREATORDATE,
							Direction: gitalypb.SortDirection_DESCENDING,
						},
					},
					expectedTags: []*gitalypb.Tag{
						sortSetup.tag1_10.tag, sortSetup.tag1_0.tag, sortSetup.tag1_2.tag,
					},
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			stream, err := client.FindAllTags(ctx, setup.request)
			require.NoError(t, err)

			tags, err := testhelper.ReceiveAndFold(stream.Recv, func(
				result []*gitalypb.Tag,
				response *gitalypb.FindAllTagsResponse,
			) []*gitalypb.Tag {
				return append(result, response.GetTags()...)
			})
			testhelper.RequireGrpcError(t, setup.expectedErr, err)
			testhelper.ProtoEqual(t, setup.expectedTags, tags)
		})
	}
}

func BenchmarkFindAllTags(b *testing.B) {
	b.StopTimer()
	ctx := testhelper.Context(b)

	cfg, client := setupRefService(b)

	repoProto, repoPath := gittest.CreateRepository(b, ctx, cfg, gittest.CreateRepositoryConfig{
		Seed: "benchmark.git",
	})

	for i := 0; i < 1000; i++ {
		gittest.WriteTag(b, cfg, repoPath, fmt.Sprintf("%d", i), "HEAD", gittest.WriteTagConfig{
			Message: strings.Repeat("abcdefghijk", i),
		})
	}

	b.Run("FindAllTags", func(b *testing.B) {
		b.ReportAllocs()

		for i := 0; i < b.N; i++ {
			stream, err := client.FindAllTags(ctx, &gitalypb.FindAllTagsRequest{
				Repository: repoProto,
			})
			require.NoError(b, err)

			for {
				_, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					break
				}
				require.NoError(b, err)
			}
		}
	})
}

type sortSetupTag struct {
	commit *gitalypb.GitCommit
	tag    *gitalypb.Tag
	tagID  git.ObjectID
}

type sortSetup struct {
	repo                    *gitalypb.Repository
	repoPath                string
	tag1_0, tag1_2, tag1_10 sortSetupTag
}

func setupSortRepository(tb testing.TB, ctx context.Context, cfg config.Cfg) sortSetup {
	repo, repoPath := gittest.CreateRepository(tb, ctx, cfg)

	return sortSetup{
		repo:     repo,
		repoPath: repoPath,
		tag1_0:   setupTaggedCommit(tb, ctx, cfg, repo, repoPath, "v1.0", time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)),
		tag1_2:   setupTaggedCommit(tb, ctx, cfg, repo, repoPath, "v1.2", time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)),
		tag1_10:  setupTaggedCommit(tb, ctx, cfg, repo, repoPath, "v1.10", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)),
	}
}

func setupTaggedCommit(tb testing.TB, ctx context.Context, cfg config.Cfg, repo *gitalypb.Repository, repoPath string, tagName string, tagDate time.Time) sortSetupTag {
	commitID, commit := writeCommit(tb, ctx, cfg, repo)
	tagID := gittest.WriteTag(tb, cfg, repoPath, tagName, commitID.Revision(), gittest.WriteTagConfig{
		Message: tagName,
		Date:    tagDate,
	})

	return sortSetupTag{
		commit: commit,
		tagID:  tagID,
		tag: &gitalypb.Tag{
			Name:        []byte(tagName),
			Id:          tagID.String(),
			Message:     []byte(tagName),
			MessageSize: int64(len(tagName)),
			Tagger: &gitalypb.CommitAuthor{
				Name:     []byte(gittest.DefaultCommitterName),
				Email:    []byte(gittest.DefaultCommitterMail),
				Date:     timestamppb.New(tagDate),
				Timezone: []byte(tagDate.Format("-0700")),
			},
			TargetCommit: commit,
		},
	}
}
