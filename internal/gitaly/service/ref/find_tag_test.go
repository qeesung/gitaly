package ref

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/gitaly/v16/internal/git/gittest"
	"gitlab.com/gitlab-org/gitaly/v16/internal/gitaly/storage"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper"
	"gitlab.com/gitlab-org/gitaly/v16/internal/helper/text"
	"gitlab.com/gitlab-org/gitaly/v16/internal/structerr"
	"gitlab.com/gitlab-org/gitaly/v16/internal/testhelper"
	"gitlab.com/gitlab-org/gitaly/v16/proto/go/gitalypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestFindTag(t *testing.T) {
	t.Parallel()

	ctx := testhelper.Context(t)
	cfg, client := setupRefService(t)

	type setupData struct {
		request     *gitalypb.FindTagRequest
		expectedErr error
		expectedTag *gitalypb.Tag
	}

	for _, tc := range []struct {
		desc  string
		setup func(t *testing.T) setupData
	}{
		{
			desc: "empty request",
			setup: func(t *testing.T) setupData {
				return setupData{
					request:     &gitalypb.FindTagRequest{},
					expectedErr: structerr.NewInvalidArgument("%w", storage.ErrRepositoryNotSet),
				}
			},
		},
		{
			desc: "invalid repo",
			setup: func(t *testing.T) setupData {
				return setupData{
					request: &gitalypb.FindTagRequest{
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
			desc: "empty tag name",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindTagRequest{
						Repository: repo,
					},
					expectedErr: structerr.NewInvalidArgument("tag name is empty"),
				}
			},
		},
		{
			desc: "nonexistent tag",
			setup: func(t *testing.T) setupData {
				repo, _ := gittest.CreateRepository(t, ctx, cfg)

				return setupData{
					request: &gitalypb.FindTagRequest{
						Repository: repo,
						TagName:    []byte("does-not-exist"),
					},
					expectedErr: structerr.NewNotFound("tag does not exist").WithDetail(
						&gitalypb.FindTagError{
							Error: &gitalypb.FindTagError_TagNotFound{
								TagNotFound: &gitalypb.ReferenceNotFoundError{
									ReferenceName: []byte("refs/tags/does-not-exist"),
								},
							},
						},
					),
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
					request: &gitalypb.FindTagRequest{
						Repository: repo,
						TagName:    []byte(commitID),
					},
					expectedTag: &gitalypb.Tag{
						Name:         []byte(commitID),
						Id:           tagID.String(),
						TargetCommit: commit,
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
					request: &gitalypb.FindTagRequest{
						Repository: repo,
						TagName:    []byte(commitID),
					},
					expectedTag: &gitalypb.Tag{
						Name:         []byte(commitID),
						Id:           tagID.String(),
						TargetCommit: commit,
						Message:      []byte("annotated"),
						MessageSize:  9,
						Tagger:       gittest.DefaultCommitAuthor,
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
					request: &gitalypb.FindTagRequest{
						Repository: repo,
						TagName:    []byte("tagged-blob"),
					},
					expectedTag: &gitalypb.Tag{
						Name:        []byte("tagged-blob"),
						Id:          tagID.String(),
						Message:     []byte("annotated"),
						MessageSize: 9,
						Tagger:      gittest.DefaultCommitAuthor,
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
				for i := 0; i < 20; i++ {
					tagName := fmt.Sprintf("tag-%d", i)
					currentID = gittest.WriteTag(t, cfg, repoPath, tagName, currentID.Revision(), gittest.WriteTagConfig{
						Message: tagName,
					})
				}

				return setupData{
					request: &gitalypb.FindTagRequest{
						Repository: repo,
						TagName:    []byte("tag-19"),
					},
					expectedTag: &gitalypb.Tag{
						Name:         []byte("tag-19"),
						Id:           currentID.String(),
						TargetCommit: commit,
						Message:      []byte("tag-19"),
						MessageSize:  6,
						Tagger:       gittest.DefaultCommitAuthor,
					},
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
					request: &gitalypb.FindTagRequest{
						Repository: repo,
						TagName:    []byte(commitID),
					},
					expectedTag: &gitalypb.Tag{
						Name:         []byte(commitID),
						Id:           tagID.String(),
						TargetCommit: commit,
						Message:      []byte("message"),
						MessageSize:  7,
						Tagger:       gittest.DefaultCommitAuthor,
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
					request: &gitalypb.FindTagRequest{
						Repository: repo,
						TagName:    []byte("child-tag"),
					},
					expectedTag: &gitalypb.Tag{
						Name:         []byte("child-tag"),
						Id:           childTagID.String(),
						TargetCommit: commit,
						Message:      []byte("child message"),
						MessageSize:  13,
						Tagger:       gittest.DefaultCommitAuthor,
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
					request: &gitalypb.FindTagRequest{
						Repository: repo,
						TagName:    []byte("tag"),
					},
					expectedTag: &gitalypb.Tag{
						Name:         []byte("tag"),
						Id:           tagID.String(),
						TargetCommit: commit,
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
					request: &gitalypb.FindTagRequest{
						Repository: repo,
						TagName:    []byte("tag"),
					},
					expectedTag: &gitalypb.Tag{
						Name:         []byte("tag"),
						Id:           tagID.String(),
						TargetCommit: commit,
						Message:      []byte(tagMessage[:helper.MaxCommitOrTagMessageSize]),
						MessageSize:  int64(len(tagMessage)),
						Tagger:       gittest.DefaultCommitAuthor,
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
					request: &gitalypb.FindTagRequest{
						Repository: repo,
						TagName:    []byte("signed-tag"),
					},
					expectedTag: &gitalypb.Tag{
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
				}
			},
		},
	} {
		tc := tc

		t.Run(tc.desc, func(t *testing.T) {
			t.Parallel()

			setup := tc.setup(t)

			response, err := client.FindTag(ctx, setup.request)
			testhelper.RequireGrpcError(t, setup.expectedErr, err)
			testhelper.ProtoEqual(t, setup.expectedTag, response.GetTag())
		})
	}
}
